// Package dashboard 는 GUI 와 CLI 가 로컬 SQLite 를 읽는 조회 API 다 (ADR 0004).
//
// 계획서 「화면 → 쿼리 대응」 표의 각 행이 여기 메서드 하나로 대응한다. 쓰기는 데몬만 하고
// 이 패키지는 read-only 연결(mode=ro & busy_timeout(5000))로만 연다 — store.OpenReadOnly 가
// "어떻게 여는가" 를 소유하고 여기서는 "무엇을 묻는가" 만 소유한다.
//
// # Wails 를 import 하지 않는다
//
// 이 패키지는 순수 Go 다. Wails v3 서비스가 gui/ 에서 이 타입들을 감싸고 바인딩을 생성한다
// (ADR 0004). 그 결과 두 가지 제약이 여기에 걸린다.
//
//   - **모든 공개 구조체 필드에 json 태그를 붙인다.** 태그가 곧 TS 필드명이라 필드 이름을
//     바꾸면 프런트엔드가 조용히 undefined 를 읽는다. 규약은 snake_case 다.
//   - **에러 메시지가 사용자에게 보인다.** Go 의 error 는 Promise reject 로 전파된다.
//     그래서 어떤 에러에도 SQL 문장을 싣지 않는다 (queryErr).
//
// # 미설치는 에러가 아니다
//
// ServiceStartup 이 error 를 반환하면 앱 기동이 통째로 중단된다. 그런데 DB 가 없는 상태
// (미설치·local enable 전·데몬 첫 실행 전)는 정상이다. 그래서 Open 은 DB 부재를 에러로
// 만들지 않고 "비어 있는 Reader" 를 돌려주며, 모든 조회가 빈 결과를 준다 (Available 이 false).
// store.OpenReadOnlyIfPresent 와 store.ErrNoDatabase 가 이를 위해 준비된 것이다.
//
// # 단위
//
// 밖으로 나가는 시각은 전부 UTC unix **초** 다. events.ts 만 나노초라 그 값은 이 패키지를
// 지나가지 않는다. 시간대는 조회 인자로만 받고 저장값을 바꾸지 않는다 (timezone.go).
package dashboard

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	// tzdata 가 없는 환경에서도 time.LoadLocation 이 동작하게 한다. Windows 와 최소 컨테이너에는
	// 시스템 tz 데이터베이스가 없어서 Today("Asia/Seoul") 이 "unknown time zone" 으로 실패한다 —
	// 시간대가 이 패키지의 핵심 인자라 그 실패는 기능 전체를 못 쓰게 만든다.
	// 표준 라이브러리라 go.mod 는 바뀌지 않고, 대가는 바이너리 수백 KB 다.
	_ "time/tzdata"

	"github.com/your-org/pulsemetry/internal/store"
)

const (
	// maxReadConns 는 조회 커넥션 상한이다. 기본값(무제한)이면 GUI 의 병렬 조회가 커넥션을
	// 계속 늘린다. WAL 이라 읽기는 서로 막지 않으므로 몇 개면 충분하다.
	maxReadConns = 4
	// readIdleTimeout 은 유휴 커넥션을 닫는 시간이다. 화면을 오래 안 보는 동안 파일 핸들을
	// 붙잡고 있으면 Windows 에서 데몬의 prune 이 막힌다 (계획서 리스크 표).
	readIdleTimeout = 30 * time.Second
)

// Reader 는 로컬 데이터의 read-only 조회 핸들이다. 동시 사용에 안전하다.
type Reader struct {
	mu sync.RWMutex
	// ro 가 nil 이면 DB 파일이 아직 없다는 뜻이다. 에러가 아니라 "빈 결과" 상태다.
	ro *store.ReadOnly

	path    string
	dataDir string

	// now 는 "오늘" 과 신선도 판정의 기준 시각이다. 필드로 둔 이유는 테스트가 벽시계에
	// 의존하면 시간대 경계 테스트가 하루 중 언제 도느냐에 따라 흔들리기 때문이다.
	now func() time.Time
}

// Open 은 dbPath 의 로컬 DB 를 read-only 로 연다 (계획서 「GUI 연동 형태」).
//
// DB 파일이 없어도 에러가 아니다 — Reader 는 열리고 모든 조회가 빈 결과를 돌려준다.
// 나중에 데몬이 DB 를 만들면 Reopen 으로 다시 시도할 수 있다.
func Open(dbPath string) (*Reader, error) {
	if strings.TrimSpace(dbPath) == "" {
		return nil, errors.New("dashboard: 데이터베이스 경로가 비어 있음")
	}
	abs, err := filepath.Abs(dbPath)
	if err != nil {
		return nil, fmt.Errorf("dashboard: 데이터베이스 경로 해석 실패 (%s)", dbPath)
	}
	r := &Reader{path: abs, dataDir: filepath.Dir(abs), now: time.Now}
	if err := r.attach(); err != nil {
		return nil, err
	}
	return r, nil
}

