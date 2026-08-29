package store

import (
	"fmt"
	"strings"

	"github.com/your-org/pulsemetry/internal/event"
)

// 원본 이벤트에서 llm_calls · tool_calls · file_changes 로의 승격.
//
// # 컬럼 하나에는 출처가 하나뿐이다
//
// (이 경고는 삭제된 internal/rollup/mapping.go 에서 옮겨 왔다. 롤업 테이블은 사라졌지만
// 규칙이 지키던 위험은 그대로다.)
//
//   - cost_usd 와 토큰 4종은 **로그 이벤트에서만** 받는다. claude_code.cost.usage 와
//     claude_code.token.usage 메트릭에도 같은 값이 실려 오지만, 둘 다 승격하면 정확히
//     2배가 된다 (계획서 리스크 표 "비용 10배").
//   - 그래서 llm_calls 의 출처는 claude_code.api_request 로그와 Codex 의
//     codex.sse_event(kind=response.completed) 뿐이다. 메트릭은 어떤 경우에도 승격하지 않는다.
//
// 조회 시점 집계가 llm_calls 를 SUM 하므로, 여기서 한 번 두 배가 되면 화면의 모든 비용이
// 두 배가 된다. 되짚을 근거는 남지 않는다.

// eventSuffix 는 벤더 접두를 뗀 이벤트 이름이다. session/kind.go 와 같은 규칙이다 —
// claude_code.tool_result 와 codex.tool_result 가 같은 승격 규칙을 타야 벤더가 늘어도
// 이 파일을 고치지 않는다.
func eventSuffix(name string) string {
	if i := strings.IndexByte(name, '.'); i >= 0 {
		return name[i+1:]
	}
	return name
}

// isLLMCall 은 llm_calls 로 승격할 이벤트인지 본다.
func isLLMCall(e event.Event) bool {
	if e.Signal != event.SignalLog {
		// 메트릭은 절대 승격하지 않는다. 위 주석의 이유다.
		return false
	}
	switch eventSuffix(e.Name) {
	case "api_request":
		return true
	case "sse_event":
		// Codex 는 스트리밍 이벤트를 한 이름으로 보내고 종류를 kind 속성에 담는다.
		// 완료 이벤트 하나만 호출 한 건이다 — 나머지 델타까지 세면 호출 수가 폭발한다.
		return e.Attr.Type == "response.completed"
	}
	return false
}

// toolRole 은 도구 이벤트가 결정 쪽인지 결과 쪽인지다.
type toolRole uint8

const (
	toolRoleNone toolRole = iota
	toolRoleDecision
	toolRoleResult
)

func toolRoleOf(e event.Event) toolRole {
	switch eventSuffix(e.Name) {
	case "tool_decision":
		return toolRoleDecision
	case "tool_result":
		return toolRoleResult
	}
	return toolRoleNone
}

const insertLLMCallSQL = `INSERT INTO llm_calls (
  turn_id, source_event_id, called_at, model,
  input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, reasoning_tokens,
  cost_usd, duration_ms, request_id
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`

// upsertToolCallSQL 은 결정 이벤트와 결과 이벤트를 call_key 하나로 합친다.
//
// 두 이벤트는 순서가 정해져 있지 않다 — 보통 결정이 먼저지만 배치가 섞이면 결과가 먼저
// 도착한다. 그래서 컬럼마다 COALESCE 로 "이미 있는 값을 지우지 않는다" 를 지킨다.
//
// turn_id 만 예외다. 스키마 문서가 "결과 이벤트의 턴, 결과가 없으면 결정 이벤트의 턴" 이라고
// 정했으므로 결과가 들어올 때만 덮어쓴다.
//
// success 는 excluded 우선이다. 결과 이벤트가 성공 여부를 들고 오고, 결정만 있는 동안은
// NULL 이어야 한다 — 미상을 0(실패)으로 눕히면 화면의 실패율이 조용히 부푼다.
const upsertToolCallSQL = `INSERT INTO tool_calls (
  turn_id, call_key, decision_event_id, result_event_id,
  tool_name, target, mcp_server, called_at, duration_ms, blocked_on_user_ms,
  success, decision, decision_source, input_size_bytes, result_size_bytes,
  error_type, error_message
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(call_key) DO UPDATE SET
  turn_id = CASE WHEN excluded.result_event_id IS NOT NULL
                 THEN excluded.turn_id ELSE tool_calls.turn_id END,
  decision_event_id  = COALESCE(tool_calls.decision_event_id, excluded.decision_event_id),
  result_event_id    = COALESCE(tool_calls.result_event_id,   excluded.result_event_id),
  tool_name          = COALESCE(tool_calls.tool_name,  excluded.tool_name),
  target             = COALESCE(tool_calls.target,     excluded.target),
  mcp_server         = COALESCE(tool_calls.mcp_server, excluded.mcp_server),
  called_at          = MIN(COALESCE(excluded.called_at, tool_calls.called_at),
                           COALESCE(tool_calls.called_at, excluded.called_at)),
  duration_ms        = COALESCE(excluded.duration_ms,        tool_calls.duration_ms),
  blocked_on_user_ms = COALESCE(excluded.blocked_on_user_ms, tool_calls.blocked_on_user_ms),
  success            = COALESCE(excluded.success,            tool_calls.success),
  decision           = COALESCE(tool_calls.decision,        excluded.decision),
  decision_source    = COALESCE(tool_calls.decision_source, excluded.decision_source),
  input_size_bytes   = COALESCE(excluded.input_size_bytes,  tool_calls.input_size_bytes),
  result_size_bytes  = COALESCE(excluded.result_size_bytes, tool_calls.result_size_bytes),
  error_type         = COALESCE(excluded.error_type,    tool_calls.error_type),
  error_message      = COALESCE(excluded.error_message, tool_calls.error_message)
RETURNING id`

