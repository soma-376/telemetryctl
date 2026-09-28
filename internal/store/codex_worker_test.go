package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackfillCodexPendingOnCurrentV1DDL(t *testing.T) {
	db := openTestDB(t)
	for _, query := range []string{
		`INSERT INTO vendors(vendor,first_seen,last_seen,status) VALUES ('codex',1,2,'enabled'),('claude_code',1,2,'enabled')`,
		`INSERT INTO sessions(id,vendor_id,session_key) VALUES (1,'codex','old-owner'),(2,'claude_code','other')`,
		`INSERT INTO turns(id,session_id,turn_key,turn_index,prompt_text) VALUES
(1,1,'old-turn',1,'old prompt'),(2,1,'virtual',NULL,NULL),(3,2,'claude-turn',1,'other')`,
	} {
		if _, err := db.SQL().Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	created, err := db.BackfillCodexPending(context.Background())
	if err != nil || created != 1 {
		t.Fatalf("현재 DDL 과거 행 backfill = %d, %v", created, err)
	}
	created, err = db.BackfillCodexPending(context.Background())
	if err != nil || created != 0 {
		t.Fatalf("반복 backfill = %d, %v", created, err)
	}
	var p, q, turns int
	_ = db.SQL().QueryRow(`SELECT COUNT(*) FROM codex_turn_provenance`).Scan(&p)
	_ = db.SQL().QueryRow(`SELECT COUNT(*) FROM codex_pending`).Scan(&q)
	_ = db.SQL().QueryRow(`SELECT COUNT(*) FROM turns`).Scan(&turns)
	if p != 1 || q != 1 || turns != 3 {
		t.Fatalf("기존 데이터 범위 변경: provenance=%d pending=%d turns=%d", p, q, turns)
	}
}

func TestLegacyV1ShapeRejectedWithoutChangingDeleteJournalDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	raw, err := sql.Open(DriverName, "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`PRAGMA journal_mode=DELETE`,
		`CREATE TABLE meta ("key" TEXT PRIMARY KEY,value TEXT NOT NULL)`,
		`INSERT INTO meta("key",value) VALUES ('local_schema_version','1')`,
		`CREATE TABLE turns(id INTEGER PRIMARY KEY,prompt_text TEXT)`,
		`INSERT INTO turns(id,prompt_text) VALUES (1,'preserved')`,
	} {
		if _, err := raw.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	entriesBefore, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Open(context.Background(), path)
	if err == nil || !strings.Contains(err.Error(), "재생성해야 한다") {
		t.Fatalf("구형 v1 구조 거부 = %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	entriesAfter, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) || len(entriesBefore) != len(entriesAfter) {
		t.Fatalf("거부된 구형 DB 바이트/sidecar 변경: bytes %d/%d entries %d/%d", len(before), len(after), len(entriesBefore), len(entriesAfter))
	}
	read, err := sql.Open(DriverName, "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	var body string
	if err := read.QueryRow(`SELECT prompt_text FROM turns WHERE id=1`).Scan(&body); err != nil || body != "preserved" {
		t.Fatalf("구형 DB 행 손상: body=%q err=%v", body, err)
	}
}

func TestExistingEmptySQLiteFileGetsCurrentDDL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.db")
	raw, err := sql.Open(DriverName, "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`PRAGMA user_version=1`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	version, err := db.SchemaVersion(context.Background())
	if err != nil || version != 1 {
		t.Fatalf("빈 SQLite 파일 초기화 = %d, %v", version, err)
	}
}

func TestCodexEvidenceCacheBoundsOversizedPendingAndCandidate(t *testing.T) {
	db := openTestDB(t)
	for _, query := range []string{
		`INSERT INTO vendors(vendor,first_seen,last_seen,status) VALUES ('codex',1,2,'enabled')`,
		`INSERT INTO sessions(id,vendor_id,session_key) VALUES (1,'codex','owner')`,
		`INSERT INTO turns(id,session_id,turn_key,turn_index,prompt_message_id) VALUES (1,1,'turn',1,?)`,
		`INSERT INTO codex_turn_provenance(turn_id,label,processing_state,link_state) VALUES (1,'unknown','pending','not_attempted')`,
		`INSERT INTO codex_pending(turn_id) VALUES (1)`,
	} {
		var args []any
		if strings.Contains(query, "prompt_message_id") {
			args = []any{strings.Repeat("m", 300<<10)}
		}
		if _, err := db.SQL().Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	pending, err := db.ListCodexPending(context.Background(), 1, 64)
	if err != nil || len(pending) != 1 || !pending[0].TooLarge || pending[0].MessageID != "" || pending[0].Owner != "" {
		t.Fatalf("큰 pending 원문이 반환됨: count=%d err=%v", len(pending), err)
	}
	for _, item := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO codex_jsonl_files(id,path,generation,os_file_id,owner_session_key,observed_at) VALUES (1,'test',1,'inode','owner',1)`, nil},
		{`INSERT INTO codex_jsonl_records(file_id,start_offset,end_offset,record_type,event_time,message_id,structure_evidence,representation_key)
VALUES (1,0,1,'event_msg',1700000000250000000,'m1','{"form":"UserMessage"}',?)`, []any{strings.Repeat("r", 9<<20)}},
	} {
		if _, err := db.SQL().Exec(item.query, item.args...); err != nil {
			t.Fatal(err)
		}
	}
	records, overflow, err := db.CodexCandidateRecords(context.Background(), CodexPromptRow{Owner: "owner", MessageID: "m1"})
	if err != nil || !overflow || len(records) != 0 {
		t.Fatalf("큰 후보를 잘라 선택함: records=%d overflow=%t err=%v", len(records), overflow, err)
	}
}
