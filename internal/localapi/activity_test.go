package localapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/dashboard"
	"github.com/your-org/pulsemetry/internal/dashboard/activity"
	"github.com/your-org/pulsemetry/internal/dashboard/home"
	"github.com/your-org/pulsemetry/internal/event"
	"github.com/your-org/pulsemetry/internal/store"
)

func TestActivityRoutesReadStoredSession(t *testing.T) {
	ctx := context.Background()
	path := store.PathIn(t.TempDir())
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for i, name := range []string{"codex.user_prompt", "codex.sse_event", "codex.tool_result"} {
		e := event.Event{Vendor: "codex", InstallationID: "test", Signal: event.SignalLog, Name: name,
			TS: event.NanoFromTime(time.Unix(1789139543+int64(i), 0)), SessionID: "activity-test", Sequence: i,
			Attr:    event.Attributes{Type: "response.completed", ToolName: "Read", WorkspacePath: "/workspace/demo"},
			Measure: event.Measures{InputTokens: event.Some[int64](100), OutputTokens: event.Some[int64](20)}}
		rec := store.EventRecord{Event: e, TurnKey: "turn-1"}
		if i == 2 {
			rec.CallKey = "call-1"
		}
		if i == 0 {
			rec.Contents = []event.Content{{Kind: event.ContentPrompt, Body: strings.Repeat("가", 4001)}}
		}
		if _, err := db.Write(ctx, store.Batch{Events: []store.EventRecord{rec}}); err != nil {
			t.Fatal(err)
		}
	}
	// Activity 경로는 완료된 사용자 제출 턴만 보여 준다. 이 테스트는 조회
	// 경계이므로 저장된 턴의 분류 결과를 fixture에 명시한다.
	if _, err := db.SQL().ExecContext(ctx, `UPDATE codex_turn_provenance SET label='client_submitted',processing_state='finalized',link_state='unique'`); err != nil {
		t.Fatal(err)
	}
	svc := dashboard.NewService(path)
	t.Cleanup(func() { _ = svc.Stop() })
	h := WithActivity(http.NotFoundHandler(), svc)
	query := func(path string, out any) int {
		t.Helper()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if out != nil && w.Code == 200 {
			if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
				t.Fatal(err)
			}
		}
		return w.Code
	}
	var page activity.Page
	q := url.Values{"q": {`{"vendors":["codex"],"since":1789139543,"until":1789139600,"limit":1}`}}
	if code := query(ActivityPath+"?"+q.Encode(), &page); code != 200 || len(page.Rows) != 1 {
		t.Fatalf("page=%+v status=%d", page, code)
	}
	row := page.Rows[0]
	if row.InputTokens != 100 || row.OutputTokens != 20 || row.ReportedCostCalls != 0 {
		t.Fatalf("row=%+v", row)
	}
	var detail activity.Detail
	if code := query(ActivityPath+"/"+strconv.FormatInt(row.ID, 10), &detail); code != 200 {
		t.Fatal(code)
	}
	if !detail.Detail.Found || detail.Metrics.Totals.LLMCalls != 1 || len(detail.Metrics.Turns) != 1 || len(detail.Detail.Tools) != 1 {
		t.Fatalf("found=%v calls=%d turns=%d tools=%d", detail.Detail.Found, detail.Metrics.Totals.LLMCalls, len(detail.Metrics.Turns), len(detail.Detail.Tools))
	}
	turn := detail.Metrics.Turns[0]
	if !turn.PromptTruncated || len([]rune(turn.PromptText)) != 4000 || detail.Detail.Tools[0].TurnID != turn.TurnID {
		t.Fatalf("턴·도구 연결 또는 프롬프트 상한 오류: %+v", turn)
	}
	q.Set("q", `{"vendors":["claude_code"]}`)
	if code := query(ActivityPath+"?"+q.Encode(), &page); code != 200 || len(page.Rows) != 0 {
		t.Fatalf("필터 오류: %+v", page)
	}
	for _, path := range []string{ActivityPath + "?q=invalid", ActivityPath + "/0", ActivityPath + "/abc"} {
		if code := query(path, nil); code != 400 {
			t.Fatalf("%s=%d", path, code)
		}
	}
	if code := query(ActivityPath+"/9999", &detail); code != 200 || detail.Detail.Found {
		t.Fatalf("없는 세션=%+v status=%d", detail, code)
	}
}

// HTTP 응답에서도 숨긴 세션의 사용량과 ID 직접 상세는 유지한다.
func TestHomeAndActivityRoutesUseVisibleSessionsWithoutHidingDetail(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, store.PathIn(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	at := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC).Unix()
	for _, query := range []string{
		`INSERT INTO vendors(vendor,first_seen,last_seen,status) VALUES ('codex',1,2,'enabled')`,
		`INSERT INTO sessions(id,vendor_id,session_key,started_at,active_time_sec) VALUES
 (1,'codex','hidden',1789905600,30),(2,'codex','shown',1789905601,30)`,
		`INSERT INTO turns(id,session_id,turn_key,turn_index,started_at) VALUES
 (1,1,'internal',1,1789905600),(2,2,'client',1,1789905601)`,
		`INSERT INTO codex_turn_provenance(turn_id,label,processing_state,link_state) VALUES
 (1,'internal_task','finalized','unique'),(2,'client_submitted','finalized','unique')`,
		`INSERT INTO events(id,turn_id,seq,event_name,occurred_at,record_hash) VALUES
 (1,1,1,'usage',1789905600,'hidden-usage')`,
		`INSERT INTO llm_calls(turn_id,source_event_id,called_at,input_tokens,output_tokens,cost_usd)
 VALUES (1,1,1789905600,40,2,0.5)`,
	} {
		if _, err := db.SQL().ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	svc := dashboard.NewService(db.Path())
	t.Cleanup(func() { _ = svc.Stop() })
	h := WithHome(WithActivity(http.NotFoundHandler(), svc), svc)
	get := func(path string, out any) {
		t.Helper()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", path, w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			t.Fatal(err)
		}
	}
	homeQuery := url.Values{"q": {`{"tz":"UTC","start":"2026-09-20","end":"2026-09-20"}`}}
	var summary home.Snapshot
	get(HomePath+"?"+homeQuery.Encode(), &summary)
	if len(summary.Recent) != 1 || summary.Recent[0].SessionKey != "shown" ||
		summary.Usage.Totals.SessionsStarted != 1 || summary.RunningSessions != 1 ||
		summary.Usage.Totals.InputTokens != 40 || summary.Usage.Totals.OutputTokens != 2 ||
		summary.Usage.Totals.ActiveSeconds != 60 {
		t.Fatalf("Home HTTP 응답 = %+v", summary)
	}
	activityQuery := url.Values{"q": {`{"vendors":["codex"],"limit":1}`}}
	var page activity.Page
	get(ActivityPath+"?"+activityQuery.Encode(), &page)
	if len(page.Rows) != 1 || page.Rows[0].SessionKey != "shown" || page.HasMore {
		t.Fatalf("Activity HTTP 목록 = %+v", page)
	}
	var detail activity.Detail
	get(ActivityPath+"/1", &detail)
	if !detail.Detail.Found || detail.Detail.Session.ID != 1 || detail.Metrics.Totals.Tokens.Input != 40 {
		t.Fatalf("숨긴 ID HTTP 상세 = %+v", detail)
	}
	var stored int
	if err := db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE started_at>=? AND started_at<?`, at, at+2).Scan(&stored); err != nil || stored != 2 {
		t.Fatalf("DB 원시 세션 = %d, %v", stored, err)
	}
}
