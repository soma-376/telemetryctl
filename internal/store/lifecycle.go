package store

import (
	"context"
	"fmt"

	"github.com/your-org/pulsemetry/internal/event"
)

// closeIdleSessionsSQL 은 마지막 활동이 컷오프보다 오래된 진행 중 세션을 마감한다.
// 조립기의 Advance 가 메모리 맵만 보는 것과 달리, 재시작으로 조립기가 잊은 세션도 닫는다.
//
// ended_at 에 넣는 값은 감지 시각이 아니라 last_activity_at 이다 — 감지 시각을 쓰면 모든
// 세션의 소요 시간에 유휴 임계값이 붙는다 (session.Assembler.close 의 같은 규칙).
//
// ended_at IS NULL 은 이미 마감된 세션을 지킨다. 마감 뒤 낙오 이벤트가 활동 시각을 미는
// 것은 정상 경로인데, 이 조건이 없으면 그때마다 마감 시각이 뒤로 끌려간다.
const closeIdleSessionsSQL = `UPDATE sessions
   SET ended_at = last_activity_at
 WHERE ended_at IS NULL
   AND last_activity_at IS NOT NULL
   AND last_activity_at < ?`

// CloseIdleSessions 는 마지막 활동이 idleBefore 이전인 진행 중 세션을 마감하고 마감한
// 행 수를 돌려준다. 컷오프를 인자로 받는 것은 임계값이 정책이기 때문이다.
//
// 호출자가 지킬 것 둘.
//
//   - 조립기 스냅샷 저장 뒤에 부른다. upsertSessionSQL 이 ended_at 을 무조건 덮으므로
//     먼저 돌면 뒤따르는 스냅샷이 마감을 지운다.
//   - 컷오프는 조립기 유휴 임계값보다 느슨해야 한다. 더 공격적이면 조립기가 running 이라
//     믿는 세션을 닫고 다음 스냅샷이 도로 열어 매 틱 왕복한다.
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
