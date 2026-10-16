package dashboard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/codexsource"
)

func seedCodexFBASE(t *testing.T, f *fixture) (int64, time.Time) {
	t.Helper()
	day := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	at := day.Add(time.Hour).Unix()
	if _, err := f.db.SQL().Exec(`INSERT INTO vendors(vendor,first_seen,last_seen,status) VALUES ('codex',?,?,'enabled')`, at, at); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.SQL().Exec(`INSERT INTO sessions(id,vendor_id,session_key,started_at,ended_at,last_activity_at) VALUES (1,'codex','fbase',?,?,?)`, at, at+10, at+10); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.SQL().Exec(`INSERT INTO sessions(id,vendor_id,session_key,started_at,ended_at,last_activity_at) VALUES (2,'codex','p-owner',?,?,?)`, at, at+10, at+10); err != nil {
		t.Fatal(err)
	}
	labels := []struct {
		label, state, body, message string
		index, session              any
		provenance                  bool
	}{
		{"client_submitted", "finalized", "u1", "u1-message", 1, 1, true},
		{"client_submitted", "finalized", "u2", "u2-message", 2, 1, true},
		{"internal_task", "finalized", "internal", "i-message", 0, 1, true},
		{"unknown", "finalized", "unknown", "x-message", 3, 1, true},
		{"unknown", "pending", "p-body", "p-message", 1, 2, true},
		{"", "", "missing-classification", "m-message", 4, 1, false},
		{"internal_task", "finalized", "virtual", "v-message", nil, 1, true}, // 가상 턴은 언제나 미분류 사용량
	}
	for i, row := range labels {
		id := i + 1
		if _, err := f.db.SQL().Exec(`INSERT INTO turns(id,session_id,turn_key,turn_index,started_at,ended_at,prompt_text,prompt_message_id,prompt_source_turn_id)
VALUES (?,?,?,?,?,?,?,?,?)`, id, row.session, fmt.Sprintf("t-%d", id), row.index, at, at+10, row.body, row.message, fmt.Sprintf("t-%d", id)); err != nil {
			t.Fatal(err)
		}
		if row.provenance {
			if _, err := f.db.SQL().Exec(`INSERT INTO codex_turn_provenance(turn_id,label,processing_state,link_state) VALUES (?,?,?,'unmatched')`, id, row.label, row.state); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := f.db.SQL().Exec(`INSERT INTO codex_pending(turn_id) VALUES (5)`); err != nil {
		t.Fatal(err)
	}
	callRows := []struct{ turn, input, output int }{{1, 50, 10}, {1, 30, 10}, {2, 40, 10}, {3, 160, 40}, {4, 250, 50}, {5, 30, 10}, {6, 20, 10}, {7, 15, 5}}
	sequences := map[int]int{}
	for i, call := range callRows {
		sequences[call.turn]++
		id := i + 1
		if _, err := f.db.SQL().Exec(`INSERT INTO events(id,turn_id,seq,event_name,occurred_at,record_hash)
VALUES (?,?,?,'codex.sse_event',?,?)`, id, call.turn, sequences[call.turn], at, fmt.Sprintf("fbase-%d", id)); err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.SQL().Exec(`INSERT INTO llm_calls(turn_id,source_event_id,called_at,model,input_tokens,output_tokens,cache_read_tokens,reasoning_tokens,cost_usd)
VALUES (?,?,?,'gpt-test',?,?,7,9,0.01)`, call.turn, id, at, call.input, call.output); err != nil {
			t.Fatal(err)
		}
	}
	return 1, day
}

func TestCodexFBASEPromptAndUsageAcrossSurfaces(t *testing.T) {
	f := newFixture(t)
	sessionID, day := seedCodexFBASE(t, f)
	ctx := context.Background()
	start, end := day, day.AddDate(0, 0, 1)
	check := func(prompts, user, system, unclassified, other int64) {
		t.Helper()
		split, err := ReadCodexPromptUsage(ctx, f.db.SQL(), start, end)
		if err != nil {
			t.Fatal(err)
		}
		if split.TotalTokens != 740 || split.UserTokens != user || split.SystemTokens != system || split.UnclassifiedTokens != unclassified || split.OtherTokens != other || split.TotalTokens != split.UserTokens+split.OtherTokens || split.OtherTokens != split.SystemTokens+split.UnclassifiedTokens {
			t.Fatalf("Codex 소계 = %+v", split)
		}
		usage, err := ReadUsageBreakdown(ctx, f.db.SQL(), []time.Time{start, end})
		if err != nil {
			t.Fatal(err)
		}
		if usage.Totals.Prompts != prompts || usage.Totals.InputTokens+usage.Totals.OutputTokens != 740 || usage.Totals.APIRequests != 8 {
			t.Fatalf("기간 집계 = %+v", usage.Totals)
		}
		sessions, err := f.reader.Sessions(ctx, SessionQuery{Vendor: "codex"})
		if err != nil || len(sessions) != 2 {
			t.Fatalf("세션 카드 = %+v, %v", sessions, err)
		}
		var cardPrompts, cardTokens, cardCalls int64
		for _, card := range sessions {
			cardPrompts += card.Prompts
			cardTokens += card.InputTokens + card.OutputTokens
			cardCalls += card.APIRequests
		}
		if cardPrompts != prompts || cardTokens != 740 || cardCalls != 8 {
			t.Fatalf("세션 카드 합계 = prompts %d tokens %d calls %d", cardPrompts, cardTokens, cardCalls)
		}
		metrics, err := f.reader.SessionMetrics(ctx, SessionMetricsQuery{SessionID: sessionID, TurnLimit: 2})
		if err != nil {
			t.Fatal(err)
		}
		if metrics.Totals.PromptTurns != 2 || metrics.Totals.Tokens.Billable() != 700 || metrics.Totals.LLMCalls != 7 || len(metrics.Turns) != 2 || metrics.TurnsTruncated {
			t.Fatalf("상세 전체합/표시 목록 = totals %+v, rows %d, truncated %v", metrics.Totals, len(metrics.Turns), metrics.TurnsTruncated)
		}
		for _, turn := range metrics.Turns {
			if turn.Virtual || turn.TurnIndex == nil {
				t.Fatalf("가상/비사용자 턴 목록 혼입 = %+v", turn)
			}
		}
		limited, err := f.reader.SessionMetrics(ctx, SessionMetricsQuery{SessionID: sessionID, TurnLimit: 1})
		if err != nil || len(limited.Turns) != 1 || limited.Turns[0].TurnIndex == nil || *limited.Turns[0].TurnIndex != 1 || !limited.TurnsTruncated {
			t.Fatalf("사용자 필터 후 limit = %+v, %v", limited, err)
		}
		pending, err := f.reader.SessionMetrics(ctx, SessionMetricsQuery{SessionID: 2, TurnLimit: 1})
		if err != nil || pending.Totals.PromptTurns != prompts-2 || pending.Totals.Tokens.Billable() != 40 || pending.Totals.LLMCalls != 1 || len(pending.Turns) != int(prompts-2) {
			t.Fatalf("지연 분류 세션 전체합/목록 = %+v, %v", pending, err)
		}
		detail, err := f.reader.Session(ctx, sessionID)
		if err != nil || detail.Session.Prompts != 2 || detail.Session.InputTokens+detail.Session.OutputTokens != 700 {
			t.Fatalf("Activity 상세 = %+v, %v", detail, err)
		}
		pDetail, err := f.reader.Session(ctx, 2)
		if err != nil || pDetail.Session.Prompts != prompts-2 || pDetail.Session.InputTokens+pDetail.Session.OutputTokens != 40 {
			t.Fatalf("Activity 지연 세션 = %+v, %v", pDetail, err)
		}
	}
	check(2, 150, 500, 90, 590)
	root := t.TempDir()
	ownerFile := filepath.Join(root, "owner.jsonl")
	row := fmt.Sprintf(`{"type":"event_msg","timestamp":%q,"payload":{"type":"user_message","message":"p-body","message_id":"p-message","turn_id":"t-5"}}`, day.Add(time.Hour+time.Second).Format(time.RFC3339Nano))
	if err := os.WriteFile(ownerFile, []byte(`{"type":"session_meta","payload":{"id":"p-owner","source":"cli"}}`+"\n"+row+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	epoch, err := f.db.BeginCodexWorkerEpoch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := (codexsource.Worker{DB: f.db, Root: root, Epoch: epoch}).RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var label, processing string
	if err := f.db.SQL().QueryRow(`SELECT label,processing_state FROM codex_turn_provenance WHERE turn_id=5`).Scan(&label, &processing); err != nil || label != "client_submitted" || processing != "finalized" {
		t.Fatalf("P 워커 지연 분류 = %s/%s %v", label, processing, err)
	}
	check(3, 190, 500, 50, 550)
}
