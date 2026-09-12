package otlpdecode

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/your-org/pulsemetry/internal/event"
)

func TestPayloadPreservesEachReceivedRecord(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind PayloadKind
	}{
		{"logs_session_walkthrough.json", PayloadLogs},
		{"logs_broken_records.json", PayloadLogs},
		{"metrics_token_usage.json", PayloadMetrics},
		{"metrics_broken_points.json", PayloadMetrics},
	} {
		for _, enc := range []Encoding{EncodingJSON, EncodingProtobuf} {
			t.Run(tc.name+enc.String(), func(t *testing.T) {
				res, err := Decode(tc.kind, payloadIn(t, tc.kind, tc.name, enc), enc, testOptions())
				if err != nil {
					t.Fatal(err)
				}
				if len(res.Payloads) != len(res.Events) {
					t.Fatalf("payloads=%d events=%d", len(res.Payloads), len(res.Events))
				}
				for i, p := range res.Payloads {
					if p.EventIndex != i || !json.Valid(p.JSON) {
						t.Fatalf("invalid payload index=%d", p.EventIndex)
					}
					one, err := Decode(tc.kind, p.JSON, EncodingJSON, testOptions())
					if err != nil || len(one.Events) != 1 {
						t.Fatalf("single event decode: %v count=%d", err, len(one.Events))
					}
					want, got := res.Events[i], one.Events[0]
					if want.Name != got.Name || want.TS != got.TS || want.Attr != got.Attr || want.Measure != got.Measure {
						t.Fatalf("wrong event at %d", i)
					}
				}
			})
		}
	}
}

func TestPayloadKeepsUnknownFieldsAndSensitiveValues(t *testing.T) {
	raw := string(loadFixture(t, "logs_session_walkthrough.json"))
	raw = strings.Replace(raw, `"resourceLogs":`, `"futureField":{"authorization":"Bearer received-secret"},"resourceLogs":`, 1)
	res, err := Decode(PayloadLogs, []byte(raw), EncodingJSON, testOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Payloads) == 0 {
		t.Fatal("missing payload")
	}
	for _, p := range res.Payloads {
		if !strings.Contains(string(p.JSON), "Bearer received-secret") {
			t.Fatal("received field removed")
		}
	}
	opt := testOptions()
	opt.SkipPayload = true
	off, err := Decode(PayloadLogs, []byte(raw), EncodingJSON, opt)
	if err != nil || len(off.Payloads) != 0 || len(off.Events) != len(res.Events) {
		t.Fatal("OFF changed events or retained payload")
	}
}

func TestPayloadLimitsDropOnlyPayload(t *testing.T) {
	raw := string(loadFixture(t, "logs_session_walkthrough.json"))
	raw = strings.Replace(raw, `"resourceLogs":`, `"large":"`+strings.Repeat("x", event.MaxPayloadBytes)+`","resourceLogs":`, 1)
	res, err := Decode(PayloadLogs, []byte(raw), EncodingJSON, testOptions())
	if err != nil || len(res.Events) == 0 || len(res.Payloads) != 0 || res.PayloadsDropped != len(res.Events) {
		t.Fatalf("limit: %v events=%d dropped=%d", err, len(res.Events), res.PayloadsDropped)
	}

	var doc map[string]json.RawMessage
	if err := json.Unmarshal(loadFixture(t, "logs_session_walkthrough.json"), &doc); err != nil {
		t.Fatal(err)
	}
	var resources []json.RawMessage
	if err := json.Unmarshal(doc["resourceLogs"], &resources); err != nil {
		t.Fatal(err)
	}
	repeated := make([]json.RawMessage, 20)
	for i := range repeated {
		repeated[i] = resources[0]
	}
	doc["resourceLogs"], _ = json.Marshal(repeated)
	doc["batchContext"], _ = json.Marshal(strings.Repeat("x", 40<<10))
	batch, _ := json.Marshal(doc)
	bounded, err := Decode(PayloadLogs, batch, EncodingJSON, testOptions())
	if err != nil || len(bounded.Payloads) == 0 || bounded.PayloadsDropped == 0 || len(bounded.Events) != len(bounded.Payloads)+bounded.PayloadsDropped {
		t.Fatal("batch limit dropped events or was not enforced")
	}
	total := 0
	for _, p := range bounded.Payloads {
		total += len(p.JSON)
	}
	if total > maxBatchPayloadBytes {
		t.Fatal("batch payload budget exceeded")
	}
}
