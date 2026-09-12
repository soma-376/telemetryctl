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