const insertFileChangeSQL = `INSERT INTO file_changes (
  tool_call_id, file_path, operation, renamed_from, additions, deletions, old_hash, new_hash
) VALUES (?,?,?,?,?,?,?,?)`

// promote 는 **이번 트랜잭션이 실제로 넣은** 이벤트만 승격한다.
//
// 이것이 정확성 조건이다. llm_calls.source_event_id 와 tool_calls 의 두 이벤트 ID 는 각각
// UNIQUE 이고 그 제약은 ON CONFLICT(call_key) 가 잡지 않는다. 중복이라 건너뛴 이벤트를
// 다시 승격하면 이미 그 이벤트를 소비한 행과 부딪혀 배치 전체가 실패한다 — 재전송 한 번에
// 저장이 통째로 멎는다.
func (w *writer) promote(recs []EventRecord, turnIDs, eventIDs []int64) error {
	if err := w.promoteLLMCalls(recs, turnIDs, eventIDs); err != nil {
		return err
	}
	return w.promoteToolCalls(recs, turnIDs, eventIDs)
}

func (w *writer) promoteLLMCalls(recs []EventRecord, turnIDs, eventIDs []int64) error {
	for i, rec := range recs {
		if eventIDs[i] == 0 || !isLLMCall(rec.Event) {
			continue
		}
		m := rec.Event.Measure
		_, err := w.tx.ExecContext(w.ctx, insertLLMCallSQL,
			turnIDs[i], eventIDs[i], nullSec(rec.Event.TS.Sec()), nullStr(rec.Event.Attr.Model),
			optInt(m.InputTokens), optInt(m.OutputTokens),
			optInt(m.CacheReadTokens), optInt(m.CacheCreationTokens), nil,
			optFloat(m.CostUSD), optInt(m.DurationMS), nil,
		)
		if err != nil {
			return fmt.Errorf("store: llm_calls INSERT (name=%q): %w", rec.Event.Name, err)
		}
		w.res.LLMCallsInserted++
	}
	return nil
}

func (w *writer) promoteToolCalls(recs []EventRecord, turnIDs, eventIDs []int64) error {
	for i, rec := range recs {
		role := toolRoleOf(rec.Event)
		if eventIDs[i] == 0 || role == toolRoleNone || rec.CallKey == "" {
			continue
		}
		var decisionID, resultID any
		if role == toolRoleDecision {
			decisionID = eventIDs[i]
		} else {
			resultID = eventIDs[i]
		}

		e := rec.Event
		m := e.Measure
		var toolCallID int64
		err := w.tx.QueryRowContext(w.ctx, upsertToolCallSQL,
			turnIDs[i], rec.CallKey, decisionID, resultID,
			nullStr(e.Attr.ToolName), nullStr(rec.TargetPath), nullStr(e.Attr.MCPServer),
			nullSec(e.TS.Sec()), optInt(m.DurationMS), nil,
			optBool(m.Success), nullStr(e.Attr.Decision), nullStr(e.Attr.DecisionSource),
			optInt(m.ToolInputBytes), optInt(m.ToolResultBytes),
			nullStr(m.ErrorType), nullStr(m.ErrorMessage),
		).Scan(&toolCallID)
		if err != nil {
			return fmt.Errorf("store: tool_calls UPSERT (call_key=%q): %w", rec.CallKey, err)
		}
		w.res.ToolCallsUpserted++

		if err := w.promoteFileChange(rec, role, toolCallID); err != nil {
			return err
		}
	}
	return nil
}

// promoteFileChange 는 **결과 이벤트에서만** 파일 변경을 만든다.
//
// 결정 이벤트도 tool_input 을 실어 오므로 양쪽에서 만들면 호출 하나에 file_changes 가 두 행이
// 된다. file_changes 에는 그것을 막을 UNIQUE 가 없다. 그리고 결정만 있고 결과가 없는 호출은
// 실행되지 않았다는 뜻이라 — 거부된 편집이 그 경우다 — 파일이 바뀌지도 않았다.
func (w *writer) promoteFileChange(rec EventRecord, role toolRole, toolCallID int64) error {
	if role != toolRoleResult || rec.File.Operation == "" || rec.File.Path == "" {
		return nil
	}
	f := rec.File
	_, err := w.tx.ExecContext(w.ctx, insertFileChangeSQL,
		toolCallID, f.Path, f.Operation, nullStr(f.RenamedFrom),
		optInt(f.Additions), optInt(f.Deletions), nullStr(f.OldHash), nullStr(f.NewHash),
	)
	if err != nil {
		return fmt.Errorf("store: file_changes INSERT (op=%q): %w", f.Operation, err)
	}
	w.res.FileChangesInserted++
	return nil
}
