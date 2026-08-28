package dashboard

import (
	"context"
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

	f.write(testBatch{
		Events: []store.EventRecord{
			{Event: newEvent("s-recent", recent, 1)},
			{Event: newEvent("s-recent", recent.Add(time.Minute), 2)},
			{Event: func() event.Event {
				e := newEvent("s-old", old, 3)
				e.Vendor = "codex"
				return e
			}()},
		},
		Sessions: []session.Session{
			newSession("s-recent", recent, func(s *session.Session) {
				s.Status = session.StatusRunning
				s.EndedAt = event.Opt[event.UnixSec]{}
			}),
			newSession("s-old", old, func(s *session.Session) { s.Vendor = "codex" }),
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
	if rows[0].Vendor != "claude_code" {
		t.Fatalf("1행 = %q, want claude_code", rows[0].Vendor)
	}
	if !rows[0].Connected {
		t.Error("최근에 본 벤더가 Connected=false")
	}
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
}

// Insights MCP 카드 — connected=1, tool_calls=0 이 "한 번도 안 썼다" 이고
// connect_failures 합계가 "N번 연결 실패" 다.
func TestMCPUsage(t *testing.T) {
	f := newFixture(t)

	var sessions []session.Session
	// 최근 3개 세션: github 은 붙었지만 한 번도 안 쓰고, postgres 는 계속 연결 실패한다.
	for i := 0; i < 3; i++ {
		id := "s-recent-" + string(rune('a'+i))
		sessions = append(sessions, newSession(id, testNow.Add(-time.Duration(i+1)*time.Hour), func(s *session.Session) {
			s.MCP = []session.MCPUsage{
				{ServerName: "github", Connected: true, ToolCalls: 0},
				{ServerName: "postgres", Connected: false, ConnectFailures: 6},
				{ServerName: "filesystem", Connected: true, ToolCalls: 4, Tokens: 100},
			}
		}))
	}
	// 창 밖의 오래된 세션. github 을 실제로 썼지만 최근 3개에는 안 들어간다.
	sessions = append(sessions, newSession("s-ancient", testNow.Add(-200*time.Hour), func(s *session.Session) {
		s.MCP = []session.MCPUsage{{ServerName: "github", Connected: true, ToolCalls: 99}}
	}))
	f.write(testBatch{Sessions: sessions})

	rows, err := f.reader.MCPUsage(context.Background(), 3)
	if err != nil {
		t.Fatalf("MCPUsage: %v", err)
	}
	byName := map[string]MCPRow{}
	for _, r := range rows {
		byName[r.ServerName] = r
	}
	if len(byName) != 3 {
		t.Fatalf("서버 = %d, want 3 (%+v)", len(byName), rows)
	}

	gh := byName["github"]
	if !gh.NeverUsed {
		t.Errorf("github NeverUsed = false, want true (%+v)", gh)
	}
	if gh.UnusedSessions != 3 || gh.ConnectedSessions != 3 {
		t.Errorf("github = %+v, want unused=3 connected=3", gh)
	}
	if gh.ScopeSessions != 3 {
		t.Errorf("ScopeSessions = %d, want 3 — '최근 N개 세션에서' 문장의 N 이다", gh.ScopeSessions)
	}
	if gh.ToolCalls != 0 {
		t.Errorf("github ToolCalls = %d — 창 밖의 오래된 세션이 새어 들어왔다", gh.ToolCalls)
	}

	pg := byName["postgres"]
	if pg.ConnectFailures != 18 {
		t.Errorf("postgres ConnectFailures = %d, want 18 (6×3)", pg.ConnectFailures)
	}
	// 붙은 적이 없으면 "안 썼다" 가 아니라 "못 붙었다" 다. 둘을 합치면 사용자가 지우지 말아야 할
	// 서버를 지운다.
	if pg.NeverUsed {
		t.Error("postgres NeverUsed = true — 연결 실패를 미사용으로 보고했다")
	}

	fsRow := byName["filesystem"]
	if fsRow.NeverUsed || fsRow.ToolCalls != 12 || fsRow.Tokens != 300 {
		t.Errorf("filesystem = %+v, want tool_calls=12 tokens=300 never_used=false", fsRow)
	}
	// 툴 호출 내림차순이라 실제로 쓰인 서버가 먼저다.
	if rows[0].ServerName != "filesystem" {
		t.Errorf("1행 = %q, want filesystem (툴 호출 내림차순)", rows[0].ServerName)
	}
}

// 0 이하는 계획서 예시의 14 를 쓴다.
func TestMCPUsageDefaultWindow(t *testing.T) {
	f := newFixture(t)
	var sessions []session.Session
	for i := 0; i < 20; i++ {
		sessions = append(sessions, newSession("s"+string(rune('a'+i)), testNow.Add(-time.Duration(i+1)*time.Hour),
			func(s *session.Session) {
				s.MCP = []session.MCPUsage{{ServerName: "github", Connected: true}}
			}))
	}
	f.write(testBatch{Sessions: sessions})

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
