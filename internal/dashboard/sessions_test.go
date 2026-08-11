package dashboard

import (
	"context"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/event"
	"github.com/your-org/pulsemetry/internal/session"
	"github.com/your-org/pulsemetry/internal/store"
)

func seedSessions(f *fixture) {
	running := func(s *session.Session) {
		s.Status = session.StatusRunning
		s.EndedAt = event.Opt[event.UnixSec]{}
	}
	f.write(store.Batch{Sessions: []session.Session{
		newSession("s-old", testNow.Add(-72*time.Hour)),
		newSession("s-mid", testNow.Add(-24*time.Hour), func(s *session.Session) {
			s.Vendor = "codex"
			s.ProjectHash = "hash-b"
			s.ProjectName = "pulsemetry-backend"
		}),
		newSession("s-new", testNow.Add(-time.Hour), running),
	}})
}

func TestSessionsOrderAndFilters(t *testing.T) {
	f := newFixture(t)
	seedSessions(f)
	ctx := context.Background()

	tests := []struct {
		name string
		q    SessionQuery
		want []string
	}{
		{
			name: "기본은 started_at 내림차순",
			q:    SessionQuery{},
			want: []string{"s-new", "s-mid", "s-old"},
		},
		{
			name: "Since 로 자르기",
			q:    SessionQuery{Since: event.SecFromTime(testNow.Add(-25 * time.Hour)).Time().Unix()},
			want: []string{"s-new", "s-mid"},
		},
		{
			name: "Until 은 배타",
			q:    SessionQuery{Until: event.SecFromTime(testNow.Add(-24 * time.Hour)).Time().Unix()},
			want: []string{"s-old"},
		},
		{
			name: "상태 필터",
			q:    SessionQuery{Status: []string{string(session.StatusRunning)}},
			want: []string{"s-new"},
		},
		{
			name: "상태 여러 개",
			q:    SessionQuery{Status: []string{string(session.StatusRunning), string(session.StatusCompleted)}},
			want: []string{"s-new", "s-mid", "s-old"},
		},
		{
			name: "벤더 필터",
			q:    SessionQuery{Vendor: "codex"},
			want: []string{"s-mid"},
		},
		{
			name: "프로젝트 필터",
			q:    SessionQuery{ProjectHash: "hash-b"},
			want: []string{"s-mid"},
		},
		{
			name: "Limit 과 Offset",
			q:    SessionQuery{Limit: 1, Offset: 1},
			want: []string{"s-mid"},
		},
		{
			name: "없는 상태는 빈 결과",
			q:    SessionQuery{Status: []string{"nonexistent"}},
			want: []string{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := f.reader.Sessions(ctx, tc.q)
			if err != nil {
				t.Fatalf("Sessions: %v", err)
			}
			got := make([]string, len(rows))
			for i, r := range rows {
				got[i] = r.SessionID
			}
			if len(got) != len(tc.want) {
				t.Fatalf("세션 = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("세션 = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// 진행 중 세션의 ended_at 은 null 이어야 한다. 0 으로 눕히면 화면이 1970년에 끝난 세션으로 읽는다.
func TestSessionsEndedAtNullWhileRunning(t *testing.T) {
	f := newFixture(t)
	seedSessions(f)

	rows, err := f.reader.Sessions(context.Background(), SessionQuery{})
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	for _, r := range rows {
		switch r.SessionID {
		case "s-new":
			if r.EndedAt != nil {
				t.Errorf("진행 중 세션의 EndedAt = %v, want null", *r.EndedAt)
			}
		default:
			if r.EndedAt == nil {
				t.Errorf("%s 의 EndedAt 이 null 이다", r.SessionID)
			}
		}
	}
}

func TestSessionDetail(t *testing.T) {
	f := newFixture(t)
	sec := event.SecFromTime(testNow.Add(-time.Hour))
	s := newSession("s-detail", testNow.Add(-time.Hour), func(s *session.Session) {
		s.LinesAdded = 40
		s.LinesRemoved = 12
		s.Files = []session.File{
			{PathHash: "h1", Name: "apply.go", Ext: "go", LinesAdded: 5, LinesRemoved: 1, Edits: 1, LastTS: sec},
			{PathHash: "h2", Name: "runner.go", Ext: "go", LinesAdded: 30, LinesRemoved: 6, Edits: 3, LastTS: sec},
		}
		s.Tools = []session.ToolEvent{
			{TS: sec, ToolName: "Read", Action: session.ActionRead, TargetName: "runner.go", TargetHash: "h2", Success: event.Some(true)},
			{TS: sec + 5, ToolName: "Edit", Action: session.ActionEdit, TargetName: "runner.go", TargetHash: "h2", Success: event.Some(false), ErrorType: "conflict"},
			{TS: sec + 9, ToolName: "Bash", Action: session.ActionRun, TargetName: "go"},
		}
		s.MCP = []session.MCPUsage{
			{ServerName: "github", Connected: true, ToolCalls: 0},
			{ServerName: "postgres", ConnectFailures: 3},
		}
	})
	f.write(store.Batch{Sessions: []session.Session{s}})

	got, err := f.reader.Session(context.Background(), "s-detail")
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if !got.Found {
		t.Fatal("Found = false — 방금 넣은 세션을 못 찾았다")
	}
	if got.Session.LinesAdded != 40 {
		t.Errorf("세션 합계 lines_added = %d, want 40", got.Session.LinesAdded)
	}

	// 파일은 변경량 내림차순이다 (계획서 지정).
	if len(got.Files) != 2 || got.Files[0].FileName != "runner.go" {
		t.Fatalf("파일 순서 = %+v, want runner.go 먼저", got.Files)
	}

	// 툴 타임라인은 ts 오름차순이다.
	if len(got.Tools) != 3 {
		t.Fatalf("툴 = %d건, want 3", len(got.Tools))
	}
	if got.Tools[0].ToolName != "Read" || got.Tools[2].ToolName != "Bash" {
		t.Errorf("툴 순서 = %v/%v/%v", got.Tools[0].ToolName, got.Tools[1].ToolName, got.Tools[2].ToolName)
	}
	if got.Tools[1].Success == nil || *got.Tools[1].Success {
		t.Errorf("두 번째 툴 success = %v, want false", got.Tools[1].Success)
	}
	// 성공 여부 미상은 실패와 다르다 — null 로 남아야 한다.
	if got.Tools[2].Success != nil {
		t.Errorf("세 번째 툴 success = %v, want null", *got.Tools[2].Success)
	}
	if got.ToolsTruncated {
		t.Error("ToolsTruncated = true — 3건인데 잘렸다고 보고했다")
	}

	if len(got.MCP) != 2 || got.MCP[0].ServerName != "github" {
		t.Fatalf("MCP = %+v, want github 먼저 (이름 오름차순)", got.MCP)
	}
	if !got.MCP[0].Connected || got.MCP[0].ToolCalls != 0 {
		t.Errorf("github MCP = %+v, want connected=true tool_calls=0", got.MCP[0])
	}
}

// 없는 세션은 에러가 아니라 Found=false 다. 보존 정책이 지운 id 를 화면이 들고 있는 것은 정상이다.
func TestSessionMissingIsNotError(t *testing.T) {
	f := newFixture(t)
	seedSessions(f)

	for _, id := range []string{"없는-세션", ""} {
		got, err := f.reader.Session(context.Background(), id)
		if err != nil {
			t.Fatalf("Session(%q): %v", id, err)
		}
		if got.Found {
			t.Errorf("Session(%q).Found = true", id)
		}
		if got.Files == nil || got.Tools == nil || got.MCP == nil {
			t.Errorf("Session(%q) 의 슬라이스가 nil — JSON 에서 null 이 되어 프런트엔드가 터진다", id)
		}
	}
}

// 긴 타임라인은 잘리되 잘렸다는 사실을 알려야 한다.
func TestSessionToolTimelineTruncates(t *testing.T) {
	f := newFixture(t)
	sec := event.SecFromTime(testNow.Add(-time.Hour))
	tools := make([]session.ToolEvent, maxToolEvents+5)
	for i := range tools {
		tools[i] = session.ToolEvent{
			TS:       sec + event.UnixSec(i),
			ToolName: "Read",
		}
	}
	f.write(store.Batch{Sessions: []session.Session{
		newSession("s-long", testNow.Add(-time.Hour), func(s *session.Session) { s.Tools = tools }),
	}})

	got, err := f.reader.Session(context.Background(), "s-long")
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if len(got.Tools) != maxToolEvents {
		t.Fatalf("툴 = %d건, want %d", len(got.Tools), maxToolEvents)
	}
	if !got.ToolsTruncated {
		t.Error("ToolsTruncated = false — 조용히 잘렸다")
	}
}
