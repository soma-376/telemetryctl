package store

import (
	"context"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/event"
	"github.com/your-org/pulsemetry/internal/rollup"
	"github.com/your-org/pulsemetry/internal/session"
)

// 이벤트 한 건이 들어가고 모든 컬럼이 제자리로 나오는지 본다.
// Opt 미설정이 0 이 아니라 NULL 로 남는 것이 특히 중요하다 — 0 으로 눕히면
// "비용 0" 과 "비용 개념 없음" 이 구분되지 않는다.
func TestWriteEventRoundTrip(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	e := newEvent("claude_code.api_request", baseTime, 0)
	e.Signal = event.SignalMetric
	e.Temporality = event.TemporalityDelta
	e.EventID = "evt-1"
	e.Attr = event.Attributes{
		Model:       "claude-opus-5",
		Type:        "input",
		ToolName:    "Edit",
		ProjectHash: "phash",
		ProjectName: "telemetryctl",
	}
	e.Measure = event.Measures{
		Value:       event.Some(3.5),
		Unit:        "token",
		InputTokens: event.Some(int64(120)),
		Success:     event.Some(false),
		DurationMS:  event.Some(int64(42)),
	}

	res, err := db.Write(ctx, Batch{Events: []EventRecord{{Event: e}}})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if res.EventsInserted != 1 || res.EventsDuplicate != 0 {
		t.Fatalf("WriteResult = %+v", res)
	}

	var (
		dedup                                 string
		ts, hour                              int64
		sessionID, vendor, signal, name, inst string
		model, projName                       string
		value                                 float64
		inputTokens, durationMS, success      int64
		costUSD, outputTokens, attempt        any
	)
	row := db.SQL().QueryRowContext(ctx, `
		SELECT dedup_key, ts, hour, session_id, vendor, signal, name, installation_id,
		       model, project_name, value, input_tokens, duration_ms, success,
		       cost_usd, output_tokens, attempt
		FROM events`)
	if err := row.Scan(&dedup, &ts, &hour, &sessionID, &vendor, &signal, &name, &inst,
		&model, &projName, &value, &inputTokens, &durationMS, &success,
		&costUSD, &outputTokens, &attempt); err != nil {
		t.Fatalf("events 조회: %v", err)
	}

	if dedup != e.DedupKey() {
		t.Errorf("dedup_key = %q, want %q", dedup, e.DedupKey())
	}
	if ts != int64(e.TS) || hour != int64(e.Hour()) {
		t.Errorf("ts/hour = %d/%d, want %d/%d", ts, hour, int64(e.TS), int64(e.Hour()))
	}
	if sessionID != "sess-1" || vendor != "claude_code" || signal != "metric" || inst != "inst-1" {
		t.Errorf("신원 컬럼 = %q/%q/%q/%q", sessionID, vendor, signal, inst)
	}
	if name != "claude_code.api_request" || model != "claude-opus-5" || projName != "telemetryctl" {
		t.Errorf("속성 컬럼 = %q/%q/%q", name, model, projName)
	}
	if value != 3.5 || inputTokens != 120 || durationMS != 42 {
		t.Errorf("수치 컬럼 = %v/%d/%d", value, inputTokens, durationMS)
	}
	if success != 0 {
		t.Errorf("success = %d, want 0 (false)", success)
	}
	for label, v := range map[string]any{"cost_usd": costUSD, "output_tokens": outputTokens, "attempt": attempt} {
		if v != nil {
			t.Errorf("%s = %v, want NULL (미설정)", label, v)
		}
	}
}

