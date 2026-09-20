package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/event"
)

func TestPayloadJSONBStorageAndPurge(t *testing.T) {
	db := openTestDB(t)
	rec := evrec("claude_code.user_prompt", baseTime, 0, inTurn("p1"))
	rec.Payload = []byte(`{"authorization":"Bearer received-secret","n":9007199254740993}`)
	mustWrite(t, db, Batch{Events: []EventRecord{rec}})
	var kind, secret, num string
	if err := db.SQL().QueryRow(`SELECT typeof(payload),json_extract(payload,'$.authorization'),CAST(json_extract(payload,'$.n') AS TEXT) FROM events`).Scan(&kind, &secret, &num); err != nil {
		t.Fatal(err)
	}
	if kind != "blob" || secret != "Bearer received-secret" || num != "9007199254740993" {
		t.Fatalf("%s %s %s", kind, secret, num)
	}
	rec.Payload = []byte(`{"changed":true}`)
	res := mustWrite(t, db, Batch{Events: []EventRecord{rec}})
	if res.EventsDuplicate != 1 || countRows(t, db, "events") != 1 {
		t.Fatal("payload changed deduplication")
	}
	if got := scanOne(t, db, `SELECT json_extract(payload,'$.authorization') FROM events`); got != "Bearer received-secret" {
		t.Fatal("duplicate replaced original")
	}
	purged, err := db.PurgeContent(context.Background(), time.Time{})
	if err != nil || purged.Payloads != 1 {
		t.Fatalf("purge: %+v %v", purged, err)
	}
	if got := scanOne(t, db, `SELECT payload FROM events`); got != nil {
		t.Fatal("payload survived purge")
	}
}

func TestPayloadOmittedWithoutLosingEvent(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled bool
		raw     string
	}{
		{"off", false, `{"secret":"value"}`},
		{"missing", true, ""},
		{"invalid", true, "{"},
		{"large", true, `{"x":"` + strings.Repeat("x", event.MaxPayloadBytes) + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestDB(t, WithContentStorage(tc.enabled))
			rec := evrec("claude_code.user_prompt", baseTime, 0, inTurn("p1"))
			rec.Payload = []byte(tc.raw)
			mustWrite(t, db, Batch{Events: []EventRecord{rec}})
			if countRows(t, db, "events") != 1 || scanOne(t, db, `SELECT payload FROM events`) != nil {
				t.Fatal("event missing or payload retained")
			}
		})
	}
}
