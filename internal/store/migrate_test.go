package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/hostenv"
	"github.com/your-org/pulsemetry/internal/session"
)

// v3 이후의 일반 증분 마이그레이션은 기존 데이터를 보존한다. v3 자체만 의도적으로
// 파괴적이며 이후 마이그레이션까지 데이터 손실을 기본값으로 만들지는 않는다.
func TestIncrementalMigration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), DefaultFileName)

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open v3: %v", err)
	}
	mustExec(t, db, `INSERT INTO vendors (vendor, first_seen, last_seen, status) VALUES ('codex', 1, 2, 'enabled')`)
	mustExec(t, db, `INSERT INTO sessions (id, vendor_id, session_key) VALUES (1, 'codex', 'sess-1')`)
	db.Close()

	// 후속 티켓이 추가할 마이그레이션을 흉내낸다.
	original := migrations
	t.Cleanup(func() { migrations = original })
	migrations = append(append([]migration{}, original...), migration{
		version: original[len(original)-1].version + 1,
		name:    "테스트용 컬럼",
		stmts:   []string{`ALTER TABLE sessions ADD COLUMN test_column TEXT`},
	})

	db, err = Open(ctx, path)
	if err != nil {
		t.Fatalf("Open v4: %v", err)
	}
	defer db.Close()

	got, err := db.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if got != LatestSchemaVersion() {
		t.Fatalf("스키마 버전 = %d, want %d", got, LatestSchemaVersion())
	}

	var id int
	var sessionKey string
	var testColumn any
	if err := db.SQL().QueryRowContext(ctx,
		`SELECT id, session_key, test_column FROM sessions`).Scan(&id, &sessionKey, &testColumn); err != nil {
		t.Fatalf("새 컬럼 조회: %v", err)
	}
	if id != 1 || sessionKey != "sess-1" {
		t.Fatalf("session = (%d, %q) — 증분 마이그레이션이 기존 데이터를 잃었다", id, sessionKey)
	}
	if testColumn != nil {
		t.Errorf("test_column = %v, want NULL", testColumn)
	}
}

