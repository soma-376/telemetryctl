// Package store 는 로컬 파이프라인의 SQLite 저장소다 (ADR 0002).
//
// 스키마·마이그레이션·쓰기·보존 정책·read-only 연결까지가 이 패키지의 몫이다.
// 화면별 조회 API 는 10단계 internal/dashboard 소관이라 여기 없다.
//
// 드라이버는 순수 Go 구현인 modernc.org/sqlite 다. 드라이버 이름이 "sqlite" 이고
// mattn/go-sqlite3 의 "sqlite3" 가 아니다. CGO 없이 빌드되는 것이 이 선택의 이유이므로
// (ADR 0002 Context) CGO 를 요구하는 의존성을 여기에 들이지 않는다.
//
// # 프로세스 모델
//
// 데몬이 DB 를 소유하고 유일하게 쓴다. GUI 는 OpenReadOnly 로 연다. WAL 이라 둘이 서로를
// 막지 않는다. 쓰기 핸들은 커넥션 하나로 제한한다 — SQLite 는 어차피 동시 쓰기 하나뿐이고,
// 풀이 여러 커넥션을 열면 같은 프로세스 안에서 스스로와 SQLITE_BUSY 를 만든다.
//
// # 단위
//
// events.ts 는 unix 나노초, 그 외 시각 컬럼(sessions.*, tool_events.ts, session_files.last_ts,
// vendors.*, rollup_hourly.hour)은 전부 unix 초다. event 패키지가 UnixNano·UnixSec·Hour 로
// 나눠 두었으므로 이 패키지는 변환을 직접 하지 않고 그 타입을 그대로 받는다.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	// 순수 Go SQLite 드라이버. 드라이버 이름은 "sqlite" 다.
	_ "modernc.org/sqlite"

	"github.com/your-org/pulsemetry/internal/hostenv"
)

const (
	// DriverName 은 database/sql 에 등록된 드라이버 이름이다.
	DriverName = "sqlite"

	// DefaultFileName 은 데이터 디렉터리 안의 DB 파일 이름이다.
	DefaultFileName = "pulsemetry.db"

	// DefaultBusyTimeout 은 계획서 「GUI 연동 형태」가 명시한 busy_timeout(5000) 이다.
	DefaultBusyTimeout = 5 * time.Second
)

// DefaultDataDir 는 데이터 디렉터리다. installer.StatePath 가 state.json 을 두는 곳과 같은
// 디렉터리를 쓴다 (~/.pulsemetry) — 설치 상태와 수집 데이터가 흩어지지 않게 한다.
func DefaultDataDir(env hostenv.Env) string {
	return filepath.Join(env.HomeDir, ".pulsemetry")
}

// DefaultPath 는 기본 DB 경로다.
func DefaultPath(env hostenv.Env) string { return PathIn(DefaultDataDir(env)) }

// PathIn 은 데이터 디렉터리 안의 DB 경로다. daemon --data-dir 가 쓴다.
func PathIn(dataDir string) string { return filepath.Join(dataDir, DefaultFileName) }

// DefaultRetentionDays 는 모든 로컬 데이터의 고정 보존일이다 (PROJ-71).
// events·event_content·tool_events 와 세션·롤업·벤더 계층에 같은 값을 적용한다.
const DefaultRetentionDays = 400

type config struct {
	busyTimeout  time.Duration
	storeContent bool
}

// Option 은 Open 의 동작을 바꾼다.
type Option func(*config)

// WithBusyTimeout 은 잠금 대기 시간을 바꾼다. 0 이하는 무시한다.
func WithBusyTimeout(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.busyTimeout = d
		}
	}
}

// WithContentStorage 는 원문 저장 여부를 바꾼다 (daemon --no-store-content).
// false 면 Write 가 받은 원문을 버린다 — 저장소가 프라이버시 모드의 집행 지점이다.
func WithContentStorage(enabled bool) Option {
	return func(c *config) { c.storeContent = enabled }
}

// DB 는 데몬이 소유하는 읽기·쓰기 핸들이다. 동시 사용에 안전하다(database/sql 이 직렬화한다).
type DB struct {
	db   *sql.DB
	path string
	cfg  config
}

// Open 은 DB 를 열고 마이그레이션을 적용한다. 파일과 상위 디렉터리가 없으면 만든다.
func Open(ctx context.Context, path string, opts ...Option) (*DB, error) {
	cfg := config{
		busyTimeout:  DefaultBusyTimeout,
		storeContent: true,
	}
	for _, o := range opts {
		o(&cfg)
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("store: 경로 해석 (%s): %w", path, err)
	}
	// 원문과 tool details 가 들어가는 파일이라 소유자만 읽게 둔다.
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return nil, fmt.Errorf("store: 데이터 디렉터리 생성: %w", err)
	}

	sqlDB, err := sql.Open(DriverName, writeDSN(abs, cfg.busyTimeout))
	if err != nil {
		return nil, fmt.Errorf("store: DB 열기 (%s): %w", abs, err)
	}
	// 쓰기는 한 커넥션으로 직렬화한다. 풀이 커넥션을 더 열면 같은 프로세스가 스스로와
	// 쓰기 잠금을 다투게 되고, PRAGMA 도 커넥션마다 따로 적용된다.
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(0)

	if err := sqlDB.PingContext(ctx); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("store: DB 연결 (%s): %w", abs, err)
	}
	if err := migrate(ctx, sqlDB); err != nil {
		sqlDB.Close()
		return nil, err
	}
	return &DB{db: sqlDB, path: abs, cfg: cfg}, nil
}

