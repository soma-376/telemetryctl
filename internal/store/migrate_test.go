package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/hostenv"
	"github.com/your-org/pulsemetry/internal/session"
)

// 후속 티켓이 마이그레이션을 추가하는 절차 자체를 검사한다 — migrations 끝에 항목 하나를
// 더하면 이미 만들어진 DB 가 데이터를 잃지 않고 따라 올라와야 한다.
func TestIncrementalMigration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), DefaultFileName)

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open v1: %v", err)
	}
	if _, err := db.Write(ctx, Batch{Sessions: []session.Session{newSession("sess-1", baseTime)}}); err != nil {
		t.Fatalf("Write: %v", err)
	}
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
		t.Fatalf("Open v2: %v", err)
	}
	defer db.Close()

	got, err := db.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if got != LatestSchemaVersion() {
		t.Fatalf("스키마 버전 = %d, want %d", got, LatestSchemaVersion())
	}

	var id string
	var testColumn any
	if err := db.SQL().QueryRowContext(ctx,
		`SELECT session_id, test_column FROM sessions`).Scan(&id, &testColumn); err != nil {
		t.Fatalf("새 컬럼 조회: %v", err)
	}
	if id != "sess-1" {
		t.Fatalf("session_id = %q — 마이그레이션이 기존 데이터를 잃었다", id)
	}
	if testColumn != nil {
		t.Errorf("test_column = %v, want NULL", testColumn)
	}
}

func TestMigrateV1ToV2PreservesDataWithoutBackfill(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), DefaultFileName)
	original := migrations
	t.Cleanup(func() { migrations = original })

	migrations = original[:1]
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open v1: %v", err)
	}
	if _, err := db.Write(ctx, Batch{Sessions: []session.Session{newSession("sess-1", baseTime)}}); err != nil {
		db.Close()
		t.Fatalf("v1 Write: %v", err)
	}
	const legacyPhaseJSON = `[{"type":"legacy"}]`
	if _, err := db.SQL().ExecContext(ctx,
		`UPDATE sessions SET phase_json = ? WHERE session_id = 'sess-1'`, legacyPhaseJSON); err != nil {
		db.Close()
		t.Fatalf("v1 phase_json 설정: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("v1 Close: %v", err)
	}

	migrations = original
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatalf("Open v2: %v", err)
	}
	defer db.Close()

	var phaseJSON string
	if err := db.SQL().QueryRowContext(ctx,
		`SELECT phase_json FROM sessions WHERE session_id = 'sess-1'`).Scan(&phaseJSON); err != nil {
		t.Fatalf("기존 session 조회: %v", err)
	}
	if phaseJSON != legacyPhaseJSON {
		t.Fatalf("phase_json = %q", phaseJSON)
	}
	var nullTurns int
	if err := db.SQL().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tool_events WHERE session_id = 'sess-1' AND turn_index IS NULL`).Scan(&nullTurns); err != nil {
		t.Fatalf("기존 tool_events 조회: %v", err)
	}
	if nullTurns != 2 {
		t.Fatalf("turn_index가 NULL인 기존 tool_events = %d, want 2", nullTurns)
	}
	if countRows(t, db, "turns") != 0 || countRows(t, db, "session_phases") != 0 {
		t.Fatal("v2 마이그레이션이 기존 데이터에서 turn 또는 phase를 백필했다")
	}
}

// 실패한 마이그레이션은 버전을 올리지 않는다. 올려 버리면 다음 기동이 반쯤 적용된 스키마를
// 완성된 것으로 믿는다.
func TestFailedMigrationDoesNotAdvanceVersion(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), DefaultFileName)

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	db.Close()

	original := migrations
	t.Cleanup(func() { migrations = original })
	broken := original[len(original)-1].version + 1
	migrations = append(append([]migration{}, original...), migration{
		version: broken,
		name:    "깨진 마이그레이션",
		stmts: []string{
			`ALTER TABLE sessions ADD COLUMN good_column TEXT`,
			`ALTER TABLE 존재하지않는테이블 ADD COLUMN x TEXT`,
		},
	})

	if _, err := Open(ctx, path); err == nil {
		t.Fatal("깨진 마이그레이션인데 Open 이 성공했다")
	}

	migrations = original
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatalf("복구 Open: %v", err)
	}
	defer db.Close()

	v, err := db.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if v != LatestSchemaVersion() {
		t.Fatalf("스키마 버전 = %d, want %d — 실패한 마이그레이션이 버전을 올렸다", v, LatestSchemaVersion())
	}
	// 첫 문장의 ALTER 도 롤백돼야 한다.
	var n int
	if err := db.SQL().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name = 'good_column'`).Scan(&n); err != nil {
		t.Fatalf("컬럼 확인: %v", err)
	}
	if n != 0 {
		t.Fatal("실패한 마이그레이션의 첫 문장이 롤백되지 않았다")
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
