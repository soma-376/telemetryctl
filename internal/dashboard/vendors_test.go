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

// Settings 「연결 상태」 — vendors.last_seen.
func TestVendors(t *testing.T) {
	f := newFixture(t)
	recent := testNow.Add(-2 * time.Hour)
	old := testNow.Add(-72 * time.Hour)

	f.write(store.Batch{
		Sessions: []session.Session{
			newSession("s-recent", recent, running),
			newSession("s-old", old, codex),
		},
		Events: []store.EventRecord{
			promptRecord("s-recent", "t-r1", recent, 1, "첫 프롬프트"),
			promptRecord("s-recent", "t-r2", recent.Add(time.Minute), 2, "두 번째 프롬프트"),
			func() store.EventRecord {
				r := promptRecord("s-old", "t-o1", old, 3, "오래된 프롬프트")
				r.Event.Vendor = vendorCodex
				r.Event.Name = vendorCodex + ".user_prompt"
				return r
			}(),
		},
	})

	rows, err := f.reader.Vendors(context.Background())
	if err != nil {
		t.Fatalf("Vendors: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("벤더 = %d, want 2 (%+v)", len(rows), rows)
	}
	// last_seen 내림차순.
	if rows[0].Vendor != vendorClaude {
		t.Fatalf("1행 = %q, want claude_code", rows[0].Vendor)
	}
	if !rows[0].Connected {
		t.Error("최근에 본 벤더가 Connected=false")
	}
	// v3 가 새로 둔 컬럼이다. 쓰기 경로가 처음 관측에서 enabled 로 넣는다.
	if rows[0].Status != "enabled" {
		t.Errorf("Status = %q, want enabled", rows[0].Status)
	}
	// v3 vendors 에는 events_total 컬럼이 없어 세어서 만든다.
	if rows[0].EventsTotal != 2 {
		t.Errorf("EventsTotal = %d, want 2", rows[0].EventsTotal)
	}
	if rows[0].RunningSessions != 1 || rows[0].Sessions != 1 {
		t.Errorf("세션 수 = %d/%d, want 1/1", rows[0].RunningSessions, rows[0].Sessions)
	}
	// 3일 전이 마지막이면 "연결 안 됨" 이다.
	if rows[1].Connected {
		t.Errorf("%s 가 Connected=true — last_seen 이 %v 전이다", rows[1].Vendor, VendorActiveWindow)
	}
	if rows[1].RunningSessions != 0 {
		t.Errorf("codex RunningSessions = %d, want 0", rows[1].RunningSessions)
	}
	if rows[1].EventsTotal != 1 {
		t.Errorf("codex EventsTotal = %d, want 1", rows[1].EventsTotal)
	}
}

// Insights MCP 카드 — v3 의 유일한 관측은 tool_calls.mcp_server 다.
//
// v1 의 mcp_session_usage 가 사라지면서 connect_failures·tokens·connected 도 함께 사라졌다.
// 남은 질문은 "최근 N개 세션에서 이 서버를 실제로 썼는가" 하나다.
func TestMCPUsage(t *testing.T) {
	f := newFixture(t)

	var recs []store.EventRecord
	seq := 0
	next := func() int { seq++; return seq }

	// 최근 3개 세션: filesystem 은 매번 쓰고, github 은 한 번도 안 쓴다.
	for i := range 3 {
		key := fmt.Sprintf("s-recent-%d", i)
		at := testNow.Add(-time.Duration(i+1) * time.Hour)
		for j := range 4 {
			recs = append(recs, toolRecord(key, key+"-turn", fmt.Sprintf("call-fs-%d-%d", i, j),
				at.Add(time.Duration(j)*time.Second), next(), toolSpec{
					ToolName: "read_file", MCPServer: "filesystem", Success: event.Some(true),
				}))
		}
		// postgres 는 붙긴 했지만 호출이 매번 실패한다.
		recs = append(recs, toolRecord(key, key+"-turn", fmt.Sprintf("call-pg-%d", i),
			at.Add(10*time.Second), next(), toolSpec{
				ToolName: "query", MCPServer: "postgres", Success: event.Some(false), ErrorType: "timeout",
			}))
	}
	// 창 밖의 오래된 세션. github 을 실제로 썼지만 최근 3개에는 안 들어간다.
	recs = append(recs, toolRecord("s-ancient", "t-ancient", "call-gh-old",
		testNow.Add(-200*time.Hour), next(), toolSpec{
			ToolName: "list_issues", MCPServer: "github", Success: event.Some(true),
		}))
	f.write(store.Batch{Events: recs})

	rows, err := f.reader.MCPUsage(context.Background(), 3)
	if err != nil {
		t.Fatalf("MCPUsage: %v", err)
	}
	byName := map[string]MCPRow{}
	for _, r := range rows {
		byName[r.ServerName] = r
	}
	if len(byName) != 2 {
		t.Fatalf("서버 = %d, want 2 (%+v)", len(byName), rows)
	}

	fsRow := byName["filesystem"]
	if fsRow.ToolCalls != 12 || fsRow.Sessions != 3 {
		t.Errorf("filesystem = %+v, want tool_calls=12 sessions=3", fsRow)
	}
	if fsRow.ScopeSessions != 3 {
		t.Errorf("ScopeSessions = %d, want 3 — '최근 N개 세션에서' 문장의 N 이다", fsRow.ScopeSessions)
	}
	if fsRow.Errors != 0 {
		t.Errorf("filesystem Errors = %d, want 0", fsRow.Errors)
	}

	pg := byName["postgres"]
	if pg.ToolCalls != 3 || pg.Errors != 3 {
		t.Errorf("postgres = %+v, want tool_calls=3 errors=3", pg)
	}

	// 창 밖의 github 은 아예 나오지 않는다 — "최근 14개 세션에서 안 썼다" 를 화면이
	// 그리려면 아는 서버 목록과 대조해야 하고 그것은 GUI 몫이다.
	if _, ok := byName["github"]; ok {
		t.Errorf("창 밖의 오래된 세션이 새어 들어왔다: %+v", rows)
	}
	// 툴 호출 내림차순이라 실제로 많이 쓰인 서버가 먼저다.
	if rows[0].ServerName != "filesystem" {
		t.Errorf("1행 = %q, want filesystem (툴 호출 내림차순)", rows[0].ServerName)
	}
}

// 0 이하는 계획서 예시의 14 를 쓴다.
func TestMCPUsageDefaultWindow(t *testing.T) {
	f := newFixture(t)
	var recs []store.EventRecord
	for i := range 20 {
		key := fmt.Sprintf("s-%02d", i)
		recs = append(recs, toolRecord(key, key+"-turn", fmt.Sprintf("call-%02d", i),
			testNow.Add(-time.Duration(i+1)*time.Hour), i+1, toolSpec{
				ToolName: "list_issues", MCPServer: "github", Success: event.Some(true),
			}))
	}
	f.write(store.Batch{Events: recs})

	rows, err := f.reader.MCPUsage(context.Background(), 0)
	if err != nil {
		t.Fatalf("MCPUsage: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("행 = %d, want 1", len(rows))
	}
	if rows[0].ScopeSessions != defaultMCPSessions {
		t.Errorf("ScopeSessions = %d, want %d", rows[0].ScopeSessions, defaultMCPSessions)
	}
	if rows[0].Sessions != defaultMCPSessions {
		t.Errorf("Sessions = %d, want %d — 창 밖 세션이 새어 들어왔다", rows[0].Sessions, defaultMCPSessions)
	}
}
