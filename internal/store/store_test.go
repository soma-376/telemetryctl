package store

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/your-org/pulsemetry/internal/session"
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

	// 계획서 스키마의 테이블·가상 테이블이 전부 만들어졌는지 본다.
	tables := []string{
		"meta", "sessions", "turns", "session_phases", "session_files", "tool_events", "mcp_session_usage",
		"vendors", "events", "event_content", "content_fts", "rollup_hourly",
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
}

// WITHOUT ROWID 는 계획서가 지정한 저장 형태다. 빠뜨려도 SQL 은 전부 동작하므로
// 스키마 문자열을 직접 확인하는 수밖에 없다.
func TestWithoutRowidTables(t *testing.T) {
	db := openTestDB(t)
	for _, name := range []string{"turns", "session_phases", "session_files", "mcp_session_usage", "rollup_hourly"} {
		var ddl string
		err := db.SQL().QueryRowContext(context.Background(),
			`SELECT sql FROM sqlite_master WHERE type='table' AND name = ?`, name).Scan(&ddl)
		if err != nil {
			t.Fatalf("%s DDL 조회: %v", name, err)
		}
		if !contains(ddl, "WITHOUT ROWID") {
			t.Errorf("%s 가 WITHOUT ROWID 가 아니다", name)
		}
	}
}

func TestTurnAndPhaseSchema(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	rows, err := db.SQL().QueryContext(ctx, `PRAGMA index_info('idx_tool_events_turn')`)
	if err != nil {
		t.Fatalf("idx_tool_events_turn 조회: %v", err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var seq, cid int
		var name string
		if err := rows.Scan(&seq, &cid, &name); err != nil {
			t.Fatalf("idx_tool_events_turn 컬럼 조회: %v", err)
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("idx_tool_events_turn 순회: %v", err)
	}
	if got := strings.Join(columns, ","); got != "session_id,turn_index,ts" {
		t.Fatalf("idx_tool_events_turn 컬럼 = %s", got)
	}

	for _, id := range []string{"sess-1", "sess-2"} {
		if _, err := db.Write(ctx, Batch{Sessions: []session.Session{newSession(id, baseTime)}}); err != nil {
			t.Fatalf("세션 %s Write: %v", id, err)
		}
		if _, err := db.SQL().ExecContext(ctx, `
			INSERT INTO turns (session_id, turn_index, started_at, last_event_at)
			VALUES (?, 1, ?, ?)`, id, baseTime.Unix(), baseTime.Unix()); err != nil {
			t.Fatalf("턴 %s INSERT: %v", id, err)
		}
		if _, err := db.SQL().ExecContext(ctx, `
			INSERT INTO session_phases
			  (session_id, phase_index, phase_type, start_turn_index, end_turn_index, started_at, last_event_at, turn_count)
			VALUES (?, 1, 'implementation', 1, 1, ?, ?, 1)`, id, baseTime.Unix(), baseTime.Unix()); err != nil {
			t.Fatalf("단계 %s INSERT: %v", id, err)
		}
	}
	if _, err := db.SQL().ExecContext(ctx, `
		INSERT INTO turns (session_id, turn_index, started_at, last_event_at)
		VALUES ('sess-1', 1, ?, ?)`, baseTime.Unix(), baseTime.Unix()); err == nil {
		t.Fatal("같은 세션의 중복 turn_index가 허용됐다")
	}
	if _, err := db.SQL().ExecContext(ctx, `
		INSERT INTO session_phases
		  (session_id, phase_index, phase_type, start_turn_index, end_turn_index, started_at, last_event_at)
		VALUES ('sess-1', 1, 'review', 1, 1, ?, ?)`, baseTime.Unix(), baseTime.Unix()); err == nil {
		t.Fatal("같은 세션의 중복 phase_index가 허용됐다")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
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
