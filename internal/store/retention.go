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

// PurgeResult 는 purge --content 가 비운 원문 수다.
//
// 합계 하나가 아니라 컬럼별로 나누는 이유는 사용자가 "무엇이 사라졌는가" 를 확인할 수
// 있어야 하기 때문이다. 되돌릴 수 없는 명령이라 "3행 지움" 만으로는 옳게 동작했는지
// 아무도 판정하지 못한다.
type PurgeResult struct {
	// Prompts 는 turns.prompt_text 다 — 사용자가 친 프롬프트 원문.
	Prompts int64
	// Payloads 는 events.payload 다 — 원본 이벤트 JSONB.
	Payloads int64
	// ErrorMessages 는 tool_calls.error_message 다 — 경로·명령이 섞여 나오는 벤더 오류 문자열.
	ErrorMessages int64
}

// Total 은 비운 행의 총합이다.
func (r PurgeResult) Total() int64 { return r.Prompts + r.Payloads + r.ErrorMessages }

// purgeStep 은 원문이 담기는 컬럼 하나다.
//
// UPDATE 문과 계수 SELECT 를 같은 조각에서 만든다. 두 곳에 조건을 따로 쓰면 "지우겠다고
// 말한 수" 와 "실제로 지운 수" 가 조용히 갈린다.
type purgeStep struct {
	name   string
	table  string
	column string
	// scope 는 --before 가 붙었을 때 더할 조건이다. 시각 컬럼이 NULL 이면 소속 턴의 시각으로
	// 판정하고, 그것마저 없으면 조건이 NULL 이라 대상에서 빠진다 — 구간 삭제가 자기 구간을
	// 넘지 않게 하는 쪽이 맞다. 전체 삭제(--before 없음)는 어차피 다 지운다.
	scope string
	dst   func(*PurgeResult) *int64
}

// where 는 이 컬럼의 대상 조건이다.
//
// IS NOT NULL 이 계수의 핵심이다. 이미 비어 있는 행까지 세면 사용자에게 보고되는 수가
// 부풀고, 두 번 돌렸을 때 "또 지웠다" 고 말하게 된다.
func (s purgeStep) where(scoped bool) string {
	w := ` WHERE "` + s.column + `" IS NOT NULL`
	if scoped {
		w += ` AND ` + s.scope
	}
	return w
}

func (s purgeStep) update(scoped bool) string {
	return `UPDATE ` + s.table + ` SET "` + s.column + `" = NULL` + s.where(scoped)
}

func (s purgeStep) count(scoped bool) string {
	return `SELECT COUNT(*) FROM ` + s.table + s.where(scoped)
}

// purgeSteps 는 v3 에서 원문이 남는 자리 **전부** 다.
//
// v1 의 event_content 처럼 통째로 지울 테이블이 없다. 원문은 세 컬럼에 흩어져 있고
// 나머지 컬럼(수치·모델·도구 이름·오류 타입)은 원문이 아니므로 남는다 — 행을 지우면
// 집계가 함께 사라진다. 그래서 DELETE 가 아니라 UPDATE ... SET NULL 이다.
var purgeSteps = []purgeStep{
	{
		name: "프롬프트", table: "turns", column: "prompt_text",
		scope: `COALESCE(started_at, ended_at) < ?`,
		dst:   func(r *PurgeResult) *int64 { return &r.Prompts },
	},
	{
		name: "이벤트 payload", table: "events", column: "payload",
		scope: `COALESCE(occurred_at,
		  (SELECT COALESCE(t.started_at, t.ended_at) FROM turns t WHERE t.id = events.turn_id)) < ?`,
		dst: func(r *PurgeResult) *int64 { return &r.Payloads },
	},
	{
		name: "도구 오류 메시지", table: "tool_calls", column: "error_message",
		scope: `COALESCE(called_at,
		  (SELECT COALESCE(t.started_at, t.ended_at) FROM turns t WHERE t.id = tool_calls.turn_id)) < ?`,
		dst: func(r *PurgeResult) *int64 { return &r.ErrorMessages },
	},
}

// PurgeContent 는 원문만 지운다 (telemetryctl purge --content [--before]).
//
// v3 에서 원문이 남는 자리는 turns.prompt_text · events.payload · tool_calls.error_message
// 세 곳이다. 행을 지우지 않고 컬럼만 NULL 로 만든다 — 세션·턴·이벤트 행과 수치는 그대로라
// 집계는 변하지 않고 원문 검색만 불가능해진다.
//
// 세 문장은 **한 트랜잭션**이다. 하나만 성공하고 끝나면 사용자는 "지웠다" 는 보고를 받고도
// 다른 컬럼에 원문이 남은 DB 를 갖게 된다.
//
// before 가 제로값이면 전부 지운다.
func (d *DB) PurgeContent(ctx context.Context, before time.Time) (PurgeResult, error) {
	var res PurgeResult
	scoped, args := purgeScope(before)

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return res, fmt.Errorf("store: purge 트랜잭션 시작: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // 커밋 후에는 ErrTxDone 이라 무시한다

	for _, step := range purgeSteps {
		n, err := exec(ctx, tx, step.update(scoped), args...)
		if err != nil {
			return PurgeResult{}, fmt.Errorf("store: %s purge: %w", step.name, err)
		}
		*step.dst(&res) = n
	}

	if err := tx.Commit(); err != nil {
		return PurgeResult{}, fmt.Errorf("store: purge 커밋: %w", err)
	}
	return res, nil
}

// ContentCounts 는 같은 조건으로 **지워질** 행을 미리 센다. purge 가 지우기 전에 무엇이
// 사라지는지 말할 수 있어야 한다.
func (d *DB) ContentCounts(ctx context.Context, before time.Time) (PurgeResult, error) {
	return contentCounts(ctx, d.db, before)
}

// ContentCounts 는 read-only 핸들용이다. 쓰기 핸들을 열기 전에 현황을 읽는 경로가 쓴다.
func (r *ReadOnly) ContentCounts(ctx context.Context, before time.Time) (PurgeResult, error) {
	return contentCounts(ctx, r.db, before)
}

func contentCounts(ctx context.Context, q rowQuerier, before time.Time) (PurgeResult, error) {
	var res PurgeResult
	scoped, args := purgeScope(before)
	for _, step := range purgeSteps {
		var n int64
		if err := q.QueryRowContext(ctx, step.count(scoped), args...).Scan(&n); err != nil {
			return PurgeResult{}, fmt.Errorf("store: %s 계수: %w", step.name, err)
		}
		*step.dst(&res) = n
	}
	return res, nil
}

// purgeScope 는 --before 경계를 SQL 인자로 옮긴다. 제로값이면 구간 제한이 없다.
func purgeScope(before time.Time) (bool, []any) {
	if before.IsZero() {
		return false, nil
	}
	return true, []any{int64(event.SecFromTime(before))}
}

func exec(ctx context.Context, tx *sql.Tx, query string, args ...any) (int64, error) {
	out, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return out.RowsAffected()
}
