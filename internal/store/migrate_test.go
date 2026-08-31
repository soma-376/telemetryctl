package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestSchemaSQLCreatesCurrentSchema(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if got, err := db.SchemaVersion(ctx); err != nil || got != schemaVersion {
		t.Fatalf("SchemaVersion = %d, %v", got, err)
	}
	for _, name := range []string{"vendors", "sessions", "turns", "events", "llm_calls", "tool_calls", "file_changes", "vendor_limit_snapshots"} {
		var n int
		if err := db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n); err != nil || n != 1 {
			t.Fatalf("테이블 %s = %d, %v", name, n, err)
		}
	}
}

func TestSchemaSQLExecutesAsOneScript(t *testing.T) {
	db, err := sql.Open(DriverName, "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "schema.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(context.Background(), schemaSQL); err != nil {
		t.Fatalf("schemaSQL: %v", err)
	}
}

func TestExistingDevelopmentSchemaIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), DefaultFileName)
	raw, err := sql.Open(DriverName, "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(createMetaTable); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO meta ("key",value) VALUES (?,?)`, MetaSchemaVersion, "5"); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	_, err = Open(context.Background(), path)
	if err == nil || !strings.Contains(err.Error(), "pulsemetry.db를 삭제") {
		t.Fatalf("구형 DB 오류 = %v", err)
	}
}
