package store

import (
	"context"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/session"
)

// 결정 이벤트가 먼저 오고 결과가 뒤따르는 보통의 순서.
func TestToolCallMergesDecisionThenResult(t *testing.T) {
	db := openTestDB(t)

	mustWrite(t, db, Batch{Events: []EventRecord{
		evrec("claude_code.tool_decision", baseTime, 0,
			inTurn("p1"), call("claude_code:toolu_1"), toolName("Edit"), decision("accept", "config")),
	}})
	mustWrite(t, db, Batch{Events: []EventRecord{
		evrec("claude_code.tool_result", baseTime.Add(time.Second), 0,
			inTurn("p1"), call("claude_code:toolu_1"), toolName("Edit"), succeeded(true)),
	}})

	assertMergedToolCall(t, db)
}

// 결과가 먼저 도착하는 경우. 배치가 섞이면 실제로 일어난다.
func TestToolCallMergesResultThenDecision(t *testing.T) {
	db := openTestDB(t)

	mustWrite(t, db, Batch{Events: []EventRecord{
		evrec("claude_code.tool_result", baseTime.Add(time.Second), 0,
			inTurn("p1"), call("claude_code:toolu_1"), toolName("Edit"), succeeded(true)),
	}})
	mustWrite(t, db, Batch{Events: []EventRecord{
		evrec("claude_code.tool_decision", baseTime, 0,
			inTurn("p1"), call("claude_code:toolu_1"), toolName("Edit"), decision("accept", "config")),
	}})

	assertMergedToolCall(t, db)
}

func assertMergedToolCall(t *testing.T, db *DB) {
	t.Helper()
	if n := countRows(t, db, "tool_calls"); n != 1 {
		t.Fatalf("tool_calls = %d행, want 1 — 두 이벤트가 한 호출로 합쳐지지 않았다", n)
	}

	var (
		decisionID, resultID any
		tool, dec, src       any
		success              any
		calledAt             int64
	)
	row := db.SQL().QueryRowContext(context.Background(), `
		SELECT decision_event_id, result_event_id, tool_name, decision, decision_source,
		       success, called_at
		FROM tool_calls`)
	if err := row.Scan(&decisionID, &resultID, &tool, &dec, &src, &success, &calledAt); err != nil {
		t.Fatalf("tool_calls 조회: %v", err)
	}
	if decisionID == nil || resultID == nil {
		t.Fatalf("두 이벤트 ID 가 다 안 찼다: %v / %v", decisionID, resultID)
	}
	if tool != "Edit" || dec != "accept" || src != "config" {
		t.Errorf("컬럼 = %v / %v / %v", tool, dec, src)
	}
	if success != int64(1) {
		t.Errorf("success = %v, want 1", success)
	}
	// called_at 은 두 이벤트 중 이른 쪽이다.
	if calledAt != int64(baseTime.Unix()) {
		t.Errorf("called_at = %d, want %d", calledAt, baseTime.Unix())
	}
	assertNoOrphans(t, db)
}

// 결과가 있는 호출의 turn_id 는 결과 이벤트의 턴을 따른다 (docs/sqlite-schema/tool-calls.md).
func TestToolCallTurnFollowsResultEvent(t *testing.T) {
	db := openTestDB(t)
	mustWrite(t, db, Batch{Events: []EventRecord{
		evrec("claude_code.user_prompt", baseTime, 0, inTurn("p1")),
		evrec("claude_code.tool_decision", baseTime, 1,
			inTurn("p1"), call("claude_code:toolu_1"), toolName("Edit"), decision("accept", "config")),
		evrec("claude_code.user_prompt", baseTime, 2, inTurn("p2")),
		evrec("claude_code.tool_result", baseTime.Add(time.Second), 0,
			inTurn("p2"), call("claude_code:toolu_1"), toolName("Edit"), succeeded(true)),
	}})

	got := scanOne(t, db, `
		SELECT t.turn_key FROM tool_calls c JOIN turns t ON t.id = c.turn_id`)
	if got != "p2" {
		t.Fatalf("turn_key = %v, want p2 (결과 이벤트의 턴)", got)
	}
}

