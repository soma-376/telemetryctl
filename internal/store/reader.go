package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrNoDatabase 는 DB 파일이 아직 없다는 뜻이다.
//
// 미설치·데몬 미실행 상태에서 GUI 가 뜨는 것은 정상이다. 계획서 「GUI 연동 형태」가 명시한
// 대로 ServiceStartup 이 error 를 내면 앱 기동이 통째로 중단되므로, 이 상태를 실패가 아니라
// 빈 결과로 다룰 수 있게 별도 센티널로 구분한다.
var ErrNoDatabase = errors.New("store: 데이터베이스 파일이 없음")

// ReadOnly 는 GUI 가 쓰는 read-only 연결이다 (ADR 0002·0004).
//
// 조회 API 자체는 10단계 internal/dashboard 소관이다. 여기서는 "어떻게 여는가" 만 소유한다 —
// mode=ro 를 빠뜨리면 화면이 데이터를 오염시킬 수 있고, busy_timeout 을 빠뜨리면 데몬이
// 쓰는 순간의 조회가 SQLITE_BUSY 로 즉시 실패한다.
type ReadOnly struct {
	db   *sql.DB
	path string
}

// OpenReadOnly 는 계획서가 명시한 mode=ro & busy_timeout(5000) 연결을 연다.
// 파일이 없으면 ErrNoDatabase 를 감싼 에러를 돌려준다.
func OpenReadOnly(path string, opts ...Option) (*ReadOnly, error) {
	cfg := config{busyTimeout: DefaultBusyTimeout}
	for _, o := range opts {
		o(&cfg)
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("store: 경로 해석 (%s): %w", path, err)
	}
	// mode=ro 로 열면 SQLite 가 파일을 만들지 않고 실패하는데, 그 에러는 잠금 실패·손상과
	// 구분되지 않는다. 부재를 별도 센티널로 만들려면 먼저 확인하는 수밖에 없다.
	if _, err := os.Stat(abs); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w (%s)", ErrNoDatabase, abs)
		}
		return nil, fmt.Errorf("store: DB 파일 확인 (%s): %w", abs, err)
	}

	db, err := sql.Open(DriverName, readDSN(abs, cfg.busyTimeout))
	if err != nil {
		return nil, fmt.Errorf("store: read-only 열기 (%s): %w", abs, err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: read-only 연결 (%s): %w", abs, err)
	}
	return &ReadOnly{db: db, path: abs}, nil
}

// OpenReadOnlyIfPresent 는 DB 가 **아직 읽을 수 없으면** (nil, nil) 을 돌려준다.
//
// GUI 의 ServiceStartup 이 부를 자리다. nil 은 "아직 데이터가 없다" 이고 호출자는 모든
// 조회에 빈 결과를 돌려주면 된다 — 미설치를 error 로 만들면 앱이 뜨지 않는다.
//
// # 파일이 있다고 읽을 수 있는 것은 아니다
//
// 파일 부재만 보면 **데몬이 DB 를 만드는 중** 인 순간을 놓친다. Open 은 연결을 열어
// 파일을 만든 뒤 마이그레이션을 실행하므로, 그 사이에는 파일이 존재하는데 테이블은 없다.
// 그 순간에 붙은 조회 핸들은 모든 질의가 `no such table` 로 실패하고, 더 나쁘게는
// dashboard.Reader 가 그 핸들을 붙잡은 것으로 보고 다시 붙지 않는다 — GUI 는 앱을 껐다
// 켤 때까지 영구히 빈 화면과 에러 토스트를 반복한다.
//
// GUI 를 먼저 켜고 나중에 `local enable` 로 데몬을 붙이는 것이 정상 시나리오이므로
// (ADR 0004), 이 창은 실제로 밟힌다. 그래서 여기서 스키마 준비 여부까지 보고, 아직이면
// 파일이 없을 때와 **같은 답** 을 준다. 호출자의 재시도(Reopen)가 그대로 답이 된다.
func OpenReadOnlyIfPresent(path string, opts ...Option) (*ReadOnly, error) {
	r, err := OpenReadOnly(path, opts...)
	if errors.Is(err, ErrNoDatabase) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	ready, rerr := r.schemaReadable(context.Background())
	if rerr != nil {
		r.Close() //nolint:errcheck // 이미 실패한 핸들이라 닫기 실패에 할 일이 없다
		return nil, rerr
	}
	if !ready {
		r.Close() //nolint:errcheck // 위와 같다
		return nil, nil
	}
	return r, nil
}

// minReadableSchemaVersion 은 조회가 성립하는 최소 스키마 버전이다.
//
// 마이그레이션 v3 가 도메인 테이블을 전부 지우고 다시 만들었다 (ADR 0009). 그보다 낮은
// DB 에는 지금의 조회가 읽을 테이블이 아예 없으므로, 절반만 마이그레이션된 상태
// (마이그레이션 도중 크래시)도 "아직 읽을 수 없음" 으로 본다.
const minReadableSchemaVersion = 3

// schemaReadable 은 마이그레이션이 조회 가능한 지점까지 진행됐는지 본다.
//
// meta 테이블 존재 여부를 sqlite_master 로 먼저 확인하는 이유는, 없는 테이블을 SELECT
// 하면 드라이버 메시지를 문자열로 판별해야 하기 때문이다. 그 판별은 드라이버가 바뀌면
// 조용히 무너진다.
func (r *ReadOnly) schemaReadable(ctx context.Context) (bool, error) {
	var tables int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'meta'`).Scan(&tables)
	if err != nil {
		return false, fmt.Errorf("store: 스키마 준비 확인 (%s): %w", r.path, err)
	}
	if tables == 0 {
		return false, nil
	}
	v, err := readSchemaVersion(ctx, r.db)
	if err != nil {
		return false, err
	}
	return v >= minReadableSchemaVersion, nil
}

func (r *ReadOnly) Close() error { return r.db.Close() }

// Path 는 열려 있는 DB 파일의 절대 경로다.
func (r *ReadOnly) Path() string { return r.path }

// SQL 은 하위 핸들이다. dashboard 가 조회문을 여기에 건다.
func (r *ReadOnly) SQL() *sql.DB { return r.db }

// SchemaVersion 은 meta.local_schema_version 값이다. 조회 쪽이 자기가 아는 스키마인지
// 확인할 수 있어야 한다 — 데몬이 더 새로운 버전으로 마이그레이션했을 수 있다.
func (r *ReadOnly) SchemaVersion(ctx context.Context) (int, error) {
	return readSchemaVersion(ctx, r.db)
}

// Meta 는 meta 값을 읽는다. 키가 없으면 ok=false.
func (r *ReadOnly) Meta(ctx context.Context, key string) (string, bool, error) {
	return readMeta(ctx, r.db, key)
}

// SetReadLimits 는 조회 커넥션 수 상한을 조정한다. 기본값(무제한)이면 GUI 의 병렬 조회가
// 커넥션을 계속 늘린다.
func (r *ReadOnly) SetReadLimits(maxOpen int, idleLifetime time.Duration) {
	if maxOpen > 0 {
		r.db.SetMaxOpenConns(maxOpen)
	}
	if idleLifetime > 0 {
		r.db.SetConnMaxIdleTime(idleLifetime)
	}
}
