package store

import (
	"context"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/event"
	"github.com/your-org/pulsemetry/internal/rollup"
	"github.com/your-org/pulsemetry/internal/session"
)

const day = 24 * time.Hour

// seedRetention 은 단일 400일 컷오프 양쪽을 걸치는 데이터를 넣는다.
// now 기준 상대 시각으로만 만들어 경계 판정이 벽시계에 의존하지 않는다.
func seedRetention(t *testing.T, db *DB, now time.Time) {
	t.Helper()
	ctx := context.Background()

	ages := []time.Duration{1 * day, 399 * day, 401 * day}
	var (
		recs     []EventRecord
		sessions []session.Session
		rollups  []rollup.Row
	)
	for i, age := range ages {
		at := now.Add(-age)
		ev := newEvent("claude_code.user_prompt", at, i)
		ev.SessionID = sessionIDFor(i)
		recs = append(recs, EventRecord{
			Event:    ev,
			Contents: []event.Content{{Kind: event.ContentPrompt, Body: "토큰 검증 로직을 고쳐줘"}},
		})

		s := newSession(sessionIDFor(i), at)
		sessions = append(sessions, s)

		r := rollup.Row{Hour: event.HourOf(event.NanoFromTime(at)), Dim: rollup.DimTotal}
		r.CostUSD = 1
		rollups = append(rollups, r)
	}

	// vendors 는 이벤트에서 파생되므로 가장 오래된 이벤트가 first_seen, 최신이 last_seen 이다.
	if _, err := db.Write(ctx, Batch{Events: recs, Sessions: sessions, Rollups: rollups}); err != nil {
		t.Fatalf("시드 Write: %v", err)
	}
	for i, age := range ages {
		at := now.Add(-age).Unix()
		id := sessionIDFor(i)
		if _, err := db.SQL().ExecContext(ctx, `
			INSERT INTO turns (session_id, turn_index, started_at, last_event_at)
			VALUES (?, 1, ?, ?)`, id, at, at); err != nil {
			t.Fatalf("turns 시드: %v", err)
		}
		if _, err := db.SQL().ExecContext(ctx, `
			INSERT INTO session_phases
			  (session_id, phase_index, phase_type, start_turn_index, end_turn_index, started_at, last_event_at, turn_count)
			VALUES (?, 1, 'implementation', 1, 1, ?, ?, 1)`, id, at, at); err != nil {
			t.Fatalf("session_phases 시드: %v", err)
		}
	}
}

func sessionIDFor(i int) string { return []string{"fresh", "mid", "ancient"}[i] }

// 모든 로컬 데이터가 같은 400일 보존 기간을 따르는지 검사한다.
func TestPruneUsesUnifiedRetention(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := baseTime
	seedRetention(t, db, now)

	res, err := db.Prune(ctx, now)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}

	// 401일 된 이벤트와 원문만 사라진다.
	if res.Events != 1 || res.EventContent != 1 {
		t.Errorf("이벤트 삭제 = %d/%d, want 1/1", res.Events, res.EventContent)
	}
	if n := countRows(t, db, "events"); n != 2 {
		t.Errorf("events = %d행, want 2", n)
	}

	// 401일 된 세션과 종속 데이터도 함께 사라진다.
	if res.Sessions != 1 {
		t.Errorf("sessions 삭제 = %d, want 1", res.Sessions)
	}

	// 399일 된 데이터는 이벤트·원문·타임라인·세션 모두 온전하다.
	if n := countWhere(t, db, "sessions", "session_id = 'mid'"); n != 1 {
		t.Fatalf("399일 된 세션이 사라졌다")
	}
	if n := countWhere(t, db, "session_files", "session_id = 'mid'"); n != 1 {
		t.Errorf("399일 된 세션의 파일 목록이 사라졌다")
	}
	if n := countWhere(t, db, "tool_events", "session_id = 'mid'"); n != 2 {
		t.Errorf("399일 된 세션의 툴 타임라인 = %d행, want 2", n)
	}
	if n := countWhere(t, db, "events", "session_id = 'mid'"); n != 1 {
		t.Errorf("399일 된 세션의 이벤트 = %d행, want 1", n)
	}
	if n := countWhere(t, db, "event_content", "event_id IN (SELECT id FROM events WHERE session_id = 'mid')"); n != 1 {
		t.Errorf("399일 된 세션의 원문 = %d행, want 1", n)
	}
	// 수치도 남아 있어야 Today·Activity 의 세션 카드가 계속 보인다.
	var toolCalls int64
	if err := db.SQL().QueryRowContext(ctx,
		`SELECT tool_calls FROM sessions WHERE session_id = 'mid'`).Scan(&toolCalls); err != nil {
		t.Fatalf("sessions 조회: %v", err)
	}
	if toolCalls != 2 {
		t.Errorf("31일 된 세션의 tool_calls = %d, want 2", toolCalls)
	}

	// 1일 된 것은 전부 온전하다.
	for _, table := range []string{"session_files", "tool_events"} {
		if n := countWhere(t, db, table, "session_id = 'fresh'"); n == 0 {
			t.Errorf("1일 된 세션의 %s 가 사라졌다", table)
		}
	}
}

