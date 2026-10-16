package daemon

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/otlpdecode"
)

func TestPipelinePersistsEachPayloadWithItsEvent(t *testing.T) {
	db := openTestStore(t)
	t.Cleanup(func() { _ = db.Close() })
	p := newTestPipeline(t, db, &syncBuffer{}, func() time.Time { return time.Unix(fixtureUnix, 0) })
	b := walkthroughBatch(t)
	for range 2 {
		p.cmds <- command{kind: cmdBatch, batch: b}
	}
	if !p.close(time.Now().Add(5 * time.Second)) {
		t.Fatal("pipeline did not finish")
	}
	rows, err := db.SQL().Query(`SELECT event_name,json(payload) FROM events ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var name, raw string
		if err := rows.Scan(&name, &raw); err != nil {
			t.Fatal(err)
		}
		if !json.Valid([]byte(raw)) {
			t.Fatal("invalid JSONB conversion")
		}
		res, err := otlpdecode.Decode(b.Kind, []byte(raw), otlpdecode.EncodingJSON, otlpdecode.Options{InstallationID: "inst-unit", SkipPayload: true})
		if err != nil || len(res.Events) != 1 || res.Events[0].Name != name {
			t.Fatalf("wrong payload for %s: %v", name, err)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != len(b.Result.Events) {
		t.Fatalf("stored=%d expected=%d", count, len(b.Result.Events))
	}
}