func TestMigrateV2ToV3RecreatesDomainSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), DefaultFileName)
	original := migrations
	t.Cleanup(func() { migrations = original })

	migrations = original[:2]
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open v2: %v", err)
	}
	if _, err := db.Write(ctx, Batch{Sessions: []session.Session{newSession("sess-1", baseTime)}}); err != nil {
		db.Close()
		t.Fatalf("v2 Write: %v", err)
	}
	if err := db.SetMeta(ctx, MetaInstallationID, "inst-1"); err != nil {
		db.Close()
		t.Fatalf("v2 meta 설정: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("v2 Close: %v", err)
	}

	migrations = original
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatalf("Open v3: %v", err)
	}
	defer db.Close()

	if countRows(t, db, "sessions") != 0 || countRows(t, db, "vendors") != 0 {
		t.Fatal("v3 마이그레이션이 기존 도메인 데이터를 남겼다")
	}
	if value, ok, err := db.Meta(ctx, MetaInstallationID); err != nil || !ok || value != "inst-1" {
		t.Fatalf("보존된 meta = (%q, %v, %v), want (inst-1, true, nil)", value, ok, err)
	}
	for _, name := range []string{"session_phases", "session_files", "tool_events", "mcp_session_usage", "event_content", "content_fts", "rollup_hourly"} {
		var n int
		if err := db.SQL().QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master WHERE name = ?`, name).Scan(&n); err != nil {
			t.Fatalf("sqlite_master 조회 (%s): %v", name, err)
		}
		if n != 0 {
			t.Errorf("v3 에서 제거한 객체 %s 가 남았다", name)
		}
	}
}

// 파괴적 v3 도 단일 트랜잭션이다. 마지막 생성 뒤 실패해도 삭제한 v2 테이블과 데이터,
// local_schema_version 이 모두 원래 상태로 돌아와야 한다.
func TestFailedV3MigrationRollsBackDropsAndVersion(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), DefaultFileName)
	original := migrations
	t.Cleanup(func() { migrations = original })

	migrations = original[:2]
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open v2: %v", err)
	}
	if _, err := db.Write(ctx, Batch{Sessions: []session.Session{newSession("sess-1", baseTime)}}); err != nil {
		db.Close()
		t.Fatalf("v2 Write: %v", err)
	}
	db.Close()

	brokenV3 := append(append([]string{}, schemaV3...),
		`ALTER TABLE 존재하지않는테이블 ADD COLUMN x TEXT`)
	migrations = append(append([]migration{}, original[:2]...), migration{
		version: 3,
		name:    "깨진 v3",
		stmts:   brokenV3,
	})

	if _, err := Open(ctx, path); err == nil {
		t.Fatal("깨진 v3 마이그레이션인데 Open 이 성공했다")
	}

	migrations = original[:2]
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatalf("v2 확인 Open: %v", err)
	}
	defer db.Close()

	v, err := db.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if v != 2 {
		t.Fatalf("스키마 버전 = %d, want 2 — 실패한 v3가 버전을 올렸다", v)
	}
	if countWhere(t, db, "sessions", "session_id = 'sess-1'") != 1 {
		t.Fatal("실패한 v3가 기존 sessions 데이터를 복구하지 못했다")
	}
	var oldTables, newTables int
	if err := db.SQL().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE name = 'event_content'`).Scan(&oldTables); err != nil {
		t.Fatalf("v2 테이블 확인: %v", err)
	}
	if err := db.SQL().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE name = 'llm_calls'`).Scan(&newTables); err != nil {
		t.Fatalf("v3 테이블 확인: %v", err)
	}
	if oldTables != 1 || newTables != 0 {
		t.Fatalf("롤백 후 old event_content=%d, new llm_calls=%d", oldTables, newTables)
	}
}

func TestPathHelpers(t *testing.T) {
	env := hostenv.Env{HomeDir: filepath.Join("/home", "jy")}
	if got, want := DefaultDataDir(env), filepath.Join("/home", "jy", ".pulsemetry"); got != want {
		t.Errorf("DefaultDataDir = %q, want %q", got, want)
	}
	if got, want := DefaultPath(env), filepath.Join("/home", "jy", ".pulsemetry", DefaultFileName); got != want {
		t.Errorf("DefaultPath = %q, want %q", got, want)
	}
	if got, want := PathIn("/tmp/pm-test"), filepath.Join("/tmp/pm-test", DefaultFileName); got != want {
		t.Errorf("PathIn = %q, want %q", got, want)
	}
}

func TestOptions(t *testing.T) {
	db := openTestDB(t, WithBusyTimeout(1500*time.Millisecond), WithContentStorage(false))
	if db.StoresContent() {
		t.Error("WithContentStorage(false) 가 반영되지 않았다")
	}
	if got := db.Path(); filepath.Base(got) != DefaultFileName {
		t.Errorf("Path = %q", got)
	}
	var busy int
	if err := db.SQL().QueryRowContext(context.Background(), "PRAGMA busy_timeout").Scan(&busy); err != nil {
		t.Fatalf("PRAGMA busy_timeout: %v", err)
	}
	if busy != 1500 {
		t.Fatalf("busy_timeout = %d, want 1500", busy)
	}
}

// 공백처럼 URI 인코딩이 필요한 경로에서도 열려야 한다.
func TestOpenPathWithSpaces(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "Application Support", "pulse metry")
	db, err := Open(ctx, filepath.Join(dir, DefaultFileName))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	if _, err := db.Write(ctx, Batch{Sessions: []session.Session{newSession("sess-1", baseTime)}}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	ro, err := OpenReadOnly(db.Path())
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	defer ro.Close()
	if got := ro.Path(); got != db.Path() {
		t.Errorf("Path = %q, want %q", got, db.Path())
	}
}
