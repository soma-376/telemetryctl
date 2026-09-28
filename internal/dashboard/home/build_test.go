package home

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/dashboard"
	"github.com/your-org/pulsemetry/internal/store"
)

func TestQueryWindows(t *testing.T) {
	for _, tc := range []struct {
		start, end, tz, unit string
		windows              int
		hours                float64
	}{
		{"2026-09-20", "2026-09-20", "Asia/Seoul", "hour", 12, 24},
		{"2026-03-08", "2026-03-08", "America/New_York", "hour", 12, 23},
		{"2026-11-01", "2026-11-01", "America/New_York", "hour", 13, 25},
		{"2026-10-31", "2026-11-01", "America/New_York", "hour", 9, 49},
		{"2026-09-01", "2026-09-30", "Asia/Seoul", "day", 30, 720},
		{"2026-07-01", "2026-08-01", "UTC", "week", 5, 768},
		{"2026-02-15", "2026-07-10", "UTC", "month", 6, 3504},
	} {
		t.Run(tc.start+tc.end+tc.tz, func(t *testing.T) {
			bounds, unit, _, err := queryWindows(Query{TZ: tc.tz, Start: tc.start, End: tc.end})
			if err != nil {
				t.Fatal(err)
			}
			if unit != tc.unit || len(bounds)-1 != tc.windows || bounds[len(bounds)-1].Sub(bounds[0]).Hours() != tc.hours {
				t.Fatalf("경계: %v %s", bounds, unit)
			}
			if bounds[0].Format("2006-01-02") != tc.start || bounds[len(bounds)-1].AddDate(0, 0, -1).Format("2006-01-02") != tc.end {
				t.Fatal("양 끝 범위를 넓혔다")
			}
		})
	}
	for _, q := range []Query{
		{TZ: "Mars/Moon", Start: "2026-01-01", End: "2026-01-01"},
		{Start: "2026-02-30", End: "2026-03-01"}, {Start: "2026-09-02", End: "2026-09-01"},
		{Start: "2025-01-01", End: "2026-02-05"}, {},
	} {
		if ValidateQuery(q) == nil {
			t.Fatalf("잘못된 조건 허용: %+v", q)
		}
	}
	if err := ValidateQuery(Query{Start: "2025-01-01", End: "2026-02-04"}); err != nil {
		t.Fatal("400일 거부:", err)
	}
}

func TestSnapshotAbsentDatabase(t *testing.T) {
	svc := dashboard.NewService(filepath.Join(t.TempDir(), "missing.db"))
	t.Cleanup(func() { _ = svc.Stop() })
	out, err := NewBuilder(svc).Snapshot(context.Background(), Query{TZ: "Asia/Seoul", Start: "2026-09-20", End: "2026-09-20"})
	if err != nil || out.DatabaseAvailable || len(out.Usage.Windows) != 12 || out.Recent == nil || out.ActiveAgents == nil || out.Usage.Vendors == nil {
		t.Fatalf("빈 상태: %+v %v", out, err)
	}
}