// 거부된 호출은 결과 이벤트가 없다. CHECK(decision_event_id IS NOT NULL OR
// result_event_id IS NOT NULL) 가 걸리지 않아야 하고, success 는 NULL 이어야 한다.
func TestRejectedToolCallHasDecisionOnly(t *testing.T) {
	db := openTestDB(t)
	mustWrite(t, db, Batch{Events: []EventRecord{
		evrec("claude_code.tool_decision", baseTime, 0,
			inTurn("p1"), call("claude_code:toolu_1"), toolName("Edit"), decision("reject", "user")),
	}})

	var decisionID, resultID, success any
	row := db.SQL().QueryRowContext(context.Background(),
		`SELECT decision_event_id, result_event_id, success FROM tool_calls`)
	if err := row.Scan(&decisionID, &resultID, &success); err != nil {
		t.Fatalf("tool_calls 조회: %v", err)
	}
	if decisionID == nil || resultID != nil {
		t.Fatalf("결정만 있어야 한다: %v / %v", decisionID, resultID)
	}
	// 미상 ≠ 실패. 0 으로 눕히면 화면의 실패율이 조용히 부푼다.
	if success != nil {
		t.Fatalf("success = %v, want NULL", success)
	}
}

// 자동 승인된 호출은 결정 이벤트가 없다. 반대쪽 CHECK 도 걸리지 않아야 한다.
func TestAutoApprovedToolCallHasResultOnly(t *testing.T) {
	db := openTestDB(t)
	mustWrite(t, db, Batch{Events: []EventRecord{
		evrec("claude_code.tool_result", baseTime, 0,
			inTurn("p1"), call("claude_code:toolu_1"), toolName("Bash"), succeeded(false)),
	}})

	var decisionID, resultID, success any
	row := db.SQL().QueryRowContext(context.Background(),
		`SELECT decision_event_id, result_event_id, success FROM tool_calls`)
	if err := row.Scan(&decisionID, &resultID, &success); err != nil {
		t.Fatalf("tool_calls 조회: %v", err)
	}
	if decisionID != nil || resultID == nil {
		t.Fatalf("결과만 있어야 한다: %v / %v", decisionID, resultID)
	}
	if success != int64(0) {
		t.Fatalf("success = %v, want 0", success)
	}
}

// **중복이라 건너뛴 이벤트를 다시 승격하면 안 된다.**
//
// decision_event_id·result_event_id·source_event_id 는 각각 UNIQUE 이고 그 제약은
// ON CONFLICT(call_key) 가 잡지 않는다. 재승격하면 배치 전체가 하드 에러로 죽는다 —
// 재전송 한 번에 저장이 통째로 멎는 것이다.
func TestDedupedEventIsNotRepromoted(t *testing.T) {
	db := openTestDB(t)
	batch := Batch{Events: []EventRecord{
		evrec("claude_code.tool_result", baseTime, 0,
			inTurn("p1"), call("claude_code:toolu_1"), toolName("Edit"), succeeded(true)),
		evrec("claude_code.api_request", baseTime, 1, inTurn("p1"), cost(0.5), tokens(100, 20)),
	}}

	mustWrite(t, db, batch)
	res := mustWrite(t, db, batch) // 같은 배치 재전송

	if res.EventsInserted != 0 || res.EventsDuplicate != 2 {
		t.Fatalf("두 번째 Write = %+v", res)
	}
	if res.ToolCallsUpserted != 0 || res.LLMCallsInserted != 0 {
		t.Fatalf("중복 이벤트가 재승격됐다: %+v", res)
	}
	if n := countRows(t, db, "tool_calls"); n != 1 {
		t.Fatalf("tool_calls = %d행, want 1", n)
	}
	if n := countRows(t, db, "llm_calls"); n != 1 {
		t.Fatalf("llm_calls = %d행, want 1", n)
	}
}

// 오류 정보는 두 컬럼으로 갈린다 — error_type 은 정제된 값, error_message 는 원문이다 (ADR 0010).
func TestToolCallStoresErrorMessageSeparately(t *testing.T) {
	db := openTestDB(t)
	const msg = "ENOENT: no such file or directory, open '/Users/jy/dev/x.go'"

	mustWrite(t, db, Batch{Events: []EventRecord{
		evrec("claude_code.tool_result", baseTime, 0,
			inTurn("p1"), call("claude_code:toolu_1"), toolName("Read"),
			succeeded(false), errMessage("", msg)),
	}})

	var errType, errMsg any
	row := db.SQL().QueryRowContext(context.Background(),
		`SELECT error_type, error_message FROM tool_calls`)
	if err := row.Scan(&errType, &errMsg); err != nil {
		t.Fatalf("tool_calls 조회: %v", err)
	}
	if errType != nil {
		t.Errorf("error_type = %v, want NULL (정제 규칙은 그대로다)", errType)
	}
	if errMsg != msg {
		t.Errorf("error_message = %v, want 벤더 원문", errMsg)
	}
}

