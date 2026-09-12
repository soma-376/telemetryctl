package store

import (
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/event"
)

func ttft(ms int64) func(*EventRecord) {
	return func(r *EventRecord) { r.Event.Measure.TTFTMS = event.Some(ms) }
}

func requestID(id string) func(*EventRecord) {
	return func(r *EventRecord) { r.Event.Attr.RequestID = id }
}

func TestLLMCallStoresRequestID(t *testing.T) {
	db := openTestDB(t)

	mustWrite(t, db, Batch{Events: []EventRecord{
		evrec("claude_code.api_request", baseTime, 0,
			inTurn("p1"), tokens(100, 20), requestID("req_011CVkbQ")),
	}})

	var got string
	row := db.SQL().QueryRow(`SELECT COALESCE(request_id,'') FROM llm_calls`)
	if err := row.Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "req_011CVkbQ" {
		t.Fatalf("request_id = %q", got)
	}
}

// ttft 는 턴을 만든 이벤트가 아니라 뒤따르는 스트림 이벤트에 실려 온다. 턴 UPSERT 는
// 새 턴일 때만 돌므로, 이미 있는 턴에도 반영돼야 한다.
func TestTurnTTFTArrivesAfterTurnExists(t *testing.T) {
	db := openTestDB(t)

	mustWrite(t, db, Batch{Events: []EventRecord{
		evrec("codex.user_prompt", baseTime, 0, withVendor("codex"), inTurn("p1")),
	}})
	mustWrite(t, db, Batch{Events: []EventRecord{
		evrec("codex.sse_event", baseTime.Add(time.Second), 0,
			withVendor("codex"), inTurn("p1"), ttft(1420)),
	}})

	var got int64
	if err := db.SQL().QueryRow(`SELECT ttft_ms FROM turns`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != 1420 {
		t.Fatalf("ttft_ms = %d", got)
	}
}

// 한 턴에 응답이 여러 번이면 첫 응답까지의 지연이 그 턴의 체감 지연이다.
func TestTurnTTFTKeepsFirstObserved(t *testing.T) {
	db := openTestDB(t)

	mustWrite(t, db, Batch{Events: []EventRecord{
		evrec("codex.sse_event", baseTime, 0, withVendor("codex"), inTurn("p1"), ttft(1420)),
	}})
	mustWrite(t, db, Batch{Events: []EventRecord{
		evrec("codex.sse_event", baseTime.Add(time.Second), 1, withVendor("codex"), inTurn("p1"), ttft(99)),
	}})

	var got int64
	if err := db.SQL().QueryRow(`SELECT ttft_ms FROM turns`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != 1420 {
		t.Fatalf("ttft_ms = %d, 첫 관측값이 유지돼야 한다", got)
	}
}

// 관측되지 않으면 0 이 아니라 NULL 이다.
func TestTurnTTFTUnobservedStaysNull(t *testing.T) {
	db := openTestDB(t)

	mustWrite(t, db, Batch{Events: []EventRecord{
		evrec("codex.user_prompt", baseTime, 0, withVendor("codex"), inTurn("p1")),
	}})

	var got *int64
	if err := db.SQL().QueryRow(`SELECT ttft_ms FROM turns`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("ttft_ms = %d, NULL 이어야 한다", *got)
	}
}