func (d *DB) Close() error { return d.db.Close() }

// Path 는 열려 있는 DB 파일의 절대 경로다.
func (d *DB) Path() string { return d.path }

// SQL 은 하위 핸들이다. 이 패키지가 제공하지 않는 질의가 필요한 호출자(10단계 dashboard)를 위한 것이다.
func (d *DB) SQL() *sql.DB { return d.db }

// StoresContent 는 원문을 저장하는 설정인지 알려준다.
func (d *DB) StoresContent() bool { return d.cfg.storeContent }

// SchemaVersion 은 meta.local_schema_version 값이다.
func (d *DB) SchemaVersion(ctx context.Context) (int, error) { return readSchemaVersion(ctx, d.db) }

// Meta 는 meta 값을 읽는다. 키가 없으면 ok=false.
func (d *DB) Meta(ctx context.Context, key string) (string, bool, error) {
	return readMeta(ctx, d.db, key)
}

// SetMeta 는 meta 값을 쓴다.
func (d *DB) SetMeta(ctx context.Context, key, value string) error {
	if key == MetaSchemaVersion {
		// 버전은 마이그레이션 러너만 옮긴다. 밖에서 고치면 적용된 DDL 과 기록된 버전이 갈린다.
		return fmt.Errorf("store: %s 는 마이그레이션 러너가 소유한다", MetaSchemaVersion)
	}
	if err := setMetaTx(ctx, d.db, key, value); err != nil {
		return fmt.Errorf("store: meta 쓰기 (%s): %w", key, err)
	}
	return nil
}

// rowQuerier 는 읽기·쓰기 핸들과 read-only 핸들이 함께 만족하는 최소 인터페이스다.
type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func readMeta(ctx context.Context, q rowQuerier, key string) (string, bool, error) {
	var v string
	err := q.QueryRowContext(ctx, `SELECT value FROM meta WHERE "key" = ?`, key).Scan(&v)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("store: meta 조회 (%s): %w", key, err)
	}
	return v, true, nil
}

// writeDSN 은 쓰기 커넥션의 DSN 이다.
//
// PRAGMA 를 DSN 에 싣는 이유는 커넥션마다 따로 적용되기 때문이다. 코드에서 한 번 실행하면
// 풀이 새 커넥션을 열 때 그 커넥션에는 적용되지 않는다. foreign_keys 는 특히 그렇다 —
// 꺼진 채로도 SQL 은 전부 성공하고 CASCADE 만 조용히 동작하지 않는다.
func writeDSN(path string, busy time.Duration) string {
	return dsn(path, nil, []string{
		// GUI 가 데몬 쓰기 중에 읽어야 한다 (ADR 0002).
		"journal_mode(WAL)",
		// 잠금 경합을 드라이버 안에서 기다린다. 없으면 GUI 가 읽는 순간의 쓰기가 SQLITE_BUSY 로 즉시 실패한다.
		fmt.Sprintf("busy_timeout(%d)", busy.Milliseconds()),
		// 계획서 스키마가 ON DELETE CASCADE 에 의존한다. SQLite 기본값이 OFF 라 명시하지 않으면
		// 세션을 지워도 session_files·tool_events·mcp_session_usage 가 고아로 남는다.
		"foreign_keys(1)",
		// CASCADE 로 지워진 event_content 행에 대해서도 AFTER DELETE 트리거가 돌아야
		// content_fts 가 따라 정리된다.
		"recursive_triggers(1)",
		// WAL 에서 권장되는 조합이다. 커밋마다 fsync 하지 않아 수신 경로의 쓰기 지연이 줄고,
		// 잃을 수 있는 것은 OS 크래시 직전 몇 건의 텔레메트리뿐이다.
		"synchronous(NORMAL)",
	})
}

// readDSN 은 계획서 「GUI 연동 형태」가 명시한 mode=ro & busy_timeout(5000) 연결이다.
func readDSN(path string, busy time.Duration) string {
	return dsn(path,
		[]string{"mode=ro"},
		[]string{fmt.Sprintf("busy_timeout(%d)", busy.Milliseconds())},
	)
}

// dsn 은 SQLite URI 를 만든다. mode=ro 는 URI 형식에서만 인식되므로 file: 접두가 필요하고,
// 그러면 경로도 URI 규칙(구분자 /, 퍼센트 인코딩)을 따라야 한다.
func dsn(path string, uriParams, pragmas []string) string {
	params := append([]string{}, uriParams...)
	for _, p := range pragmas {
		params = append(params, "_pragma="+url.QueryEscape(p))
	}
	return "file://" + uriPath(path) + "?" + strings.Join(params, "&")
}

// uriPath 는 파일 경로를 file:// URI 의 경로 부분으로 옮긴다.
// Windows 의 C:\a\b 는 /C:/a/b 가 된다 — SQLite 가 문서화한 형식이다.
func uriPath(p string) string {
	s := filepath.ToSlash(p)
	if !strings.HasPrefix(s, "/") {
		s = "/" + s
	}
	return (&url.URL{Path: s}).EscapedPath()
}