// 세션 계층 삭제는 CASCADE 로 종속 테이블을 정리한다.
func TestPruneSessionCascade(t *testing.T) {
	db := openTestDB(t)
	now := baseTime
	seedRetention(t, db, now)

	res, err := db.Prune(context.Background(), now)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if res.Turns != 1 || res.SessionPhases != 1 || res.SessionFiles != 1 || res.MCPUsage != 1 {
		t.Errorf("CASCADE 계수 = %d/%d/%d/%d, want 1/1/1/1",
			res.Turns, res.SessionPhases, res.SessionFiles, res.MCPUsage)
	}
	for _, table := range []string{"turns", "session_phases", "session_files", "mcp_session_usage", "tool_events"} {
		if n := countWhere(t, db, table, "session_id = 'ancient'"); n != 0 {
			t.Errorf("401일 된 세션의 %s 가 %d행 남았다 — CASCADE 가 동작하지 않았다", table, n)
		}
	}
	// 고아 확인
	if n := countWhere(t, db, "session_files", "session_id NOT IN (SELECT session_id FROM sessions)"); n != 0 {
		t.Errorf("고아 session_files %d행", n)
	}
}

func TestPruneRollupAndVendors(t *testing.T) {
	db := openTestDB(t)
	now := baseTime
	seedRetention(t, db, now)

	res, err := db.Prune(context.Background(), now)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if res.RollupHourly != 1 {
		t.Errorf("rollup_hourly 삭제 = %d, want 1 (401일 된 버킷만)", res.RollupHourly)
	}
	if n := countRows(t, db, "rollup_hourly"); n != 2 {
		t.Errorf("rollup_hourly = %d행, want 2", n)
	}
	// last_seen 이 1일 전이라 400일 컷오프에 걸리지 않는다.
	if res.Vendors != 0 || countRows(t, db, "vendors") != 1 {
		t.Errorf("vendors 가 잘못 지워졌다 (삭제 %d행)", res.Vendors)
	}
}

// 컷오프 경계를 정확히 지키는지 본다 — 경계 직전은 살고 경계 직후는 죽는다.
func TestPruneCutoffBoundary(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := baseTime

	cutoff := now.Add(-DefaultRetentionDays * day)
	tests := []struct {
		name string
		at   time.Time
		kept bool
	}{
		{"컷오프 1초 전", cutoff.Add(-time.Second), false},
		{"컷오프 정각", cutoff, true},
		{"컷오프 1초 후", cutoff.Add(time.Second), true},
	}
	for i, tc := range tests {
		if _, err := db.Write(ctx, Batch{Events: []EventRecord{{Event: newEvent("claude_code.user_prompt", tc.at, i)}}}); err != nil {
			t.Fatalf("%s Write: %v", tc.name, err)
		}
	}

	if _, err := db.Prune(ctx, now); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			n := countWhere(t, db, "events", "ts = ?", int64(event.NanoFromTime(tests[i].at)))
			if want := boolToInt(tc.kept); n != want {
				t.Fatalf("남은 행 = %d, want %d", n, want)
			}
		})
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// prune 실패는 치명적이지 않아야 한다 (계획서 리스크 표). 트랜잭션 하나라 실패해도
// 부분 삭제가 남지 않고 다음 틱 재시도가 안전하다.
func TestPruneIsRepeatable(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seedRetention(t, db, baseTime)

	first, err := db.Prune(ctx, baseTime)
	if err != nil {
		t.Fatalf("첫 Prune: %v", err)
	}
	if first.Total() == 0 {
		t.Fatal("첫 Prune 이 아무것도 지우지 않았다")
	}
	second, err := db.Prune(ctx, baseTime)
	if err != nil {
		t.Fatalf("두번째 Prune: %v", err)
	}
	if second.Total() != 0 {
		t.Fatalf("두번째 Prune 이 %d행을 더 지웠다", second.Total())
	}
}

func TestPurgeContent(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := baseTime

	for i, age := range []time.Duration{1 * day, 10 * day} {
		if _, err := db.Write(ctx, Batch{Events: []EventRecord{{
			Event:    newEvent("claude_code.user_prompt", now.Add(-age), i),
			Contents: []event.Content{{Kind: event.ContentPrompt, Body: "토큰 검증"}},
		}}}); err != nil {
			t.Fatalf("시드 Write: %v", err)
		}
	}

	t.Run("--before 는 그 이전 원문만 지운다", func(t *testing.T) {
		n, err := db.PurgeContent(ctx, now.Add(-5*day))
		if err != nil {
			t.Fatalf("PurgeContent: %v", err)
		}
		if n != 1 {
			t.Fatalf("삭제 = %d행, want 1", n)
		}
		// events 는 남는다 — 수치와 롤업은 그대로이고 검색만 불가능해진다.
		if n := countRows(t, db, "events"); n != 2 {
			t.Errorf("events = %d행, want 2", n)
		}
	})

	t.Run("--before 없으면 전부", func(t *testing.T) {
		n, err := db.PurgeContent(ctx, time.Time{})
		if err != nil {
			t.Fatalf("PurgeContent: %v", err)
		}
		if n != 1 {
			t.Fatalf("삭제 = %d행, want 1", n)
		}
		if n := countRows(t, db, "event_content"); n != 0 {
			t.Errorf("event_content = %d행, want 0", n)
		}
		if n := countRows(t, db, "events"); n != 2 {
			t.Errorf("events = %d행, want 2", n)
		}
	})
}
