package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
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

// idChunk 는 IN 절 하나에 넣을 세션 id 수다. SQLite 의 기본 변수 상한
// (SQLITE_MAX_VARIABLE_NUMBER)은 999 라 그 아래로 잡는다.
const idChunk = 500

// sessionLastActivity 는 세션의 "마지막으로 알려진 활동" 시각이다.
//
// v3 에는 last_event_at 이 없고 started_at·ended_at 은 **둘 다 선택**이다. 그래서 하나를
// 고르는 것이 아니라 알고 있는 값 중 **가장 늦은 것**을 쓴다. COALESCE 로 우선순위를 매기면
// 400일 전에 시작해 지금도 도는 긴 세션이 started_at 만으로 오래된 것이 되어, 어제 만들어진
// 이벤트까지 함께 사라진다.
//
// 0 은 "모른다" 는 뜻이다. nullSec 이 0 이하의 시각을 전부 NULL 로 눕히므로 저장된 시각은
// 항상 0 보다 크다 — 0 이 실제 값과 부딪힐 일이 없다.
const sessionLastActivity = `MAX(
  COALESCE(sessions.ended_at, 0),
  COALESCE(sessions.started_at, 0),
  COALESCE((SELECT MAX(COALESCE(e.occurred_at, t.ended_at, t.started_at))
              FROM turns t LEFT JOIN events e ON e.turn_id = t.id
             WHERE t.session_id = sessions.id), 0))`

// staleSessionsSQL 은 컷오프보다 오래된 세션을 고른다.
//
// 시각을 하나도 모르는 세션(전부 NULL)은 **대상이 아니다.** 판정할 근거가 없는데 지우면
// 되살릴 방법이 없다. 근거 없이 남는 행이 몇 개 생기는 쪽이 낫다.
const staleSessionsSQL = `SELECT id FROM sessions
WHERE ` + sessionLastActivity + ` > 0
  AND ` + sessionLastActivity + ` < ?`

// pruneSteps 는 삭제 순서다. **순서가 계약이다** (ADR 0009).
//
// v3 의 외래 키는 전부 NO ACTION 이다. CASCADE 가 없으므로 자식에서 부모 순서로
// 애플리케이션이 직접 정리한다:
//
//	file_changes → tool_calls → llm_calls → events → turns → sessions
//
// events 는 반드시 tool_calls · llm_calls 뒤에 온다 — 둘 다 events 를 참조한다.
// 각 문장의 ?는 세션 id 목록 하나뿐이고, CASCADE 가 없으므로 RowsAffected() 가 정확하다.
var pruneSteps = []struct {
	name string
	// query 는 %s 자리에 세션 id 플레이스홀더 목록이 들어간다.
	query string
	dst   func(*PruneResult) *int64
}{
	{"file_changes", `DELETE FROM file_changes WHERE tool_call_id IN (
		SELECT id FROM tool_calls WHERE turn_id IN (
			SELECT id FROM turns WHERE session_id IN (%s)))`,
		func(r *PruneResult) *int64 { return &r.FileChanges }},
	{"tool_calls", `DELETE FROM tool_calls WHERE turn_id IN (
		SELECT id FROM turns WHERE session_id IN (%s))`,
		func(r *PruneResult) *int64 { return &r.ToolCalls }},
	{"llm_calls", `DELETE FROM llm_calls WHERE turn_id IN (
		SELECT id FROM turns WHERE session_id IN (%s))`,
		func(r *PruneResult) *int64 { return &r.LLMCalls }},
	{"events", `DELETE FROM events WHERE turn_id IN (
		SELECT id FROM turns WHERE session_id IN (%s))`,
		func(r *PruneResult) *int64 { return &r.Events }},
	{"turns", `DELETE FROM turns WHERE session_id IN (%s)`,
		func(r *PruneResult) *int64 { return &r.Turns }},
	{"sessions", `DELETE FROM sessions WHERE id IN (%s)`,
		func(r *PruneResult) *int64 { return &r.Sessions }},
}

// pruneVendorsSQL 은 아무 세션도 남지 않은 오래된 벤더만 지운다.
//
// NOT IN 절이 없으면 살아 있는 세션이 참조하는 벤더를 지우려다 외래 키 위반으로 **prune
// 전체가 롤백된다.** vendors.last_seen 은 관측 범위의 끝이라 그 벤더의 마지막 세션보다
// 이를 수 없고, 그래서 "오래된 벤더" 와 "쓰이지 않는 벤더" 는 같은 뜻이 아니다.
const pruneVendorsSQL = `DELETE FROM vendors
WHERE last_seen < ? AND vendor NOT IN (SELECT vendor_id FROM sessions)`

// Prune 은 보존 정책을 적용한다.
//
// 모든 로컬 데이터에 같은 400일 컷오프를 적용한다 (ADR 0008). 경계는 열려 있다 —
// 컷오프와 **정확히 같은** 시각의 행은 남는다.
//
// # 대상을 먼저 확정한다
//
// 삭제할 세션 id 를 한 번 읽어 고정한 뒤 그 목록으로만 지운다. 삭제문 안에서 매번 다시
// 고르면, 이벤트를 지우는 문장이 "마지막 활동" 판정의 입력을 스스로 바꾸는 자기 참조가 된다.
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

	ids, err := staleSessionIDs(ctx, tx, cutoff)
	if err != nil {
		return PruneResult{}, err
	}

	for _, step := range pruneSteps {
		for _, chunk := range idChunks(ids) {
			n, err := exec(ctx, tx, fmt.Sprintf(step.query, placeholders(len(chunk))), chunk...)
			if err != nil {
				return PruneResult{}, fmt.Errorf("store: %s prune: %w", step.name, err)
			}
			*step.dst(&res) += n
		}
	}

	// 벤더는 세션이 다 사라진 뒤에 본다. 순서를 바꾸면 방금 지운 세션의 벤더가 남는다.
	n, err := exec(ctx, tx, pruneVendorsSQL, cutoff)
	if err != nil {
		return PruneResult{}, fmt.Errorf("store: vendors prune: %w", err)
	}
	res.Vendors = n

	if err := tx.Commit(); err != nil {
		return PruneResult{}, fmt.Errorf("store: prune 커밋: %w", err)
	}
	return res, nil
}

// staleSessionIDs 는 지울 세션의 대리 키를 모은다.
func staleSessionIDs(ctx context.Context, tx *sql.Tx, cutoff int64) ([]any, error) {
	rows, err := tx.QueryContext(ctx, staleSessionsSQL, cutoff)
	if err != nil {
		return nil, fmt.Errorf("store: prune 대상 세션 조회: %w", err)
	}
	defer rows.Close()

	var out []any
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("store: prune 대상 세션 읽기: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: prune 대상 세션 조회: %w", err)
	}
	return out, nil
}

// idChunks 는 id 목록을 SQLite 변수 상한 아래로 자른다. 빈 목록이면 조각도 없다 —
// 지울 것이 없을 때 `IN ()` 같은 문장을 만들지 않는다.
func idChunks(ids []any) [][]any {
	var out [][]any
	for start := 0; start < len(ids); start += idChunk {
		out = append(out, ids[start:min(start+idChunk, len(ids))])
	}
	return out
}

// placeholders 는 "?,?,?" 를 만든다.
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
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
