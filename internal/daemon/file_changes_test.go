package daemon

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	logscolpb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/your-org/pulsemetry/internal/otlpdecode"
	"github.com/your-org/pulsemetry/internal/receiver"
)

// 임시 DB에서 OTLP→디코더→파이프라인→SQLite 전체 저장 경로를 검증한다.
func TestCodexPatchFilesReachSQLite(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "otlpdecode", "testdata", "logs_codex_file_changes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var msg logscolpb.ExportLogsServiceRequest
	if err := protojson.Unmarshal(raw, &msg); err != nil {
		t.Fatal(err)
	}
	// 원문 제한보다 큰 첫 파일 뒤에도 나머지 파일이 저장되어야 한다.
	for _, record := range msg.ResourceLogs[0].ScopeLogs[0].LogRecords {
		for _, attr := range record.Attributes {
			if attr.Key == "arguments" {
				value := strings.Replace(attr.GetValue().GetStringValue(), "+hello", "+"+strings.Repeat("x", 20000), 1)
				if strings.Contains(value, strings.Repeat("x", 20000)) {
					value = strings.Replace(value, "*** End Patch", "*** Update File: src/name-only.go\n*** Move to: src/renamed-only.go\n@@\n unchanged\n*** End Patch", 1)
				}
				attr.Value = &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value}}
			}
		}
	}
	raw, err = protojson.Marshal(&msg)
	if err != nil {
		t.Fatal(err)
	}
	binary, err := proto.Marshal(&msg)
	if err != nil {
		t.Fatal(err)
	}
	for _, wire := range []struct {
		name     string
		body     []byte
		encoding otlpdecode.Encoding
	}{
		{"json", raw, otlpdecode.EncodingJSON}, {"protobuf", binary, otlpdecode.EncodingProtobuf},
	} {
		t.Run(wire.name, func(t *testing.T) {
			db := openTestStore(t)
			t.Cleanup(func() { _ = db.Close() })
			res, err := otlpdecode.DecodeLogs(wire.body, wire.encoding, otlpdecode.Options{InstallationID: "patch-test"})
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Targets) != 5 {
				t.Fatalf("targets=%+v", res.Targets)
			}
			logs := &syncBuffer{}
			p := newTestPipeline(t, db, logs, func() time.Time { return time.Unix(fixtureUnix, 0) })
			batch := receiver.Batch{Kind: otlpdecode.PayloadLogs, Encoding: wire.encoding, Body: wire.body, Result: res}
			for i := 0; i < 2; i++ {
				if err := p.Consume(context.Background(), batch); err != nil {
					t.Fatal(err)
				}
			}
			p.close(time.Now().Add(5 * time.Second))
			// 재시작 뒤 재전송에도 DB의 이벤트 중복 제거가 적용되어야 한다.
			p2 := newTestPipeline(t, db, logs, func() time.Time { return time.Unix(fixtureUnix, 0) })
			if err := p2.Consume(context.Background(), batch); err != nil {
				t.Fatal(err)
			}
			p2.close(time.Now().Add(5 * time.Second))
			rows, err := db.SQL().Query(`SELECT f.file_path,f.operation,COALESCE(f.renamed_from,''),f.additions,f.deletions,f.old_hash,f.new_hash,c.id,c.success FROM file_changes f JOIN tool_calls c ON c.id=f.tool_call_id ORDER BY f.id`)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var got [][3]string
			var gotCounts [][2]sql.NullInt64
			var owner int64
			for rows.Next() {
				var row [3]string
				var additions, deletions sql.NullInt64
				var oldHash, newHash sql.NullString
				var id int64
				var success bool
				if err := rows.Scan(&row[0], &row[1], &row[2], &additions, &deletions, &oldHash, &newHash, &id, &success); err != nil {
					t.Fatal(err)
				}
				if oldHash.Valid || newHash.Valid {
					t.Fatal("unobserved hashes must be NULL")
				}
				gotCounts = append(gotCounts, [2]sql.NullInt64{additions, deletions})
				if !success || (owner != 0 && owner != id) {
					t.Fatal("wrong owning tool call")
				}
				owner = id
				got = append(got, row)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			want := [][3]string{{"C:/work/project/src/new.go", "create", ""}, {"C:/work/project/src/existing.go", "modify", ""}, {"C:/work/project/src/old.go", "delete", ""}, {"C:/work/project/src/after.go", "rename", "C:/work/project/src/before.go"}, {"C:/work/project/src/renamed-only.go", "rename", "C:/work/project/src/name-only.go"}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("files=%v want %v", got, want)
			}
			wantCounts := [][2]sql.NullInt64{
				{{Int64: 1, Valid: true}, {Int64: 0, Valid: true}},
				{{Int64: 1, Valid: true}, {Int64: 1, Valid: true}},
				{{Int64: 0, Valid: true}, {}},
				{{Int64: 1, Valid: true}, {Int64: 1, Valid: true}},
				{{Int64: 0, Valid: true}, {Int64: 0, Valid: true}},
			}
			if !reflect.DeepEqual(gotCounts, wantCounts) {
				t.Fatalf("line counts=%+v want %+v", gotCounts, wantCounts)
			}
			if !strings.Contains(logs.String(), "파일 변경 추출 생략") || strings.Contains(logs.String(), "*** Begin Patch") || strings.Contains(logs.String(), "C:/work") {
				t.Fatalf("diagnostics missing or contains content: %s", logs.String())
			}
		})
	}
}