// call_key 가 없는 도구 이벤트는 승격하지 않는다. store 는 절대 추측하지 않는다.
func TestToolEventWithoutCallKeyIsNotPromoted(t *testing.T) {
	db := openTestDB(t)
	res := mustWrite(t, db, Batch{Events: []EventRecord{
		evrec("claude_code.tool_result", baseTime, 0, inTurn("p1"), toolName("Edit"), succeeded(true)),
	}})
	if res.ToolCallsUpserted != 0 {
		t.Fatalf("call_key 없이 승격됐다: %+v", res)
	}
	if res.EventsInserted != 1 {
		t.Fatalf("이벤트는 저장돼야 한다: %+v", res)
	}
}

// ── llm_calls ───────────────────────────────────────────────────────────────

// **비용을 두 경로로 받으면 정확히 2배가 된다.** llm_calls 의 출처는 로그뿐이다.
func TestCostIsNotDoubleCounted(t *testing.T) {
	db := openTestDB(t)

	mustWrite(t, db, Batch{Events: []EventRecord{
		// 같은 사실의 로그 표현. 이것만 승격 대상이다.
		evrec("claude_code.api_request", baseTime, 0, inTurn("p1"), cost(0.0342), tokens(1820, 614)),
		// 같은 사실의 메트릭 표현. 승격하면 비용이 두 배가 된다.
		evrec("x", baseTime, 1, inTurn("p1"), metric("claude_code.cost.usage"), cost(0.0342)),
		evrec("x", baseTime, 2, inTurn("p1"), metric("claude_code.token.usage"), tokens(1820, 614)),
	}})

	if n := countRows(t, db, "llm_calls"); n != 1 {
		t.Fatalf("llm_calls = %d행, want 1 — 메트릭까지 승격했다", n)
	}
	if got := scanOne(t, db, `SELECT SUM(cost_usd) FROM llm_calls`); got != 0.0342 {
		t.Fatalf("cost_usd 합 = %v, want 0.0342", got)
	}
	// 이벤트 자체는 셋 다 남는다 — 승격하지 않는 것과 저장하지 않는 것은 다르다.
	if n := countRows(t, db, "events"); n != 3 {
		t.Fatalf("events = %d행, want 3", n)
	}
}

// Codex 는 스트리밍 이벤트 하나로 여러 종류를 보낸다. 완료 이벤트만 호출 한 건이다.
func TestCodexSSEPromotesOnlyCompletedEvent(t *testing.T) {
	db := openTestDB(t)

	delta := evrec("codex.sse_event", baseTime, 0, inTurn("p1"), withVendor("codex"))
	delta.Event.Attr.Type = "response.output_text.delta"
	done := evrec("codex.sse_event", baseTime, 1, inTurn("p1"), withVendor("codex"), tokens(100, 20))
	done.Event.Attr.Type = "response.completed"

	mustWrite(t, db, Batch{Events: []EventRecord{delta, done}})

	if n := countRows(t, db, "llm_calls"); n != 1 {
		t.Fatalf("llm_calls = %d행, want 1", n)
	}
}

// 토큰 컬럼의 이름이 스키마와 맞는지 본다. cache_creation → cache_write_tokens 다.
func TestLLMCallColumns(t *testing.T) {
	db := openTestDB(t)
	rec := evrec("claude_code.api_request", baseTime, 0, inTurn("p1"), cost(0.5), tokens(100, 20))
	rec.Event.Attr.Model = "claude-opus-5"
	rec.Event.Measure.CacheReadTokens = someInt(300)
	rec.Event.Measure.CacheCreationTokens = someInt(40)
	rec.Event.Measure.DurationMS = someInt(3187)

	mustWrite(t, db, Batch{Events: []EventRecord{rec}})

	var (
		model                          any
		in, out, cacheRead, cacheWrite any
		reasoning, requestID           any
		duration                       any
	)
	row := db.SQL().QueryRowContext(context.Background(), `
		SELECT model, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
		       reasoning_tokens, request_id, duration_ms
		FROM llm_calls`)
	if err := row.Scan(&model, &in, &out, &cacheRead, &cacheWrite, &reasoning, &requestID, &duration); err != nil {
		t.Fatalf("llm_calls 조회: %v", err)
	}
	if model != "claude-opus-5" || in != int64(100) || out != int64(20) {
		t.Errorf("모델·토큰 = %v / %v / %v", model, in, out)
	}
	if cacheRead != int64(300) || cacheWrite != int64(40) {
		t.Errorf("캐시 토큰 = %v / %v", cacheRead, cacheWrite)
	}
	if duration != int64(3187) {
		t.Errorf("duration_ms = %v", duration)
	}
	// 관측하지 않은 값은 0 이 아니라 NULL 이다.
	if reasoning != nil || requestID != nil {
		t.Errorf("미관측 컬럼이 채워졌다: %v / %v", reasoning, requestID)
	}
}