func TestSnapshotRecentLimitAndPeriodCounts(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, store.PathIn(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	start := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC).Unix()
	if _, err := db.SQL().Exec(`INSERT INTO vendors VALUES ('codex', ?, ?, 'enabled')`, start, start); err != nil {
		t.Fatal(err)
	}
	for i := range 10 {
		// 9건은 기간 안, 마지막 1건은 기간 밖이지만 현재 진행 중인 세션이다.
		at := start + int64(i)
		if i == 9 {
			at = start - 1
		}
		if _, err := db.SQL().Exec(`INSERT INTO sessions(vendor_id,session_key,started_at,active_time_sec) VALUES ('codex', ?, ?, 60)`, i, at); err != nil {
			t.Fatal(err)
		}
	}
	svc := dashboard.NewService(db.Path())
	t.Cleanup(func() { _ = svc.Stop() })
	out, err := NewBuilder(svc).Snapshot(ctx, Query{TZ: "UTC", Start: "2026-09-20", End: "2026-09-20"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Recent) != 7 || !out.RecentTruncated || out.RunningSessions != 9 || out.Usage.Totals.SessionsStarted != 9 || out.Usage.Totals.ActiveSeconds != 540 || len(out.ActiveAgents) != 1 {
		t.Fatalf("기간·목록 집계: %+v", out)
	}
	for i, row := range out.Recent {
		if row.StartedAt != start+8-int64(i) {
			t.Fatalf("최근순 오류: %+v", out.Recent)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := NewBuilder(svc).Snapshot(canceled, Query{Start: "2026-09-20", End: "2026-09-20"}); err == nil {
		t.Fatal("취소된 조회 성공")
	}
}

func TestSnapshotCodexSubtotalKeepsAllVendorTotal(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, store.PathIn(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	at := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC).Unix()
	for _, query := range []string{
		`INSERT INTO vendors(vendor,first_seen,last_seen,status) VALUES ('codex',1,2,'enabled'),('claude_code',1,2,'enabled')`,
		`INSERT INTO sessions(id,vendor_id,session_key,started_at) VALUES (1,'codex','codex',?)`,
		`INSERT INTO sessions(id,vendor_id,session_key,started_at) VALUES (2,'claude_code','claude',?)`,
		`INSERT INTO turns(id,session_id,turn_key,turn_index,started_at) VALUES (1,1,'user',1,?),(2,1,'internal',2,?),(3,2,'claude',1,?)`,
		`INSERT INTO codex_turn_provenance(turn_id,label,processing_state,link_state) VALUES (1,'client_submitted','finalized','unique'),(2,'internal_task','finalized','unique')`,
	} {
		var args []any
		switch {
		case query == `INSERT INTO sessions(id,vendor_id,session_key,started_at) VALUES (1,'codex','codex',?)`, query == `INSERT INTO sessions(id,vendor_id,session_key,started_at) VALUES (2,'claude_code','claude',?)`:
			args = []any{at}
		case query == `INSERT INTO turns(id,session_id,turn_key,turn_index,started_at) VALUES (1,1,'user',1,?),(2,1,'internal',2,?),(3,2,'claude',1,?)`:
			args = []any{at, at, at}
		}
		if _, err := db.SQL().Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	for i, row := range []struct{ turn, tokens int }{{1, 10}, {2, 20}, {3, 7}} {
		id := i + 1
		if _, err := db.SQL().Exec(`INSERT INTO events(id,turn_id,seq,event_name,record_hash) VALUES (?,?,1,'usage',?)`, id, row.turn, id); err != nil {
			t.Fatal(err)
		}
		if _, err := db.SQL().Exec(`INSERT INTO llm_calls(turn_id,source_event_id,called_at,input_tokens,output_tokens,cost_usd) VALUES (?,?,?,?,0,0.01)`, row.turn, id, at, row.tokens); err != nil {
			t.Fatal(err)
		}
	}
	svc := dashboard.NewService(db.Path())
	t.Cleanup(func() { _ = svc.Stop() })
	out, err := NewBuilder(svc).Snapshot(ctx, Query{TZ: "UTC", Start: "2026-09-20", End: "2026-09-20"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Usage.Totals.InputTokens+out.Usage.Totals.OutputTokens != 37 ||
		out.CodexPromptUsage.TotalTokens != 30 || out.CodexPromptUsage.UserTokens != 10 ||
		out.CodexPromptUsage.SystemTokens != 20 || out.CodexPromptUsage.UnclassifiedTokens != 0 || out.CodexPromptUsage.OtherTokens != 20 {
		t.Fatalf("Home 전체 벤더/ Codex 소계 = usage %+v split %+v", out.Usage.Totals, out.CodexPromptUsage)
	}
	// 전체 사용량을 읽은 직후 별도 쓰기 연결이 커밋해도 Codex 소계는 같은 스냅샷에 머문다.
	start := time.Unix(at-1, 0)
	end := time.Unix(at+1, 0)
	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := svc.ReadSnapshot(readCtx, func(reader dashboard.SQLQuerier) error {
		usage, err := dashboard.ReadUsageBreakdown(readCtx, reader, []time.Time{start, end})
		if err != nil {
			return err
		}
		if _, err := db.SQL().ExecContext(readCtx, `INSERT INTO events(id,turn_id,seq,event_name,record_hash) VALUES (4,1,2,'usage','new')`); err != nil {
			return err
		}
		if _, err := db.SQL().ExecContext(readCtx, `INSERT INTO llm_calls(turn_id,source_event_id,called_at,input_tokens,output_tokens,cost_usd) VALUES (1,4,?,5,0,0.01)`, at); err != nil {
			return err
		}
		split, err := dashboard.ReadCodexPromptUsage(readCtx, reader, start, end)
		if err != nil {
			return err
		}
		if usage.Totals.InputTokens+usage.Totals.OutputTokens != 37 || split.TotalTokens != 30 || split.UserTokens != 10 {
			t.Fatalf("경합 중 읽기 스냅샷 불일치: usage=%+v split=%+v", usage.Totals, split)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	updated, err := NewBuilder(svc).Snapshot(ctx, Query{TZ: "UTC", Start: "2026-09-20", End: "2026-09-20"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Usage.Totals.InputTokens+updated.Usage.Totals.OutputTokens != 42 ||
		updated.CodexPromptUsage.TotalTokens != 35 || updated.CodexPromptUsage.UserTokens != 15 ||
		updated.CodexPromptUsage.OtherTokens != 20 {
		t.Fatalf("다음 스냅샷이 새 커밋을 놓침: usage=%+v split=%+v", updated.Usage.Totals, updated.CodexPromptUsage)
	}
}
