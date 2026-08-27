package store

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestOpenAppliesMigrations(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	got, err := db.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if want := LatestSchemaVersion(); got != want {
		t.Fatalf("스키마 버전 = %d, want %d", got, want)
	}

	// meta 는 마이그레이션 러너가 유지하고, 나머지는 v3 DDL 의 도메인 테이블이다.
	tables := []string{
		"meta", "vendors", "sessions", "turns", "events", "llm_calls", "tool_calls", "file_changes",
	}
	for _, name := range tables {
		var n int
		err := db.SQL().QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master WHERE name = ?`, name).Scan(&n)
		if err != nil {
			t.Fatalf("sqlite_master 조회 (%s): %v", name, err)
		}
		if n == 0 {
			t.Errorf("테이블 %s 가 없다", name)
		}
	}

	legacy := []string{
		"session_phases", "session_files", "tool_events", "mcp_session_usage",
		"event_content", "content_fts", "rollup_hourly",
	}
	for _, name := range legacy {
		var n int
		if err := db.SQL().QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master WHERE name = ?`, name).Scan(&n); err != nil {
			t.Fatalf("legacy sqlite_master 조회 (%s): %v", name, err)
		}
		if n != 0 {
			t.Errorf("v3 에 제거돼야 할 객체 %s 가 남았다", name)
		}
	}
}

