package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/your-org/pulsemetry/internal/event"
)

// PruneResult 는 보존 정책이 지운 행 수다. 어느 계층이 얼마나 사라졌는지 로그에 남길 수
// 있어야 화면의 공백을 나중에 설명할 수 있다.
type PruneResult struct {
	// 이벤트 계층 (기본 30일)
	Events       int64
	EventContent int64
	ToolEvents   int64
	// 세션 계층 (400일)
	Sessions     int64
	RollupHourly int64
	Vendors      int64
	// SessionFiles·MCPUsage 는 sessions 삭제에 딸려 CASCADE 로 지워진 수다.
	SessionFiles int64
	MCPUsage     int64
}

// Total 은 지운 행의 총합이다.
func (r PruneResult) Total() int64 {
	return r.Events + r.EventContent + r.ToolEvents +
		r.Sessions + r.RollupHourly + r.Vendors + r.SessionFiles + r.MCPUsage
}

// Prune 은 보존 정책을 적용한다 (계획서 「보존」).
//
// # 점진적 저하
//
// 두 계층의 컷오프가 다르고, 이벤트 계층을 지울 때 **세션 행을 건드리지 않는다**.
// 그래서 30일이 지나면 tool_events(툴 타임라인)와 event_content(원문)만 사라지고
// sessions 의 수치·session_files 의 파일 목록은 400일까지 남는다. Activity 화면의 세션은
// 계속 보이고 그 안의 타임라인만 비는 것이 의도된 동작이다.
//
// CASCADE 와 방향이 반대라는 점이 중요하다. sessions → tool_events 로만 CASCADE 가 걸려
// 있고 그 역방향은 없다. 즉 세션을 지우면 타임라인이 따라 지워지지만, 타임라인을 지워도
// 세션은 남는다. 이벤트 계층 삭제가 tool_events 를 직접 조준하는 것도 그래서다 —
// sessions 를 건드리는 순간 파일 목록까지 함께 사라진다.
//
// # 실패 처리
//
// 실패는 치명적이지 않다 (계획서 리스크 표: Windows 에서 GUI 가 파일을 연 채 prune).
// 트랜잭션 하나라 실패하면 아무것도 지워지지 않고, 호출자는 로깅 후 다음 틱에 다시 부르면
// 된다. 부분 삭제 상태가 남지 않으므로 재시도가 안전하다.
func (d *DB) Prune(ctx context.Context, now time.Time) (PruneResult, error) {
	p := d.cfg.retention.normalized()
	eventCutoff := event.SecFromTime(now.Add(-time.Duration(p.EventDays) * 24 * time.Hour))
	sessionCutoff := event.SecFromTime(now.Add(-time.Duration(p.SessionDays) * 24 * time.Hour))

	var res PruneResult
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return res, fmt.Errorf("store: prune 트랜잭션 시작: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // 커밋 후에는 ErrTxDone 이라 무시한다

	// ── 이벤트 계층 ──────────────────────────────────────────────────────────
	// event_content 를 먼저 지운다. events 를 지우면 CASCADE 로 따라 지워지지만, 그때
	// AFTER DELETE 트리거가 도는지는 recursive_triggers 설정에 달려 있다. 명시적으로 먼저
	// 지우면 content_fts 동기화가 PRAGMA 설정과 무관하게 보장된다 — 색인에 남은 유령 행은
	// 검색 결과에 본문 없는 히트로 나타나고 원문 삭제 약속도 깨진다.
	n, err := exec(ctx, tx,
		`DELETE FROM event_content WHERE event_id IN (SELECT id FROM events WHERE hour <= ? AND ts < ?)`,
		int64(event.HourOf(eventCutoff.Nano())), int64(eventCutoff.Nano()))
	if err != nil {
		return PruneResult{}, fmt.Errorf("store: event_content prune: %w", err)
	}
	res.EventContent = n

	// hour <= ? 는 idx_events_hour 를 태우기 위한 조건이고 ts < ? 가 실제 경계다.
	// hour 는 ts 를 내림한 값이라 두 조건의 결과 집합은 ts < ? 하나와 정확히 같다.
	n, err = exec(ctx, tx, `DELETE FROM events WHERE hour <= ? AND ts < ?`,
		int64(event.HourOf(eventCutoff.Nano())), int64(eventCutoff.Nano()))
	if err != nil {
		return PruneResult{}, fmt.Errorf("store: events prune: %w", err)
	}
	res.Events = n

	// sessions 는 건드리지 않는다. 타임라인만 사라지고 세션은 남는다.
	n, err = exec(ctx, tx, `DELETE FROM tool_events WHERE ts < ?`, int64(eventCutoff))
	if err != nil {
		return PruneResult{}, fmt.Errorf("store: tool_events prune: %w", err)
	}
	res.ToolEvents = n

	// ── 세션 계층 ────────────────────────────────────────────────────────────
	// 기준은 last_event_at 이다. started_at 으로 재면 오래 이어진 세션이 아직 살아 있는데도
	// 시작 시각만으로 잘린다.
	res.SessionFiles, err = countScoped(ctx, tx,
		`SELECT COUNT(*) FROM session_files WHERE session_id IN (SELECT session_id FROM sessions WHERE last_event_at < ?)`,
		int64(sessionCutoff))
	if err != nil {
		return PruneResult{}, err
	}
	res.MCPUsage, err = countScoped(ctx, tx,
		`SELECT COUNT(*) FROM mcp_session_usage WHERE session_id IN (SELECT session_id FROM sessions WHERE last_event_at < ?)`,
		int64(sessionCutoff))
	if err != nil {
		return PruneResult{}, err
	}
	n, err = exec(ctx, tx, `DELETE FROM sessions WHERE last_event_at < ?`, int64(sessionCutoff))
	if err != nil {
		return PruneResult{}, fmt.Errorf("store: sessions prune: %w", err)
	}
	res.Sessions = n

	n, err = exec(ctx, tx, `DELETE FROM rollup_hourly WHERE hour < ?`, int64(sessionCutoff))
	if err != nil {
		return PruneResult{}, fmt.Errorf("store: rollup_hourly prune: %w", err)
	}
	res.RollupHourly = n

	n, err = exec(ctx, tx, `DELETE FROM vendors WHERE last_seen < ?`, int64(sessionCutoff))
	if err != nil {
		return PruneResult{}, fmt.Errorf("store: vendors prune: %w", err)
	}
	res.Vendors = n

	if err := tx.Commit(); err != nil {
		return PruneResult{}, fmt.Errorf("store: prune 커밋: %w", err)
	}
	return res, nil
}

// PurgeContent 는 원문만 지운다 (telemetryctl purge --content [--before]).
//
// before 가 제로값이면 전부 지운다. events 행과 그 길이·바이트 수 컬럼은 남으므로 수치와
// 롤업은 그대로이고 검색만 불가능해진다 — 보존 정책의 점진적 저하와 같은 성질이다.
func (d *DB) PurgeContent(ctx context.Context, before time.Time) (int64, error) {
	var (
		query string
		args  []any
	)
	if before.IsZero() {
		query = `DELETE FROM event_content`
	} else {
		cutoff := event.NanoFromTime(before)
		query = `DELETE FROM event_content WHERE event_id IN (SELECT id FROM events WHERE hour <= ? AND ts < ?)`
		args = []any{int64(event.HourOf(cutoff)), int64(cutoff)}
	}

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: purge 트랜잭션 시작: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // 커밋 후에는 ErrTxDone 이라 무시한다

	n, err := exec(ctx, tx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("store: event_content purge: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: purge 커밋: %w", err)
	}
	return n, nil
}

func exec(ctx context.Context, tx *sql.Tx, query string, args ...any) (int64, error) {
	out, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return out.RowsAffected()
}

// countScoped 는 CASCADE 로 사라질 행을 미리 센다. SQLite 의 RowsAffected 는 CASCADE 로
// 지워진 행을 포함하지 않아서, 세지 않으면 PruneResult 가 실제 삭제량을 축소해 보고한다.
func countScoped(ctx context.Context, tx *sql.Tx, query string, args ...any) (int64, error) {
	var n int64
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: prune 대상 계수: %w", err)
	}
	return n, nil
}
