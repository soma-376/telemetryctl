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
	FileChanges int64
	ToolCalls   int64
	LLMCalls    int64
	Events      int64
	Turns       int64
	Sessions    int64
	Vendors     int64
}

// Total 은 지운 행의 총합이다.
func (r PruneResult) Total() int64 {
	return r.FileChanges + r.ToolCalls + r.LLMCalls + r.Events + r.Turns + r.Sessions + r.Vendors
}

// staleSessions 는 컷오프보다 오래된 세션을 고른다.
//
// 기준은 "마지막으로 알려진 활동" 이다 — 마감된 세션은 ended_at, 진행 중이면 started_at.
// 시각이 아예 없는 세션은 판정할 근거가 없으므로 대상이 아니다. 근거 없이 지우는 것보다
// 남기는 쪽이 낫다 (되살릴 방법이 없다).
const staleSessions = `SELECT id FROM sessions
WHERE COALESCE(ended_at, started_at) IS NOT NULL
  AND COALESCE(ended_at, started_at) < ?`

const staleTurns = `SELECT id FROM turns WHERE session_id IN (` + staleSessions + `)`

// Prune 은 보존 정책을 적용한다.
//
// 모든 로컬 데이터에 같은 400일 컷오프를 적용한다 (ADR 0008).
//
// # 순서가 계약이다
//
// v3 의 외래 키는 전부 NO ACTION 이다. CASCADE 가 없으므로 **자식에서 부모 순서로**
// 애플리케이션이 직접 정리한다 (ADR 0009):
//
//	file_changes → tool_calls → llm_calls → events → turns → sessions → vendors
//
// events 는 반드시 tool_calls · llm_calls 뒤에 온다 — 둘 다 events 를 참조한다.
// vendors 는 아직 세션이 남아 있는 벤더를 건드리지 않도록 보호한다.
//
// # 실패 처리
//
// 실패는 치명적이지 않다 (Windows 에서 GUI 가 파일을 연 채 prune). 트랜잭션 하나라 실패하면
// 아무것도 바뀌지 않고, 호출자는 로깅 후 다음 틱에 다시 부르면 된다.
func (d *DB) Prune(ctx context.Context, now time.Time) (PruneResult, error) {
	cutoff := int64(event.SecFromTime(now.Add(-DefaultRetentionDays * 24 * time.Hour)))

	var res PruneResult
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return res, fmt.Errorf("store: prune 트랜잭션 시작: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // 커밋 후에는 ErrTxDone 이라 무시한다

	steps := []struct {
		name  string
		query string
		dst   *int64
	}{
		{"file_changes", `DELETE FROM file_changes WHERE tool_call_id IN (
			SELECT id FROM tool_calls WHERE turn_id IN (` + staleTurns + `))`, &res.FileChanges},
		{"tool_calls", `DELETE FROM tool_calls WHERE turn_id IN (` + staleTurns + `)`, &res.ToolCalls},
		{"llm_calls", `DELETE FROM llm_calls WHERE turn_id IN (` + staleTurns + `)`, &res.LLMCalls},
		{"events", `DELETE FROM events WHERE turn_id IN (` + staleTurns + `)`, &res.Events},
		{"turns", `DELETE FROM turns WHERE session_id IN (` + staleSessions + `)`, &res.Turns},
		{"sessions", `DELETE FROM sessions WHERE id IN (` + staleSessions + `)`, &res.Sessions},
		{"vendors", `DELETE FROM vendors
			WHERE last_seen < ? AND vendor NOT IN (SELECT vendor_id FROM sessions)`, &res.Vendors},
	}
	for _, s := range steps {
		n, err := exec(ctx, tx, s.query, cutoff)
		if err != nil {
			return PruneResult{}, fmt.Errorf("store: %s prune: %w", s.name, err)
		}
		*s.dst = n
	}

	if err := tx.Commit(); err != nil {
		return PruneResult{}, fmt.Errorf("store: prune 커밋: %w", err)
	}
	return res, nil
}

// PurgeContent 는 원문만 지운다 (telemetryctl purge --content [--before]).
//
// v3 에서 원문이 남는 자리는 turns.prompt_text 하나뿐이다 — 응답·tool_input·tool_result 는
// 저장될 컬럼 자체가 없다. 턴·이벤트 행과 수치는 그대로이므로 집계는 변하지 않고 원문 검색만
// 불가능해진다.
//
// before 가 제로값이면 전부 지운다.
func (d *DB) PurgeContent(ctx context.Context, before time.Time) (int64, error) {
	query := `UPDATE turns SET prompt_text = NULL WHERE prompt_text IS NOT NULL`
	var args []any
	if !before.IsZero() {
		query += ` AND started_at IS NOT NULL AND started_at < ?`
		args = []any{int64(event.SecFromTime(before))}
	}

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: purge 트랜잭션 시작: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // 커밋 후에는 ErrTxDone 이라 무시한다

	n, err := exec(ctx, tx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("store: prompt_text purge: %w", err)
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
