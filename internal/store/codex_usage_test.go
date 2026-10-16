package store

import (
	"context"
	"database/sql"
	"testing"

	"github.com/your-org/pulsemetry/internal/otlpdecode"
	logscolpb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Codex의 실제 속성 이름과 문자열·정수 표현을 디코드해 SQLite까지 확인한다.
func TestCodexUsageDecodeAndStore(t *testing.T) {
	for _, tc := range []struct {
		name  string
		attrs string
		want  [5]sql.NullInt64
	}{
		{
			name: "usage",
			attrs: `,{"key":"input_token_count","value":{"stringValue":"106081"}},
			{"key":"output_token_count","value":{"stringValue":"2496"}},
			{"key":"cached_token_count","value":{"intValue":"11008"}},
			{"key":"cache_write_token_count","value":{"intValue":"40"}},
			{"key":"reasoning_token_count","value":{"intValue":"1200"}}`,
			want: [5]sql.NullInt64{{Int64: 106081, Valid: true}, {Int64: 2496, Valid: true},
				{Int64: 11008, Valid: true}, {Int64: 40, Valid: true}, {Int64: 1200, Valid: true}},
		},
		{
			name: "zero",
			attrs: `,{"key":"input_token_count","value":{"stringValue":"0"}},
			{"key":"output_token_count","value":{"stringValue":"0"}},
			{"key":"cached_token_count","value":{"intValue":"0"}},
			{"key":"cache_write_token_count","value":{"intValue":"0"}},
			{"key":"reasoning_token_count","value":{"intValue":"0"}}`,
			want: [5]sql.NullInt64{{Valid: true}, {Valid: true}, {Valid: true}, {Valid: true}, {Valid: true}},
		},
		{name: "missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := []byte(`{"resourceLogs":[{"scopeLogs":[{"logRecords":[{
				"timeUnixNano":"1789139543000000000","attributes":[
				{"key":"event.name","value":{"stringValue":"codex.sse_event"}},
				{"key":"event.kind","value":{"stringValue":"response.completed"}},
				{"key":"conversation.id","value":{"stringValue":"codex-usage-session"}}
				` + tc.attrs + `]}]}]}]}`)
			var msg logscolpb.ExportLogsServiceRequest
			if err := protojson.Unmarshal(payload, &msg); err != nil {
				t.Fatal(err)
			}
			binary, err := proto.Marshal(&msg)
			if err != nil {
				t.Fatal(err)
			}
			for _, wire := range []struct {
				name string
				data []byte
				enc  otlpdecode.Encoding
			}{{"json", payload, otlpdecode.EncodingJSON}, {"protobuf", binary, otlpdecode.EncodingProtobuf}} {
				t.Run(wire.name, func(t *testing.T) {
					res, err := otlpdecode.DecodeLogs(wire.data, wire.enc, otlpdecode.Options{InstallationID: "test-installation"})
					if err != nil {
						t.Fatal(err)
					}
					if len(res.Events) != 1 || res.Rejected.Total() != 0 {
						t.Fatalf("디코드 결과: %+v", res)
					}
					db := openTestDB(t)
					mustWrite(t, db, Batch{Events: []EventRecord{{Event: res.Events[0], TurnKey: "p1"}}})
					var got [5]sql.NullInt64
					err = db.SQL().QueryRowContext(context.Background(), `SELECT input_tokens, output_tokens,
						cache_read_tokens, cache_write_tokens, reasoning_tokens FROM llm_calls`).
						Scan(&got[0], &got[1], &got[2], &got[3], &got[4])
					if err != nil {
						t.Fatal(err)
					}
					if got != tc.want {
						t.Fatalf("저장된 토큰 = %+v, want %+v", got, tc.want)
					}
				})
			}
		})
	}
}