func TestSchemaV3NamedIndexesAndForeignKeys(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	indexes := []string{"ux_turns_virtual", "ix_events_name", "ix_llm_turn", "ix_fc_tool"}
	for _, name := range indexes {
		var n int
		if err := db.SQL().QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, name).Scan(&n); err != nil {
			t.Fatalf("인덱스 조회 (%s): %v", name, err)
		}
		if n != 1 {
			t.Errorf("인덱스 %s = %d개, want 1", name, n)
		}
	}
	var namedIndexCount int
	if err := db.SQL().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND sql IS NOT NULL`).Scan(&namedIndexCount); err != nil {
		t.Fatalf("명명 인덱스 계수: %v", err)
	}
	if namedIndexCount != len(indexes) {
		t.Fatalf("명명 인덱스 = %d개, want %d", namedIndexCount, len(indexes))
	}

	var unique, partial int
	if err := db.SQL().QueryRowContext(ctx, `
		SELECT "unique", partial FROM pragma_index_list('turns') WHERE name = 'ux_turns_virtual'`).
		Scan(&unique, &partial); err != nil {
		t.Fatalf("ux_turns_virtual 속성 조회: %v", err)
	}
	if unique != 1 || partial != 1 {
		t.Fatalf("ux_turns_virtual = unique %d, partial %d", unique, partial)
	}

	fks := map[string]map[string]int{
		"sessions":     {"vendors": 1},
		"turns":        {"sessions": 1},
		"events":       {"turns": 1},
		"llm_calls":    {"turns": 1, "events": 1},
		"tool_calls":   {"turns": 1, "events": 2},
		"file_changes": {"tool_calls": 1},
	}
	for table, parents := range fks {
		for parent, want := range parents {
			var n int
			if err := db.SQL().QueryRowContext(ctx,
				`SELECT COUNT(*) FROM pragma_foreign_key_list(?) WHERE "table" = ?`, table, parent).Scan(&n); err != nil {
				t.Fatalf("외래 키 조회 (%s -> %s): %v", table, parent, err)
			}
			if n != want {
				t.Errorf("%s -> %s 외래 키 = %d개, want %d", table, parent, n, want)
			}
		}
	}
	var nonDefaultDeletes int
	for table := range fks {
		var n int
		if err := db.SQL().QueryRowContext(ctx,
			`SELECT COUNT(*) FROM pragma_foreign_key_list(?) WHERE on_delete != 'NO ACTION'`, table).Scan(&n); err != nil {
			t.Fatalf("외래 키 삭제 동작 조회 (%s): %v", table, err)
		}
		nonDefaultDeletes += n
	}
	if nonDefaultDeletes != 0 {
		t.Fatalf("DDL에 없는 외래 키 삭제 동작 = %d개", nonDefaultDeletes)
	}
}

func TestSchemaV3Columns(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	want := map[string][]string{
		"vendors": {"vendor", "first_seen", "last_seen", "status"},
		"sessions": {
			"id", "vendor_id", "session_key", "title", "workspace_path", "user_email",
			"user_account_id", "terminal_type", "started_at", "ended_at", "active_time_sec",
		},
		"turns": {
			"id", "session_id", "turn_key", "turn_index", "client_version", "started_at",
			"ended_at", "prompt_text", "ttft_ms",
		},
		"events": {
			"id", "turn_id", "seq", "event_name", "occurred_at", "record_hash", "payload",
		},
		"llm_calls": {
			"id", "turn_id", "source_event_id", "called_at", "model", "input_tokens",
			"output_tokens", "cache_read_tokens", "cache_write_tokens", "reasoning_tokens",
			"cost_usd", "duration_ms", "request_id",
		},
		"tool_calls": {
			"id", "turn_id", "call_key", "decision_event_id", "result_event_id", "tool_name",
			"target", "mcp_server", "called_at", "duration_ms", "blocked_on_user_ms", "success",
			"decision", "decision_source", "input_size_bytes", "result_size_bytes", "error_type",
			"error_message",
		},
		"file_changes": {
			"id", "tool_call_id", "file_path", "operation", "renamed_from", "additions",
			"deletions", "old_hash", "new_hash",
		},
	}

	for table, expected := range want {
		rows, err := db.SQL().QueryContext(ctx, `SELECT name FROM pragma_table_info(?) ORDER BY cid`, table)
		if err != nil {
			t.Fatalf("컬럼 조회 (%s): %v", table, err)
		}
		var got []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				t.Fatalf("컬럼 스캔 (%s): %v", table, err)
			}
			got = append(got, name)
		}
		if err := rows.Close(); err != nil {
			t.Fatalf("컬럼 조회 종료 (%s): %v", table, err)
		}
		if strings.Join(got, ",") != strings.Join(expected, ",") {
			t.Errorf("%s 컬럼 = %v, want %v", table, got, expected)
		}
	}
}

func TestSchemaV3Constraints(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	mustExec(t, db, `INSERT INTO vendors (vendor, first_seen, last_seen, status) VALUES ('codex', 1, 2, 'enabled')`)
	expectConstraint(t, db, `INSERT INTO vendors (vendor, first_seen, last_seen, status) VALUES ('bad', 1, 2, 'unknown')`)
	mustExec(t, db, `INSERT INTO sessions (id, vendor_id, session_key) VALUES (1, 'codex', 'session-1')`)
	mustExec(t, db, `INSERT INTO sessions (id, vendor_id, session_key) VALUES (2, 'codex', 'session-2')`)
	expectConstraint(t, db, `INSERT INTO sessions (vendor_id, session_key) VALUES ('codex', 'session-1')`)

	mustExec(t, db, `INSERT INTO turns (id, session_id, turn_key, turn_index) VALUES (1, 1, 'turn-1', 1)`)
	mustExec(t, db, `INSERT INTO turns (id, session_id, turn_key, turn_index) VALUES (2, 1, '__unattributed__', NULL)`)
	mustExec(t, db, `INSERT INTO turns (id, session_id, turn_key, turn_index) VALUES (3, 2, '__unattributed__', NULL)`)
	expectConstraint(t, db, `INSERT INTO turns (session_id, turn_key, turn_index) VALUES (1, 'turn-1', 2)`)
	expectConstraint(t, db, `INSERT INTO turns (session_id, turn_key, turn_index) VALUES (1, 'turn-2', 1)`)
	expectConstraint(t, db, `INSERT INTO turns (session_id, turn_key, turn_index) VALUES (1, 'virtual-2', NULL)`)

	mustExec(t, db, `INSERT INTO events (id, turn_id, seq, event_name, record_hash, payload)
		VALUES (1, 1, 1, 'response', 'hash-1', jsonb('{"ok":true}'))`)
	mustExec(t, db, `INSERT INTO events (id, turn_id, seq, event_name, record_hash)
		VALUES (2, 1, 2, 'tool_result', 'hash-2')`)
	expectConstraint(t, db, `INSERT INTO events (turn_id, seq, event_name, record_hash)
		VALUES (1, 2, 'duplicate-seq', 'hash-3')`)
	expectConstraint(t, db, `INSERT INTO events (turn_id, seq, event_name, record_hash)
		VALUES (1, 3, 'duplicate-hash', 'hash-1')`)
	expectConstraint(t, db, `INSERT INTO events (turn_id, seq, event_name, record_hash, payload)
		VALUES (1, 3, 'bad-jsonb', 'hash-3', x'0102')`)

	mustExec(t, db, `INSERT INTO llm_calls (id, turn_id, source_event_id) VALUES (1, 1, 1)`)
	expectConstraint(t, db, `INSERT INTO llm_calls (turn_id, source_event_id) VALUES (1, 1)`)
	expectConstraint(t, db, `INSERT INTO tool_calls (turn_id, call_key) VALUES (1, 'call-empty')`)
	mustExec(t, db, `INSERT INTO tool_calls (id, turn_id, call_key, decision_event_id) VALUES (1, 1, 'call-1', 2)`)
	expectConstraint(t, db, `INSERT INTO tool_calls (turn_id, call_key, decision_event_id) VALUES (1, 'call-2', 2)`)
	expectConstraint(t, db, `INSERT INTO tool_calls (turn_id, call_key, result_event_id) VALUES (1, 'call-1', 1)`)
	mustExec(t, db, `INSERT INTO file_changes (tool_call_id, file_path, operation) VALUES (1, 'main.go', 'modify')`)
	expectConstraint(t, db, `INSERT INTO file_changes (tool_call_id, file_path, operation) VALUES (1, 'main.go', 'move')`)

	var n int
	if err := db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM file_changes`).Scan(&n); err != nil {
		t.Fatalf("file_changes 계수: %v", err)
	}
	if n != 1 {
		t.Fatalf("file_changes = %d행, want 1", n)
	}
}

