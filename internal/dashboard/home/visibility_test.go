package home

import (
	"context"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/dashboard"
	"github.com/your-org/pulsemetry/internal/store"
)

// Home의 세션 수만 표시 자격을 따르고 원시 사용량과 CLI·트레이 조회는 보존한다.
func TestSnapshotVisibleSessionsKeepRawUsageAndReaders(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, store.PathIn(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	at := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC).Unix()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.SQL().ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO vendors(vendor,first_seen,last_seen,status) VALUES
 ('codex',1,2,'enabled'),('claude_code',1,2,'enabled')`)
	exec(`INSERT INTO sessions(id,vendor_id,session_key,started_at,active_time_sec) VALUES
 (1,'codex','internal',?,30),(2,'codex','client',?,60),(3,'claude_code','claude',?,90)`, at+1, at+2, at+3)
	exec(`INSERT INTO turns(id,session_id,turn_key,turn_index,started_at,prompt_text) VALUES
 (1,1,'internal',1,?,'internal body'),(2,2,'client',1,?,NULL)`, at+1, at+2)
	exec(`INSERT INTO codex_turn_provenance(turn_id,label,processing_state,link_state) VALUES
 (1,'internal_task','finalized','unique'),(2,'client_submitted','finalized','ambiguous')`)
	exec(`INSERT INTO events(id,turn_id,seq,event_name,occurred_at,record_hash) VALUES
 (1,1,1,'usage',?,'usage-hidden'),(2,1,2,'tool',?,'tool-hidden')`, at+1, at+1)
	exec(`INSERT INTO llm_calls(turn_id,source_event_id,called_at,model,input_tokens,output_tokens,cost_usd)
 VALUES (1,1,?,'gpt-test',100,20,0.5)`, at+1)
	exec(`INSERT INTO tool_calls(turn_id,call_key,result_event_id,tool_name,called_at)
 VALUES (1,'hidden-tool',2,'Read',?)`, at+1)

	svc := dashboard.NewService(db.Path())
	t.Cleanup(func() { _ = svc.Stop() })
	read := func() Snapshot {
		t.Helper()
		out, err := NewBuilder(svc).Snapshot(ctx, Query{TZ: "UTC", Start: "2026-09-20", End: "2026-09-20"})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	out := read()
	if len(out.Recent) != 2 || out.RecentTruncated || out.Recent[0].SessionKey != "claude" || out.Recent[1].SessionKey != "client" {
		t.Fatalf("Home 목록 = %+v", out.Recent)
	}
	if out.Recent[1].Cost.Calls != 0 || out.Usage.Totals.SessionsStarted != 2 || out.RunningSessions != 2 || len(out.ActiveAgents) != 2 {
		t.Fatalf("표시 자격과 0사용량 세션 = %+v", out)
	}
	if out.Usage.Totals.InputTokens != 100 || out.Usage.Totals.OutputTokens != 20 ||
		out.Usage.Totals.ToolCalls != 1 || out.Usage.Totals.ActiveSeconds != 180 ||
		out.Usage.Cost.Total.USD != 0.5 || out.CodexPromptUsage.TotalTokens != 120 ||
		out.CodexPromptUsage.UserTokens != 0 {
		t.Fatalf("숨긴 세션의 원시 사용량 = %+v / %+v", out.Usage, out.CodexPromptUsage)
	}
	if out.Usage.Windows[6].SessionsStarted != 2 || out.Usage.Vendors[0].Vendor != "codex" && out.Usage.Vendors[1].Vendor != "codex" {
		t.Fatalf("창·벤더 세션 수 = %+v", out.Usage)
	}
	for _, v := range out.Usage.Vendors {
		if v.SessionsStarted != 1 {
			t.Errorf("%s 세션 수 = %d, want 1", v.Vendor, v.SessionsStarted)
		}
	}

	// 원시 조회와 직접 ID 상세는 목록 자격과 무관하다.
	if raw, err := svc.Reader().Sessions(ctx, dashboard.SessionQuery{}); err != nil || len(raw) != 3 {
		t.Fatalf("CLI 세션 목록 = %+v, %v", raw, err)
	}
	if raw, err := svc.RecentActivity(ctx, dashboard.RecentQuery{TZ: "UTC", AnyDate: true}); err != nil || len(raw.Sessions) != 3 || raw.ActiveSessions != 3 {
		t.Fatalf("트레이 원시 조회 = %+v, %v", raw, err)
	}
	if detail, err := svc.Reader().Session(ctx, 1); err != nil || !detail.Found || detail.Session.InputTokens != 100 {
		t.Fatalf("숨긴 ID 직접 상세 = %+v, %v", detail, err)
	}

	// 새 OTel 근거가 마지막 자격을 재검토하면 숨기고, 보강 commit 뒤 다시 보여준다.
	exec(`UPDATE codex_turn_provenance SET processing_state='retrying' WHERE turn_id=2`)
	out = read()
	if len(out.Recent) != 1 || out.Usage.Totals.SessionsStarted != 1 || out.RunningSessions != 1 || out.Usage.Totals.ActiveSeconds != 180 {
		t.Fatalf("자격 상실 뒤 Home = %+v", out)
	}
	exec(`UPDATE codex_turn_provenance SET processing_state='finalized' WHERE turn_id=2`)
	out = read()
	if len(out.Recent) != 2 || out.Usage.Totals.SessionsStarted != 2 {
		t.Fatalf("보강 완료 뒤 Home = %+v", out)
	}
}

// 한 응답의 목록·세션 수·활성 벤더는 보강 commit 전 한 DB 시점에 머문다.
func TestVisibleSessionSnapshotStaysCoherentAcrossProvenanceCommit(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, store.PathIn(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	start := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 1)
	for _, query := range []string{
		`INSERT INTO vendors(vendor,first_seen,last_seen,status) VALUES ('codex',1,2,'enabled')`,
		`INSERT INTO sessions(id,vendor_id,session_key,started_at,active_time_sec)
 VALUES (1,'codex','solo',1789866000,10)`,
		`INSERT INTO turns(id,session_id,turn_key,turn_index,started_at)
 VALUES (1,1,'client',1,1789866000)`,
		`INSERT INTO codex_turn_provenance(turn_id,label,processing_state,link_state)
 VALUES (1,'client_submitted','finalized','unique')`,
		`INSERT INTO events(id,turn_id,seq,event_name,occurred_at,record_hash)
 VALUES (1,1,1,'usage',1789866000,'solo-usage')`,
		`INSERT INTO llm_calls(turn_id,source_event_id,called_at,input_tokens,output_tokens,cost_usd)
 VALUES (1,1,1789866000,12,3,0.25)`,
	} {
		if _, err := db.SQL().ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	svc := dashboard.NewService(db.Path())
	t.Cleanup(func() { _ = svc.Stop() })
	if err := svc.ReadSnapshot(ctx, func(read dashboard.SQLQuerier) error {
		recent, _, err := dashboard.ReadDashboardRecentSessions(ctx, read, start, end, 7)
		if err != nil || len(recent) != 1 {
			t.Fatalf("첫 SELECT = %+v, %v", recent, err)
		}
		// 다른 쓰기 연결에서 분류가 바뀌어도 이 응답의 후속 SELECT는 이전 판을 본다.
		if _, err := db.SQL().ExecContext(ctx, `UPDATE codex_turn_provenance SET processing_state='retrying' WHERE turn_id=1`); err != nil {
			return err
		}
		usage, err := dashboard.ReadDashboardUsageBreakdown(ctx, read, []time.Time{start, end})
		if err != nil {
			return err
		}
		vendors, count, err := dashboard.ReadDashboardActiveAgents(ctx, read)
		if err != nil {
			return err
		}
		recent, _, err = dashboard.ReadDashboardRecentSessions(ctx, read, start, end, 7)
		if err != nil {
			return err
		}
		if usage.Totals.SessionsStarted != 1 || len(vendors) != 1 || count != 1 || len(recent) != 1 {
			t.Fatalf("한 스냅샷의 분류가 갈림: sessions=%d vendors=%v/%d recent=%v", usage.Totals.SessionsStarted, vendors, count, recent)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	out, err := NewBuilder(svc).Snapshot(ctx, Query{TZ: "UTC", Start: "2026-09-20", End: "2026-09-20"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Recent) != 0 || out.RecentTruncated || out.Usage.Totals.SessionsStarted != 0 || out.RunningSessions != 0 || len(out.ActiveAgents) != 0 {
		t.Fatalf("자격 0개 뒤 Home 표시 = %+v", out)
	}
	if out.Usage.Totals.InputTokens != 12 || out.Usage.Totals.OutputTokens != 3 || out.Usage.Totals.APIRequests != 1 || out.Usage.Totals.ActiveSeconds != 10 || out.Usage.Cost.Total.USD != 0.25 {
		t.Fatalf("자격 0개 뒤 원시 사용량 = %+v", out.Usage)
	}
}