// 재전송은 정상 동작이다. 에러가 아니라 조용한 무시여야 하되 건수는 보여야 한다.
// 원문도 함께 건너뛰어야 고아가 생기지 않는다.
func TestWriteEventDuplicateIsIgnored(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	rec := EventRecord{
		Event:    newEvent("claude_code.user_prompt", baseTime, 0),
		Contents: []event.Content{{Kind: event.ContentPrompt, Body: "토큰 검증 로직을 고쳐줘"}},
	}

	first, err := db.Write(ctx, Batch{Events: []EventRecord{rec}})
	if err != nil {
		t.Fatalf("첫 Write: %v", err)
	}
	if first.EventsInserted != 1 || first.ContentsInserted != 1 {
		t.Fatalf("첫 WriteResult = %+v", first)
	}

	// 같은 배치 안의 중복과 배치를 넘어선 중복 둘 다 확인한다.
	second, err := db.Write(ctx, Batch{Events: []EventRecord{rec, rec}})
	if err != nil {
		t.Fatalf("두번째 Write: %v", err)
	}
	if second.EventsInserted != 0 || second.EventsDuplicate != 2 {
		t.Fatalf("두번째 WriteResult = %+v, want 삽입 0 중복 2", second)
	}
	if second.ContentsInserted != 0 {
		t.Fatalf("중복 이벤트의 원문이 저장됐다: %+v", second)
	}

	if n := countRows(t, db, "events"); n != 1 {
		t.Errorf("events = %d행, want 1", n)
	}
	if n := countRows(t, db, "event_content"); n != 1 {
		t.Errorf("event_content = %d행, want 1", n)
	}
	// 고아 확인: 모든 원문이 살아 있는 이벤트를 가리켜야 한다.
	if n := countWhere(t, db, "event_content", "event_id NOT IN (SELECT id FROM events)"); n != 0 {
		t.Errorf("고아 원문 %d행", n)
	}
}

// 한 이벤트가 여러 종류의 원문을 가진다 (tool_result 로그는 tool_input 과 tool_result 를
// 함께 실어 온다). 계획서 DDL 의 event_id PRIMARY KEY 로는 표현되지 않아 조정한 지점이다.
func TestWriteMultipleContentKindsPerEvent(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	rec := EventRecord{
		Event: newEvent("claude_code.tool_result", baseTime, 0),
		Contents: []event.Content{
			{Kind: event.ContentToolInput, Body: `{"file_path":"/tmp/a.go"}`},
			{Kind: event.ContentToolResult, Body: "수정 완료", Truncated: true},
		},
	}
	res, err := db.Write(ctx, Batch{Events: []EventRecord{rec}})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if res.ContentsInserted != 2 {
		t.Fatalf("ContentsInserted = %d, want 2", res.ContentsInserted)
	}
	if n := countRows(t, db, "event_content"); n != 2 {
		t.Fatalf("event_content = %d행, want 2", n)
	}
	if n := countWhere(t, db, "event_content", "kind = 'tool_result' AND truncated = 1"); n != 1 {
		t.Errorf("truncated 플래그가 저장되지 않았다")
	}
}

// --no-store-content 는 저장소에서 집행된다.
func TestWriteContentStorageDisabled(t *testing.T) {
	db := openTestDB(t, WithContentStorage(false))
	ctx := context.Background()

	res, err := db.Write(ctx, Batch{Events: []EventRecord{{
		Event:    newEvent("claude_code.user_prompt", baseTime, 0),
		Contents: []event.Content{{Kind: event.ContentPrompt, Body: "비밀"}},
	}}})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if res.ContentsInserted != 0 || res.ContentsDropped != 1 {
		t.Fatalf("WriteResult = %+v, want 저장 0 폐기 1", res)
	}
	if n := countRows(t, db, "events"); n != 1 {
		t.Errorf("events = %d행, want 1 (수치는 남는다)", n)
	}
	if n := countRows(t, db, "event_content"); n != 0 {
		t.Errorf("event_content = %d행, want 0", n)
	}
}

// 조립기는 전체 스냅샷을 준다. 같은 세션을 두 번 저장해도 타임라인·파일 목록이 두 배가
// 되면 안 된다.
func TestWriteSessionSnapshotIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	s := newSession("sess-1", baseTime)

	for i := 0; i < 3; i++ {
		if _, err := db.Write(ctx, Batch{Sessions: []session.Session{s}}); err != nil {
			t.Fatalf("Write %d회차: %v", i+1, err)
		}
	}

	tests := []struct {
		table string
		want  int
	}{
		{"sessions", 1},
		{"session_files", 1},
		{"mcp_session_usage", 1},
		// 같은 초·같은 툴·같은 대상인 두 건이 접히지 않고 그대로 남아야 한다.
		{"tool_events", 2},
	}
	for _, tc := range tests {
		if n := countRows(t, db, tc.table); n != tc.want {
			t.Errorf("%s = %d행, want %d", tc.table, n, tc.want)
		}
	}

	// 수치는 누적이 아니라 대입이다. 세 번 썼다고 tool_calls 가 6 이 되면 안 된다.
	var toolCalls int64
	var cost float64
	if err := db.SQL().QueryRowContext(ctx,
		`SELECT tool_calls, cost_usd FROM sessions WHERE session_id = 'sess-1'`).Scan(&toolCalls, &cost); err != nil {
		t.Fatalf("sessions 조회: %v", err)
	}
	if toolCalls != 2 || cost != 0.5 {
		t.Errorf("tool_calls/cost_usd = %d/%v, want 2/0.5", toolCalls, cost)
	}
}