func mustExec(t *testing.T, db *DB, query string, args ...any) {
	t.Helper()
	if _, err := db.SQL().ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("SQL 실행 실패: %v\n%s", err, query)
	}
}

func expectConstraint(t *testing.T, db *DB, query string, args ...any) {
	t.Helper()
	if _, err := db.SQL().ExecContext(context.Background(), query, args...); err == nil {
		t.Fatalf("제약 위반 SQL 이 성공했다:\n%s", query)
	}
}

// foreign_keys PRAGMA 는 켜지 않아도 모든 SQL 이 성공한다. CASCADE 만 조용히 동작하지 않아서
// 보존 정책이 고아 행을 남긴다 — PRAGMA 자체를 단언해 그 조용한 실패를 막는다.
func TestPragmasEnabled(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	tests := []struct {
		pragma string
		want   string
	}{
		{"foreign_keys", "1"},
		{"recursive_triggers", "1"},
		{"journal_mode", "wal"},
	}
	for _, tc := range tests {
		t.Run(tc.pragma, func(t *testing.T) {
			var got string
			if err := db.SQL().QueryRowContext(ctx, "PRAGMA "+tc.pragma).Scan(&got); err != nil {
				t.Fatalf("PRAGMA %s: %v", tc.pragma, err)
			}
			if got != tc.want {
				t.Fatalf("PRAGMA %s = %q, want %q", tc.pragma, got, tc.want)
			}
		})
	}

	var busy int
	if err := db.SQL().QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busy); err != nil {
		t.Fatalf("PRAGMA busy_timeout: %v", err)
	}
	if want := int(DefaultBusyTimeout.Milliseconds()); busy != want {
		t.Fatalf("busy_timeout = %d, want %d", busy, want)
	}
}

func TestReopenIsIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), DefaultFileName)

	for i := 0; i < 2; i++ {
		db, err := Open(ctx, path)
		if err != nil {
			t.Fatalf("Open %d회차: %v", i+1, err)
		}
		v, err := db.SchemaVersion(ctx)
		if err != nil {
			t.Fatalf("SchemaVersion: %v", err)
		}
		if v != LatestSchemaVersion() {
			t.Fatalf("스키마 버전 = %d, want %d", v, LatestSchemaVersion())
		}
		db.Close()
	}
}

// 더 새로운 telemetryctl 이 만든 DB 를 옛 바이너리가 열면 안 된다.
// 그쪽이 아는 컬럼을 우리가 모른 채 덮어쓰기 때문이다.
func TestOpenRejectsNewerSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), DefaultFileName)

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	future := strconv.Itoa(LatestSchemaVersion() + 1)
	if _, err := db.SQL().ExecContext(ctx,
		`UPDATE meta SET value = ? WHERE "key" = ?`, future, MetaSchemaVersion); err != nil {
		t.Fatalf("버전 조작: %v", err)
	}
	db.Close()

	if _, err := Open(ctx, path); err == nil {
		t.Fatal("더 높은 스키마 버전인데 Open 이 성공했다")
	}
}

func TestMetaRoundTrip(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	if _, ok, err := db.Meta(ctx, MetaInstallationID); err != nil || ok {
		t.Fatalf("빈 meta 조회 = (%v, %v), want (false, nil)", ok, err)
	}
	if err := db.SetMeta(ctx, MetaInstallationID, "inst-1"); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	if err := db.SetMeta(ctx, MetaInstallationID, "inst-2"); err != nil {
		t.Fatalf("SetMeta 덮어쓰기: %v", err)
	}
	v, ok, err := db.Meta(ctx, MetaInstallationID)
	if err != nil || !ok || v != "inst-2" {
		t.Fatalf("Meta = (%q, %v, %v), want (inst-2, true, nil)", v, ok, err)
	}

	// 스키마 버전은 마이그레이션 러너만 옮긴다.
	if err := db.SetMeta(ctx, MetaSchemaVersion, "99"); err == nil {
		t.Fatal("SetMeta 가 local_schema_version 을 허용했다")
	}
}

func TestOpenReadOnly(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, DefaultFileName)

	t.Run("DB 없음은 ErrNoDatabase", func(t *testing.T) {
		_, err := OpenReadOnly(path)
		if !errors.Is(err, ErrNoDatabase) {
			t.Fatalf("err = %v, want ErrNoDatabase", err)
		}
		// ServiceStartup 이 error 를 내면 GUI 기동이 중단되므로 미설치는 빈 결과다.
		r, err := OpenReadOnlyIfPresent(path)
		if err != nil || r != nil {
			t.Fatalf("OpenReadOnlyIfPresent = (%v, %v), want (nil, nil)", r, err)
		}
	})

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	if err := db.SetMeta(ctx, MetaInstallationID, "inst-1"); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}

	t.Run("읽기는 되고 쓰기는 막힌다", func(t *testing.T) {
		ro, err := OpenReadOnly(path)
		if err != nil {
			t.Fatalf("OpenReadOnly: %v", err)
		}
		defer ro.Close()

		v, ok, err := ro.Meta(ctx, MetaInstallationID)
		if err != nil || !ok || v != "inst-1" {
			t.Fatalf("Meta = (%q, %v, %v)", v, ok, err)
		}
		if got, err := ro.SchemaVersion(ctx); err != nil || got != LatestSchemaVersion() {
			t.Fatalf("SchemaVersion = (%d, %v)", got, err)
		}
		if _, err := ro.SQL().ExecContext(ctx, `DELETE FROM meta`); err == nil {
			t.Fatal("read-only 연결에서 DELETE 가 성공했다")
		}
	})
}