// Reopen 은 아직 열지 못한 DB 를 다시 열어 본다.
//
// GUI 가 먼저 뜨고 나중에 `telemetryctl local enable` 로 데몬이 DB 를 만드는 순서가 정상
// 시나리오다. 이 메서드가 없으면 그 사용자는 앱을 껐다 켜야 데이터를 본다.
// 이미 열려 있으면 아무것도 하지 않는다.
func (r *Reader) Reopen() error {
	r.mu.RLock()
	open := r.ro != nil
	r.mu.RUnlock()
	if open {
		return nil
	}
	return r.attach()
}

func (r *Reader) attach() error {
	ro, err := store.OpenReadOnlyIfPresent(r.path)
	if err != nil {
		// store 의 메시지는 경로만 담는다. SQL 은 들어가지 않는다.
		return fmt.Errorf("dashboard: 로컬 데이터 열기 실패: %w", err)
	}
	if ro == nil {
		return nil // 미설치. 에러가 아니다.
	}
	ro.SetReadLimits(maxReadConns, readIdleTimeout)

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ro != nil {
		// 다른 고루틴이 먼저 붙였다. 방금 연 것은 버린다.
		ro.Close() //nolint:errcheck // 버리는 핸들이라 실패해도 할 일이 없다
		return nil
	}
	r.ro = ro
	return nil
}

// Close 는 조회 핸들을 닫는다. 열린 적이 없어도 안전하다 (ServiceShutdown).
func (r *Reader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ro == nil {
		return nil
	}
	err := r.ro.Close()
	r.ro = nil
	return err
}

// Available 은 DB 파일이 실제로 열려 있는지 알려준다.
// false 면 모든 조회가 빈 결과를 돌려준다 — 화면은 "아직 데이터가 없습니다" 를 그리면 된다.
func (r *Reader) Available() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.ro != nil
}

// Path 는 조회 대상 DB 파일의 절대 경로다. 열리지 않았어도 값이 있다.
func (r *Reader) Path() string { return r.path }

// DataDir 는 DB 가 들어 있는 데이터 디렉터리다. runtime.json 도 여기 있다.
func (r *Reader) DataDir() string { return r.dataDir }

// db 는 조회에 쓸 핸들이다. ok=false 면 DB 가 없다.
func (r *Reader) db() (*sql.DB, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.ro == nil {
		return nil, false
	}
	return r.ro.SQL(), true
}

// sqlQuerier 는 조회 함수가 필요로 하는 최소 인터페이스다. *sql.DB 를 그대로 받지 않는
// 이유는 이 패키지가 쓰기를 할 수 없다는 것을 타입으로 못박기 위해서다 — Exec 가 없으면
// read-only 연결에서 실패할 문장을 실수로 넣을 방법도 없다.
type sqlQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// queryErr 는 조회 실패를 화면에 띄워도 되는 형태로 감싼다.
//
// Wails 가 Go 의 error 를 Promise reject 로 전파하므로 이 문자열이 그대로 사용자에게 보인다.
// op 는 사람이 읽는 동작 이름이고, **SQL 문장을 여기에 넣지 않는다** — 질의문은 사용자에게
// 아무 도움이 되지 않으면서 내부 스키마만 드러낸다. 원인 에러는 감싸 두되(드라이버 메시지는
// 문장을 담지 않는다) 호출자가 질의 상수를 포맷 인자로 넘기는 일은 없어야 한다.
func queryErr(op string, err error) error {
	return fmt.Errorf("dashboard: %s 실패: %w", op, err)
}

// ── 스캔 보조 ────────────────────────────────────────────────────────────────

// nullInt64 는 NULL 이 "없음" 을 뜻하는 정수 컬럼(sessions.ended_at, tool_events.duration_ms)을
// 포인터로 옮긴다. 0 으로 눕히면 1970 년에 끝난 세션과 진행 중인 세션이 같아지고, 0ms 만에
// 끝난 툴 호출과 소요 시간을 모르는 호출이 같아진다. JSON 에서는 null 이 된다.
func nullInt64(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

// nullBool 은 tool_events.success 처럼 "미상" 이 실패와 다른 컬럼을 옮긴다.
func nullBool(n sql.NullInt64) *bool {
	if !n.Valid {
		return nil
	}
	v := n.Int64 != 0
	return &v
}

// closeRows 는 조회 루프의 마무리다. rows.Err() 를 빠뜨리면 중간에 끊긴 결과가 정상 종료로
// 보여 화면이 조용히 일부 데이터만 그린다.
func closeRows(rows *sql.Rows, op string, err *error) {
	if cerr := rows.Close(); cerr != nil && *err == nil {
		*err = queryErr(op, cerr)
	}
	if rerr := rows.Err(); rerr != nil && *err == nil {
		*err = queryErr(op, rerr)
	}
}

// clampLimit 는 조회 상한을 정한다. 0 이하는 기본값, 상한 초과는 상한으로 자른다 —
// GUI 가 실수로 0 이나 100000 을 넘겨 화면 전체가 멈추는 일을 여기서 막는다.
func clampLimit(v, def, max int) int {
	switch {
	case v <= 0:
		return def
	case v > max:
		return max
	default:
		return v
	}
}

// placeholders 는 IN (?,?,?) 자리표시자를 만든다. 값은 전부 바인딩되므로 문자열 결합이
// 질의에 닿는 것은 이 자리표시자뿐이다.
func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
