package store

import (
	"context"
	"fmt"

	"github.com/your-org/pulsemetry/internal/event"
)

// closeIdleSessionsSQL 은 마지막 활동이 컷오프보다 오래된 진행 중 세션을 마감한다.
//
// # 왜 SQL 한 문장인가
//
// 조립기의 Advance 는 메모리의 세션 맵만 순회한다. 데몬을 재시작하면 그 맵이 비고 다시
// 채울 경로가 없어, 재시작 전에 돌던 세션은 실제로 끝났어도 영원히 running 으로 남는다
// (PROJ-67). 판정 근거를 sessions.last_activity_at 으로 내리면 조립기가 모르는 세션도
// 마감할 수 있다. 그것이 이 문장이 존재하는 이유 전부다.
//
// # ended_at 에 last_activity_at 을 넣는 이유
//
// 마감을 **감지한** 시각이 아니라 마지막으로 살아 있던 시각이다. 감지 시각을 쓰면 모든
// 세션의 소요 시간에 유휴 임계값이 유령처럼 붙는다 (session.Assembler.close 의 같은 규칙).
//
// # ended_at IS NULL 이 하는 두 가지 일
//
// 대상을 진행 중인 세션으로 좁히는 것이 첫째고, 이미 마감된 세션을 **다시 건드리지 않는
// 것**이 둘째다. 마감 뒤에 낙오 이벤트가 도착해 last_activity_at 이 밀리는 것은 정상
// 경로인데(exporter 배치 지연), 이 조건이 없으면 그때마다 마감 시각이 뒤로 끌려간다.
//
// # NULL 인 행은 대상이 아니다
//
// SQL 에서 `NULL < ?` 는 NULL 이라 매칭되지 않는다. 아래 IS NOT NULL 은 그래서 논리적으로
// 잉여지만 남겨 둔다 — "판정할 근거가 없는 행은 손대지 않는다" 는 것은 의도이고,
// retention.go 의 staleSessionsSQL 이 같은 규칙을 명시적으로 적고 있다.
const closeIdleSessionsSQL = `UPDATE sessions
   SET ended_at = last_activity_at
 WHERE ended_at IS NULL
   AND last_activity_at IS NOT NULL
   AND last_activity_at < ?`

// CloseIdleSessions 는 마지막 활동이 idleBefore 이전인 진행 중 세션을 마감하고 마감한
// 행 수를 돌려준다.
//
// # 컷오프를 인자로 받는 이유
//
// 임계값은 정책이고 이 패키지에는 정책이 없다. session.Assembler.Advance 가 "지금" 을
// 인자로 받는 것과 같은 이유다 — 여기서 시계를 읽으면 경계 테스트가 벽시계에 의존한다.
// 벤더마다 임계값이 갈릴 여지도 호출자에 남는다.
//
// # 호출자가 지켜야 할 것 두 가지
//
// **조립기 스냅샷 저장이 끝난 뒤에 부른다.** upsertSessionSQL 은 ended_at 을 스냅샷 값으로
// 무조건 덮어쓰므로, 스윕이 먼저 돌면 뒤따르는 스냅샷이 방금 찍은 마감을 지운다.
//
// **컷오프는 조립기의 유휴 임계값보다 느슨해야 한다** (더 과거여야 한다). 스윕이 조립기보다
// 공격적으로 닫으면, 조립기는 여전히 running 이라고 믿는 세션을 스윕이 닫고 다음 틱
// 스냅샷이 도로 열어 매 틱 왕복한다.
func (d *DB) CloseIdleSessions(ctx context.Context, idleBefore event.UnixSec) (int, error) {
	res, err := d.db.ExecContext(ctx, closeIdleSessionsSQL, int64(idleBefore))
	if err != nil {
		return 0, fmt.Errorf("store: 유휴 세션 마감: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: 유휴 세션 마감 행 수: %w", err)
	}
	return int(n), nil
}

// ApplyLifecycle 는 명시적인 벤더 훅으로 세션을 열거나 닫는다. 훅은 활동 신호가 아니므로
// last_activity_at 은 건드리지 않는다.
func (d *DB) ApplyLifecycle(ctx context.Context, vendor, sessionID string, at event.UnixSec, end bool) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: lifecycle 트랜잭션 시작: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, `INSERT INTO vendors(vendor,first_seen,last_seen,status) VALUES(?,?,?,'enabled')
ON CONFLICT(vendor) DO UPDATE SET first_seen=MIN(first_seen,excluded.first_seen), last_seen=MAX(last_seen,excluded.last_seen)`, vendor, int64(at), int64(at)); err != nil {
		return fmt.Errorf("store: lifecycle vendor: %w", err)
	}
	var q string
	if end {
		q = `INSERT INTO sessions(vendor_id,session_key,ended_at) VALUES(?,?,?)
ON CONFLICT(vendor_id,session_key) DO UPDATE SET ended_at=COALESCE(sessions.ended_at,excluded.ended_at)`
	} else {
		q = `INSERT INTO sessions(vendor_id,session_key,started_at,ended_at) VALUES(?,?,?,NULL)
ON CONFLICT(vendor_id,session_key) DO UPDATE SET started_at=MIN(COALESCE(sessions.started_at,excluded.started_at),COALESCE(excluded.started_at,sessions.started_at)), ended_at=NULL`
	}
	if _, err := tx.ExecContext(ctx, q, vendor, sessionID, int64(at)); err != nil {
		return fmt.Errorf("store: lifecycle session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: lifecycle commit: %w", err)
	}
	return nil
}