// 스냅샷이 자라면 타임라인도 그만큼만 자란다.
func TestWriteSessionGrowingTimeline(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	s := newSession("sess-1", baseTime)
	if _, err := db.Write(ctx, Batch{Sessions: []session.Session{s}}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	s.Tools = append(s.Tools, session.ToolEvent{
		TS:       event.SecFromTime(baseTime.Add(time.Minute)),
		ToolName: "Bash",
		Action:   session.ActionRun,
		Success:  event.Some(false),
	})
	s.LastEventAt = event.SecFromTime(baseTime.Add(time.Minute))
	s.EndedAt = event.Some(s.LastEventAt)
	s.Status = session.StatusCompleted

	res, err := db.Write(ctx, Batch{Sessions: []session.Session{s}})
	if err != nil {
		t.Fatalf("두번째 Write: %v", err)
	}
	if res.ToolEventsDeleted != 2 || res.ToolEventsWritten != 3 {
		t.Fatalf("WriteResult = %+v, want 삭제 2 기록 3", res)
	}
	if n := countRows(t, db, "tool_events"); n != 3 {
		t.Errorf("tool_events = %d행, want 3", n)
	}

	var status string
	var endedAt int64
	if err := db.SQL().QueryRowContext(ctx,
		`SELECT status, ended_at FROM sessions WHERE session_id = 'sess-1'`).Scan(&status, &endedAt); err != nil {
		t.Fatalf("sessions 조회: %v", err)
	}
	if status != string(session.StatusCompleted) || endedAt != int64(s.LastEventAt) {
		t.Errorf("status/ended_at = %q/%d", status, endedAt)
	}
}

// 진행 중 세션의 ended_at 은 NULL 이어야 한다.
func TestWriteSessionRunningHasNullEndedAt(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Write(context.Background(), Batch{Sessions: []session.Session{newSession("sess-1", baseTime)}}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n := countWhere(t, db, "sessions", "ended_at IS NULL"); n != 1 {
		t.Fatalf("ended_at IS NULL 행 = %d, want 1", n)
	}
}

// 후속 티켓이 채울 컬럼은 세션 스냅샷이 덮어쓰지 않는다.
func TestWriteSessionKeepsFollowUpColumns(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	s := newSession("sess-1", baseTime)

	if _, err := db.Write(ctx, Batch{Sessions: []session.Session{s}}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := db.SQL().ExecContext(ctx,
		`UPDATE sessions SET phase_json = '[]', work_type = 'debugging'`); err != nil {
		t.Fatalf("후속 컬럼 채우기: %v", err)
	}
	if _, err := db.Write(ctx, Batch{Sessions: []session.Session{s}}); err != nil {
		t.Fatalf("두번째 Write: %v", err)
	}

	var phase, workType string
	if err := db.SQL().QueryRowContext(ctx,
		`SELECT phase_json, work_type FROM sessions`).Scan(&phase, &workType); err != nil {
		t.Fatalf("sessions 조회: %v", err)
	}
	if phase != "[]" || workType != "debugging" {
		t.Fatalf("phase_json/work_type = %q/%q — 스냅샷이 덮어썼다", phase, workType)
	}
}

// 집계기는 Flush 로 버킷을 비우고 증분만 다시 준다. 덮어쓰면 하루치가 마지막 몇 분치로 준다.
func TestWriteRollupAccumulates(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	hour := event.HourOf(event.NanoFromTime(baseTime))

	row := rollup.Row{Hour: hour, Dim: rollup.DimTotal, Key: ""}
	row.CostUSD = 1.5
	row.InputTokens = 100
	row.Prompts = 1

	for i := 0; i < 3; i++ {
		if _, err := db.Write(ctx, Batch{Rollups: []rollup.Row{row}}); err != nil {
			t.Fatalf("Write %d회차: %v", i+1, err)
		}
	}

	if n := countRows(t, db, "rollup_hourly"); n != 1 {
		t.Fatalf("rollup_hourly = %d행, want 1", n)
	}
	var cost float64
	var tokens, prompts int64
	if err := db.SQL().QueryRowContext(ctx,
		`SELECT cost_usd, input_tokens, prompts FROM rollup_hourly`).Scan(&cost, &tokens, &prompts); err != nil {
		t.Fatalf("rollup_hourly 조회: %v", err)
	}
	if cost != 4.5 || tokens != 300 || prompts != 3 {
		t.Fatalf("누적값 = %v/%d/%d, want 4.5/300/3 (덮어쓰기가 아니라 누적)", cost, tokens, prompts)
	}
}

// dim 이 다르면 별도 행이고, 기여분이 0 인 행은 만들지 않는다.
func TestWriteRollupDimensions(t *testing.T) {
	db := openTestDB(t)
	hour := event.HourOf(event.NanoFromTime(baseTime))

	total := rollup.Row{Hour: hour, Dim: rollup.DimTotal}
	total.CostUSD = 1
	vendor := rollup.Row{Hour: hour, Dim: rollup.DimVendor, Key: "claude_code"}
	vendor.CostUSD = 1
	empty := rollup.Row{Hour: hour, Dim: rollup.DimModel, Key: "claude-opus-5"}

	res, err := db.Write(context.Background(), Batch{Rollups: []rollup.Row{total, vendor, empty}})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if res.RollupRows != 2 {
		t.Fatalf("RollupRows = %d, want 2 (0 기여 행은 건너뛴다)", res.RollupRows)
	}
	if n := countRows(t, db, "rollup_hourly"); n != 2 {
		t.Fatalf("rollup_hourly = %d행, want 2", n)
	}
}

func TestWriteVendors(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	early := baseTime
	late := baseTime.Add(2 * time.Hour)
	recs := []EventRecord{
		{Event: newEvent("claude_code.user_prompt", late, 0)},
		{Event: newEvent("claude_code.user_prompt", early, 1)},
	}
	codex := newEvent("codex.user_prompt", early, 2)
	codex.Vendor = "codex"
	recs = append(recs, EventRecord{Event: codex})

	if _, err := db.Write(ctx, Batch{Events: recs}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// 재전송은 events_total 을 올리지 않는다.
	if _, err := db.Write(ctx, Batch{Events: recs}); err != nil {
		t.Fatalf("두번째 Write: %v", err)
	}

	var first, last, total int64
	if err := db.SQL().QueryRowContext(ctx,
		`SELECT first_seen, last_seen, events_total FROM vendors WHERE vendor = 'claude_code'`).
		Scan(&first, &last, &total); err != nil {
		t.Fatalf("vendors 조회: %v", err)
	}
	if first != early.Unix() || last != late.Unix() || total != 2 {
		t.Fatalf("vendors = %d/%d/%d, want %d/%d/2", first, last, total, early.Unix(), late.Unix())
	}
	if n := countRows(t, db, "vendors"); n != 2 {
		t.Errorf("vendors = %d행, want 2", n)
	}
}

// 배치 하나는 전부 적용되거나 전혀 적용되지 않는다. 세션 수치와 이벤트가 어긋나면
// 화면의 숫자가 서로 모순된다.
func TestWriteIsAtomic(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	broken := newEvent("claude_code.user_prompt", baseTime, 0)
	broken.Vendor = "" // events.vendor 는 NOT NULL

	_, err := db.Write(ctx, Batch{
		Events:   []EventRecord{{Event: newEvent("claude_code.user_prompt", baseTime, 1)}, {Event: broken}},
		Sessions: []session.Session{newSession("sess-1", baseTime)},
	})
	if err == nil {
		t.Fatal("깨진 이벤트가 들어갔다")
	}
	for _, table := range []string{"events", "sessions", "tool_events", "vendors"} {
		if n := countRows(t, db, table); n != 0 {
			t.Errorf("%s = %d행, want 0 (롤백)", table, n)
		}
	}
}

func TestWriteEmptyBatch(t *testing.T) {
	db := openTestDB(t)
	res, err := db.Write(context.Background(), Batch{})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if (res != WriteResult{}) {
		t.Fatalf("WriteResult = %+v, want 제로값", res)
	}
}
