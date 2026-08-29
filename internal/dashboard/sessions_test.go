package dashboard

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/event"
	"github.com/your-org/pulsemetry/internal/session"
	"github.com/your-org/pulsemetry/internal/store"
)

func seedSessions(f *fixture) {
	f.write(store.Batch{Sessions: []session.Session{
		newSession("s-old", testNow.Add(-72*time.Hour)),
		newSession("s-mid", testNow.Add(-24*time.Hour), codex, func(s *session.Session) {
			s.WorkspacePath = workspaceB
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
			q:    SessionQuery{Since: testNow.Add(-25 * time.Hour).Unix()},
			want: []string{"s-new", "s-mid"},
		},
		{
			name: "Until 은 배타",
			q:    SessionQuery{Until: testNow.Add(-24 * time.Hour).Unix()},
			want: []string{"s-old"},
		},
		{
			// v3 에는 status 컬럼이 없다. ended_at IS NULL 이 곧 running 이다 (ADR 0009).
			name: "상태 필터",
			q:    SessionQuery{Status: []string{StatusRunning}},
			want: []string{"s-new"},
		},
		{
			name: "상태 여러 개",
			q:    SessionQuery{Status: []string{StatusRunning, StatusCompleted}},
			want: []string{"s-new", "s-mid", "s-old"},
		},
		{
			// ADR 0009 가 산출하지 않기로 한 두 상태는 어휘에는 남지만 결과는 항상 비어 있다.
			name: "abandoned·handoff 는 항상 빈 결과",
			q:    SessionQuery{Status: []string{"abandoned", "handoff"}},
			want: []string{},
		},
		{
			name: "벤더 필터",
			q:    SessionQuery{Vendor: vendorCodex},
			want: []string{"s-mid"},
		},
		{
			name: "워크스페이스 필터",
			q:    SessionQuery{WorkspacePath: workspaceB},
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
				got[i] = r.SessionKey
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
		switch r.SessionKey {
		case "s-new":
			if r.EndedAt != nil {
				t.Errorf("진행 중 세션의 EndedAt = %v, want null", *r.EndedAt)
			}
			if r.Status != StatusRunning {
				t.Errorf("Status = %q, want %q", r.Status, StatusRunning)
			}
		default:
			if r.EndedAt == nil {
				t.Errorf("%s 의 EndedAt 이 null 이다", r.SessionKey)
			}
			if r.Status != StatusCompleted {
				t.Errorf("%s Status = %q, want %q", r.SessionKey, r.Status, StatusCompleted)
			}
		}
	}
}

// ID 는 sessions.id 이고 SessionKey 는 벤더의 문자열이다. 서로 다른 벤더가 같은 키를 써도
// 두 세션은 별개여야 한다 — v3 의 UNIQUE 가 (vendor_id, session_key) 이기 때문이다.
func TestSessionsIdentityIsSurrogateKey(t *testing.T) {
	f := newFixture(t)
	f.write(store.Batch{Sessions: []session.Session{
		newSession("shared-key", testNow.Add(-2*time.Hour)),
		newSession("shared-key", testNow.Add(-time.Hour), codex),
	}})

	rows, err := f.reader.Sessions(context.Background(), SessionQuery{})
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("세션 = %d건, want 2 (같은 session_key, 다른 벤더)", len(rows))
	}
	if rows[0].ID == rows[1].ID || rows[0].ID <= 0 {
		t.Fatalf("ID = %d/%d — 두 세션이 같은 대리 키를 받았다", rows[0].ID, rows[1].ID)
	}
	if rows[0].SessionKey != rows[1].SessionKey {
		t.Errorf("SessionKey 가 서로 다르다: %q/%q", rows[0].SessionKey, rows[1].SessionKey)
	}
}

func TestSessionDetail(t *testing.T) {
	f := newFixture(t)
	at := testNow.Add(-time.Hour)
	f.write(store.Batch{
		Sessions: []session.Session{newSession("s-detail", at)},
		Events: []store.EventRecord{
			promptRecord("s-detail", "turn-1", at, 1, "runner.go 를 고쳐 줘"),
			llmRecord("s-detail", "turn-1", at.Add(time.Second), 2,
				llmSpec{Model: "claude-sonnet-4", Cost: 1.5, Input: 300, Output: 90, CacheRead: 20, CacheWrit: 10}),
			toolRecord("s-detail", "turn-1", "call-read", at.Add(2*time.Second), 3, toolSpec{
				ToolName: "Read", Success: event.Some(true), Target: workspaceA + "/runner.go",
			}),
			toolRecord("s-detail", "turn-1", "call-edit", at.Add(5*time.Second), 4, toolSpec{
				ToolName: "Edit", Success: event.Some(false), ErrorType: "conflict",
				Target: workspaceA + "/runner.go",
				File:   fileChange(workspaceA+"/runner.go", 30, 6),
			}),
			toolRecord("s-detail", "turn-1", "call-apply", at.Add(7*time.Second), 5, toolSpec{
				ToolName: "Edit", Success: event.Some(true),
				Target: workspaceA + "/apply.go",
				File:   fileChange(workspaceA+"/apply.go", 5, 1),
			}),
			// 성공 여부를 모르는 호출. 실패와 다르다.
			toolRecord("s-detail", "turn-1", "call-bash", at.Add(9*time.Second), 6, toolSpec{
				ToolName: "Bash", MCPServer: "github",
			}),
		},
	})

	id := f.sessionID(vendorClaude, "s-detail")
	got, err := f.reader.Session(context.Background(), id)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if !got.Found {
		t.Fatal("Found = false — 방금 넣은 세션을 못 찾았다")
	}

	s := got.Session
	switch {
	case s.CostUSD != 1.5:
		t.Errorf("cost = %v, want 1.5", s.CostUSD)
	case s.InputTokens != 300 || s.OutputTokens != 90:
		t.Errorf("토큰 = %d/%d, want 300/90", s.InputTokens, s.OutputTokens)
	case s.CacheReadTokens != 20 || s.CacheCreationTokens != 10:
		t.Errorf("캐시 토큰 = %d/%d, want 20/10", s.CacheReadTokens, s.CacheCreationTokens)
	case s.APIRequests != 1:
		t.Errorf("api_requests = %d, want 1", s.APIRequests)
	case s.ToolCalls != 4:
		t.Errorf("tool_calls = %d, want 4", s.ToolCalls)
	case s.ToolErrors != 1:
		t.Errorf("tool_errors = %d, want 1 (미상은 실패가 아니다)", s.ToolErrors)
	case s.Prompts != 1:
		t.Errorf("prompts = %d, want 1", s.Prompts)
	case s.LinesAdded != 35 || s.LinesRemoved != 7:
		t.Errorf("라인 = +%d/-%d, want +35/-7", s.LinesAdded, s.LinesRemoved)
	case s.ProjectName != "telemetryctl":
		t.Errorf("ProjectName = %q, want telemetryctl (워크스페이스 basename)", s.ProjectName)
	case s.WorkspacePath != workspaceA:
		t.Errorf("WorkspacePath = %q", s.WorkspacePath)
	case s.DurationMS != 600_000:
		t.Errorf("duration_ms = %d, want 600000 (ended_at - started_at)", s.DurationMS)
	}

	// 파일은 변경량 내림차순이다 (계획서 지정).
	if len(got.Files) != 2 || got.Files[0].FileName != "runner.go" {
		t.Fatalf("파일 순서 = %+v, want runner.go 먼저", got.Files)
	}
	if got.Files[0].FileExt != "go" || got.Files[0].FilePath != workspaceA+"/runner.go" {
		t.Errorf("파일 = %+v", got.Files[0])
	}

	// 툴 타임라인은 called_at 오름차순이다.
	if len(got.Tools) != 4 {
		t.Fatalf("툴 = %d건, want 4", len(got.Tools))
	}
	if got.Tools[0].ToolName != "Read" || got.Tools[3].ToolName != "Bash" {
		t.Errorf("툴 순서 = %v", got.Tools)
	}
	if got.Tools[1].Success == nil || *got.Tools[1].Success {
		t.Errorf("두 번째 툴 success = %v, want false", got.Tools[1].Success)
	}
	// 성공 여부 미상은 실패와 다르다 — null 로 남아야 한다.
	if got.Tools[3].Success != nil {
		t.Errorf("네 번째 툴 success = %v, want null", *got.Tools[3].Success)
	}
	if got.Tools[0].TargetName != "runner.go" {
		t.Errorf("TargetName = %q, want runner.go", got.Tools[0].TargetName)
	}
	if got.ToolsTruncated {
		t.Error("ToolsTruncated = true — 4건인데 잘렸다고 보고했다")
	}

	if len(got.MCP) != 1 || got.MCP[0].ServerName != "github" || got.MCP[0].ToolCalls != 1 {
		t.Fatalf("MCP = %+v, want github 1건", got.MCP)
	}
}

// 없는 세션은 에러가 아니라 Found=false 다. 보존 정책이 지운 id 를 화면이 들고 있는 것은 정상이다.
func TestSessionMissingIsNotError(t *testing.T) {
	f := newFixture(t)
	seedSessions(f)

	for _, id := range []int64{999_999, 0, -1} {
		got, err := f.reader.Session(context.Background(), id)
		if err != nil {
			t.Fatalf("Session(%d): %v", id, err)
		}
		if got.Found {
			t.Errorf("Session(%d).Found = true", id)
		}
		if got.Files == nil || got.Tools == nil || got.MCP == nil {
			t.Errorf("Session(%d) 의 슬라이스가 nil — JSON 에서 null 이 되어 프런트엔드가 터진다", id)
		}
	}
}

// 긴 타임라인은 잘리되 잘렸다는 사실을 알려야 한다.
func TestSessionToolTimelineTruncates(t *testing.T) {
	f := newFixture(t)
	at := testNow.Add(-time.Hour)

	recs := make([]store.EventRecord, 0, maxToolEvents+5)
	for i := range maxToolEvents + 5 {
		recs = append(recs, toolRecord("s-long", "turn-long", fmt.Sprintf("call-%04d", i),
			at.Add(time.Duration(i)*time.Second), i, toolSpec{ToolName: "Read"}))
	}
	f.write(store.Batch{
		Sessions: []session.Session{newSession("s-long", at)},
		Events:   recs,
	})

	got, err := f.reader.Session(context.Background(), f.sessionID(vendorClaude, "s-long"))
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

// 진행 중인 세션의 길이는 마지막 활동까지다. ended_at 이 NULL 이라고 0 이 되면 화면의
// "소요" 열이 모든 진행 중 세션에서 0 으로 멈춘다.
func TestSessionDurationOfRunningSession(t *testing.T) {
	f := newFixture(t)
	at := testNow.Add(-2 * time.Hour)
	f.write(store.Batch{
		Sessions: []session.Session{newSession("s-run", at, running)},
		Events: []store.EventRecord{
			promptRecord("s-run", "t-run", at.Add(30*time.Minute), 1, "계속 진행"),
		},
	})

	rows, err := f.reader.Sessions(context.Background(), SessionQuery{})
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("세션 = %d건", len(rows))
	}
	if rows[0].LastEventAt != at.Add(30*time.Minute).Unix() {
		t.Errorf("LastEventAt = %d, want %d (마지막 이벤트)",
			rows[0].LastEventAt, at.Add(30*time.Minute).Unix())
	}
	if rows[0].DurationMS != int64((30 * time.Minute).Milliseconds()) {
		t.Errorf("duration_ms = %d, want %d", rows[0].DurationMS, (30 * time.Minute).Milliseconds())
	}
}