// ── file_changes ────────────────────────────────────────────────────────────

// 파일 변경은 결과 이벤트에서만 만든다. 결정 이벤트도 tool_input 을 실어 오므로
// 양쪽에서 만들면 호출 하나에 두 행이 생기고, 그것을 막을 UNIQUE 가 없다.
func TestFileChangePromotedFromResultOnly(t *testing.T) {
	db := openTestDB(t)
	const path = "/Users/jy/dev/projects/soma-376/telemetryctl/internal/store/write.go"

	mustWrite(t, db, Batch{Events: []EventRecord{
		evrec("claude_code.tool_decision", baseTime, 0,
			inTurn("p1"), call("claude_code:toolu_1"), toolName("Edit"),
			decision("accept", "config"), targetPath(path),
			fileChange(session.OperationModify, path)),
		evrec("claude_code.tool_result", baseTime.Add(time.Second), 0,
			inTurn("p1"), call("claude_code:toolu_1"), toolName("Edit"),
			succeeded(true), targetPath(path), fileChange(session.OperationModify, path)),
	}})

	if n := countRows(t, db, "file_changes"); n != 1 {
		t.Fatalf("file_changes = %d행, want 1", n)
	}
	// file_path 는 원경로다 (ADR 0010). basename 을 넣으면 같은 이름의 다른 파일이 뭉개진다.
	if got := scanOne(t, db, `SELECT file_path FROM file_changes`); got != path {
		t.Fatalf("file_path = %v, want 원경로", got)
	}
	if got := scanOne(t, db, `SELECT operation FROM file_changes`); got != "modify" {
		t.Fatalf("operation = %v", got)
	}
	// tool_calls.target 에도 원경로가 실린다.
	if got := scanOne(t, db, `SELECT target FROM tool_calls`); got != path {
		t.Fatalf("target = %v", got)
	}
	assertNoOrphans(t, db)
}

// 거부된 편집은 실행되지 않았으므로 파일이 바뀌지도 않았다.
func TestRejectedDecisionMakesNoFileChange(t *testing.T) {
	db := openTestDB(t)
	const path = "/Users/jy/dev/x.go"

	mustWrite(t, db, Batch{Events: []EventRecord{
		evrec("claude_code.tool_decision", baseTime, 0,
			inTurn("p1"), call("claude_code:toolu_1"), toolName("Edit"),
			decision("reject", "user"), targetPath(path),
			fileChange(session.OperationModify, path)),
	}})

	if n := countRows(t, db, "file_changes"); n != 0 {
		t.Fatalf("file_changes = %d행, want 0", n)
	}
}

// rename 은 renamed_from 이 없으면 CHECK 가 거부한다. 그 제약이 살아 있는지 본다.
func TestFileChangeRenameCheckIsEnforced(t *testing.T) {
	db := openTestDB(t)
	mustWrite(t, db, Batch{Events: []EventRecord{
		evrec("claude_code.tool_result", baseTime, 0,
			inTurn("p1"), call("claude_code:toolu_1"), toolName("Edit"), succeeded(true)),
	}})
	id := scanOne(t, db, `SELECT id FROM tool_calls`)

	_, err := db.SQL().ExecContext(context.Background(),
		`INSERT INTO file_changes (tool_call_id, file_path, operation) VALUES (?, '/tmp/a.go', 'rename')`, id)
	if err == nil {
		t.Fatal("renamed_from 없는 rename 이 들어갔다 — CHECK 가 죽었다")
	}
}
