package dashboard

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/event"
	"github.com/your-org/pulsemetry/internal/pricing"
	"github.com/your-org/pulsemetry/internal/session"
	"github.com/your-org/pulsemetry/internal/store"
)

// metricsAt 은 지표 픽스처의 기준 시각이다. testNow 보다 앞이라 보존 대상이 아니다.
var metricsAt = testNow.Add(-2 * time.Hour)

// execSQL 은 픽스처를 손보는 SQL 을 실행한다.
//
// 쓰기 경로가 채우지 않는 컬럼(events.payload · llm_calls.reasoning_tokens ·
// turns.ended_at)을 심을 때만 쓴다. 그 컬럼들을 "지금 안 채워지니까" 하고 테스트에서
// 빼 두면, 쓰기 쪽이 채우기 시작하는 순간 조회가 조용히 틀린다.
func execSQL(t *testing.T, f *fixture, query string, args ...any) {
	t.Helper()
	if _, err := f.db.SQL().ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("픽스처 SQL 실패: %v", err)
	}
}

// seedMetricsSession 은 턴 두 개짜리 세션을 만든다.
//
//	t-1: 프롬프트 1 · LLM 호출 1(비용 2, 100/20 토큰, 캐시 500/50) · 툴 호출 2(실패 1)
//	t-2: LLM 호출 1(비용 1.5, 40/8 토큰) · 툴 호출 1(거부)
func seedMetricsSession(f *fixture) int64 {
	f.t.Helper()
	f.write(store.Batch{
		Sessions: []session.Session{newSession("s-metrics", metricsAt)},
		Events: []store.EventRecord{
			promptRecord("s-metrics", "t-1", metricsAt, 1, "인증 토큰 검증 프록시"),
			llmRecord("s-metrics", "t-1", metricsAt, 2, llmSpec{
				Model: "claude-sonnet-4-5", Cost: 2, Input: 100, Output: 20,
				CacheRead: 500, CacheWrit: 50,
			}),
			toolRecord("s-metrics", "t-1", "call-m1", metricsAt, 3, toolSpec{
				ToolName: "Edit", Success: event.Some(true),
				Target: workspaceA + "/apply.go",
				File:   fileChange(workspaceA+"/apply.go", 4, 1),
			}),
			toolRecord("s-metrics", "t-1", "call-m2", metricsAt, 4, toolSpec{
				ToolName: "Read", Success: event.Some(false), ErrorType: "EACCES",
			}),
			promptRecord("s-metrics", "t-2", metricsAt.Add(time.Minute), 5, "테스트 추가"),
			llmRecord("s-metrics", "t-2", metricsAt.Add(time.Minute), 6, llmSpec{
				Model: "claude-sonnet-4-5", Cost: 1.5, Input: 40, Output: 8,
			}),
			toolRecord("s-metrics", "t-2", "call-m3", metricsAt.Add(time.Minute), 7, toolSpec{
				ToolName: "Edit", Decision: "reject",
			}),
		},
	})
	return f.sessionID(vendorClaude, "s-metrics")
}

// 티켓의 「작업 내용」: 소요 시간·토큰·툴 호출·비용·캐시 read/write·턴 수를 집계한다.
func TestSessionMetricsAggregatesSessionAndTurns(t *testing.T) {
	f := newFixture(t)
	id := seedMetricsSession(f)

	m, err := f.reader.SessionMetrics(context.Background(), SessionMetricsQuery{SessionID: id})
	if err != nil {
		t.Fatalf("SessionMetrics: %v", err)
	}
	if !m.Found {
		t.Fatal("Found = false")
	}

	t.Run("세션 머리", func(t *testing.T) {
		if m.SessionKey != "s-metrics" || m.Vendor != vendorClaude {
			t.Errorf("세션 = %s/%s", m.Vendor, m.SessionKey)
		}
		if m.Status != StatusCompleted {
			t.Errorf("Status = %q, want %q", m.Status, StatusCompleted)
		}
		// newSession 은 started_at ~ ended_at 이 600초, active_time_sec 이 120초다.
		if m.DurationSeconds == nil || *m.DurationSeconds != 600 {
			t.Errorf("DurationSeconds = %v, want 600", m.DurationSeconds)
		}
		if m.ActiveSeconds == nil || *m.ActiveSeconds != 120 {
			t.Errorf("ActiveSeconds = %v, want 120", m.ActiveSeconds)
		}
		if m.ProjectName != "telemetryctl" {
			t.Errorf("ProjectName = %q, want telemetryctl", m.ProjectName)
		}
	})

	t.Run("상단 합계", func(t *testing.T) {
		got := m.Totals
		switch {
		case got.TurnCount != 2 || got.PromptTurns != 2:
			t.Errorf("턴 수 = %d/%d, want 2/2", got.TurnCount, got.PromptTurns)
		case got.LLMCalls != 2:
			t.Errorf("LLMCalls = %d, want 2", got.LLMCalls)
		case got.ToolCalls != 3:
			t.Errorf("ToolCalls = %d, want 3", got.ToolCalls)
		case got.ToolErrors != 1:
			t.Errorf("ToolErrors = %d, want 1", got.ToolErrors)
		case got.ToolRejects != 1:
			t.Errorf("ToolRejects = %d, want 1", got.ToolRejects)
		case got.Tokens.Input != 140 || got.Tokens.Output != 28:
			t.Errorf("토큰 = %d/%d, want 140/28", got.Tokens.Input, got.Tokens.Output)
		case got.Tokens.CacheRead != 500 || got.Tokens.CacheWrite != 50:
			t.Errorf("캐시 토큰 = %d/%d, want 500/50", got.Tokens.CacheRead, got.Tokens.CacheWrite)
		case got.Tokens.Billable() != 168:
			t.Errorf("Billable = %d, want 168 (캐시는 더하지 않는다)", got.Tokens.Billable())
		}
		// 벤더가 보고한 비용이 있으므로 그것이 이긴다 (pricing 의 보고값 우선 규칙).
		if want := pricing.NanoUSD(3_500_000_000); got.Cost.Total.NanoUSD != want {
			t.Errorf("비용 = %d nano, want %d (2 + 1.5 USD)", got.Cost.Total.NanoUSD, want)
		}
		if got.Cost.ReportedCalls != 2 || !got.Cost.Complete {
			t.Errorf("비용 출처 = %+v, want 보고 2건·complete", got.Cost)
		}
	})

	t.Run("턴별 합계", func(t *testing.T) {
		if len(m.Turns) != 2 {
			t.Fatalf("턴 = %d, want 2 (%+v)", len(m.Turns), m.Turns)
		}
		first := m.Turns[0]
		if first.TurnKey != "t-1" || first.Virtual {
			t.Errorf("1번 턴 = %q virtual=%v, want t-1", first.TurnKey, first.Virtual)
		}
		if first.TurnIndex == nil {
			t.Fatal("실제 턴인데 TurnIndex 가 null 이다")
		}
		if first.LLMCalls != 1 || first.ToolCalls != 2 || first.ToolErrors != 1 {
			t.Errorf("1번 턴 = LLM %d · 툴 %d · 실패 %d, want 1/2/1",
				first.LLMCalls, first.ToolCalls, first.ToolErrors)
		}
		second := m.Turns[1]
		if second.LLMCalls != 1 || second.ToolCalls != 1 || second.ToolRejects != 1 {
			t.Errorf("2번 턴 = LLM %d · 툴 %d · 거부 %d, want 1/1/1",
				second.LLMCalls, second.ToolCalls, second.ToolRejects)
		}
	})
}

// 인수조건: **세션 상단 값과 턴별 합계의 정합성이 보장된다.**
//
// 셀 수 있는 값 전체를 구조체째 비교한다. 필드를 하나 더해도 이 단언이 자동으로 덮으므로
// "새 지표만 상단에서 두 번 세는" 부류의 실수가 빠져나가지 못한다.
func TestSessionMetricsTopLineEqualsTurnSum(t *testing.T) {
	f := newFixture(t)
	id := seedMetricsSession(f)
	// 가상 턴(세션 수준 이벤트)도 합계에 들어야 한다. 턴 키가 없는 이벤트가 그것을 만든다.
	f.write(store.Batch{Events: []store.EventRecord{
		llmRecord("s-metrics", "", metricsAt.Add(2*time.Minute), 8, llmSpec{
			Model: "claude-sonnet-4-5", Cost: 0.25, Input: 7, Output: 3, CacheRead: 11,
		}),
	}})

	ctx := context.Background()
	m, err := f.reader.SessionMetrics(ctx, SessionMetricsQuery{SessionID: id})
	if err != nil {
		t.Fatalf("SessionMetrics: %v", err)
	}
	if m.TurnsTruncated {
		t.Fatal("픽스처가 상한에 걸렸다 — 이 테스트는 잘리지 않은 응답을 봐야 한다")
	}

	var sum TurnTotals
	var turns, prompts int64
	for _, turn := range m.Turns {
		turns++
		if !turn.Virtual {
			prompts++
		}
		sum.add(turn.TurnTotals)
	}
	sum.finalize()

	if !reflect.DeepEqual(m.Totals.TurnTotals, sum) {
		t.Errorf("상단 값과 턴별 합계가 다르다:\n상단 = %+v\n합계 = %+v", m.Totals.TurnTotals, sum)
	}
	if m.Totals.TurnCount != turns || m.Totals.PromptTurns != prompts {
		t.Errorf("턴 수 = %d/%d, want %d/%d",
			m.Totals.TurnCount, m.Totals.PromptTurns, turns, prompts)
	}
	if turns != 3 || prompts != 2 {
		t.Errorf("픽스처 = 턴 %d · 실제 턴 %d, want 3/2 (가상 턴 하나 포함)", turns, prompts)
	}

	// PROJ-87 의 세션 목록 집계는 다른 질의로 같은 값을 낸다. 둘이 갈리면 화면마다
	// 다른 숫자가 뜬다.
	detail, err := f.reader.Session(ctx, id)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	row := detail.Session
	checks := []struct {
		name       string
		got, other int64
	}{
		{"input_tokens", m.Totals.Tokens.Input, row.InputTokens},
		{"output_tokens", m.Totals.Tokens.Output, row.OutputTokens},
		{"cache_read_tokens", m.Totals.Tokens.CacheRead, row.CacheReadTokens},
		{"cache_write_tokens", m.Totals.Tokens.CacheWrite, row.CacheCreationTokens},
		{"llm_calls", m.Totals.LLMCalls, row.APIRequests},
		{"tool_calls", m.Totals.ToolCalls, row.ToolCalls},
		{"tool_errors", m.Totals.ToolErrors, row.ToolErrors},
		{"tool_rejects", m.Totals.ToolRejects, row.ToolRejects},
		{"prompt_turns", m.Totals.PromptTurns, row.Prompts},
	}
	for _, c := range checks {
		if c.got != c.other {
			t.Errorf("%s = %d, Session() 은 %d — 두 조회가 갈렸다", c.name, c.got, c.other)
		}
	}
}

// 승격 테이블을 한 질의에 JOIN 으로 묶으면 행이 곱해져 모든 합계가 부풀어 오른다.
// PROJ-87 의 TestBreakdownDoesNotMultiplyAcrossSources 와 같은 사고를 세션 상세에서 막는다.
func TestSessionMetricsDoesNotMultiplyAcrossSources(t *testing.T) {
	f := newFixture(t)
	recs := []store.EventRecord{
		llmRecord("s-mul", "t-mul", metricsAt, 1, llmSpec{
			Model: "claude-sonnet-4-5", Cost: 5, Input: 100, Output: 20, CacheRead: 900,
		}),
	}
	for i := range 3 {
		recs = append(recs, toolRecord("s-mul", "t-mul", fmt.Sprintf("call-mul-%d", i),
			metricsAt, 10+i, toolSpec{ToolName: "Edit", Success: event.Some(true)}))
	}
	f.write(store.Batch{Events: recs})

	m, err := f.reader.SessionMetrics(context.Background(), SessionMetricsQuery{
		SessionID: f.sessionID(vendorClaude, "s-mul"),
	})
	if err != nil {
		t.Fatalf("SessionMetrics: %v", err)
	}
	got := m.Totals
	switch {
	case got.LLMCalls != 1:
		t.Errorf("LLMCalls = %d, want 1 — 도구 호출 행 수만큼 곱해졌다", got.LLMCalls)
	case got.ToolCalls != 3:
		t.Errorf("ToolCalls = %d, want 3", got.ToolCalls)
	case got.Tokens.Input != 100 || got.Tokens.Output != 20 || got.Tokens.CacheRead != 900:
		t.Errorf("토큰 = %+v, want 100/20/900", got.Tokens)
	case got.Cost.Total.NanoUSD != 5_000_000_000:
		t.Errorf("비용 = %d nano, want 5 USD", got.Cost.Total.NanoUSD)
	}
}

// 인수조건: 존재하지 않거나 보존으로 삭제된 session_id 는 오류가 아니라 found=false 다.
func TestSessionMetricsMissingSessionIsNotError(t *testing.T) {
	f := newFixture(t)
	id := seedMetricsSession(f)

	// 보존 삭제가 지운 뒤의 상태를 그대로 만든다 — 자식에서 부모 순서다 (ADR 0009).
	for _, stmt := range []string{
		`DELETE FROM file_changes WHERE tool_call_id IN
		   (SELECT id FROM tool_calls WHERE turn_id IN (SELECT id FROM turns WHERE session_id = ?))`,
		`DELETE FROM tool_calls WHERE turn_id IN (SELECT id FROM turns WHERE session_id = ?)`,
		`DELETE FROM llm_calls WHERE turn_id IN (SELECT id FROM turns WHERE session_id = ?)`,
		`DELETE FROM events WHERE turn_id IN (SELECT id FROM turns WHERE session_id = ?)`,
		`DELETE FROM turns WHERE session_id = ?`,
		`DELETE FROM sessions WHERE id = ?`,
	} {
		execSQL(t, f, stmt, id)
	}

	tests := []struct {
		name string
		id   int64
	}{
		{name: "보존이 지운 세션", id: id},
		{name: "한 번도 없던 id", id: 987654},
		{name: "0", id: 0},
		{name: "음수", id: -3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, err := f.reader.SessionMetrics(context.Background(),
				SessionMetricsQuery{SessionID: tc.id})
			if err != nil {
				t.Fatalf("에러가 났다: %v", err)
			}
			if m.Found {
				t.Error("Found = true")
			}
			if m.Turns == nil {
				t.Error("Turns 가 nil — JSON 에서 null 이 되어 프런트엔드가 터진다")
			}
		})
	}
}

// 인수조건: 긴 세션 응답의 상한과 잘림 여부가 명시된다.
//
// **상단 합계는 상한과 무관하게 세션 전체를 덮어야 한다.** 잘린 목록의 합만 보여 주면
// 화면의 총 비용이 상한에 따라 달라진다.
func TestSessionMetricsCapsTurnsAndFlagsTruncation(t *testing.T) {
	f := newFixture(t)
	const totalTurns = 5
	recs := []store.EventRecord{}
	for i := range totalTurns {
		turn := fmt.Sprintf("t-long-%d", i)
		recs = append(recs, llmRecord("s-long", turn, metricsAt.Add(time.Duration(i)*time.Minute),
			i+1, llmSpec{Model: "claude-sonnet-4-5", Cost: 1, Input: 10, Output: 2}))
	}
	f.write(store.Batch{Events: recs})
	id := f.sessionID(vendorClaude, "s-long")

	tests := []struct {
		name      string
		limit     int
		wantLimit int
		wantTurns int
		truncated bool
	}{
		{name: "기본 상한", limit: 0, wantLimit: defaultSessionTurns, wantTurns: totalTurns},
		{name: "상한이 턴 수보다 작으면 잘린다", limit: 2, wantLimit: 2, wantTurns: 2, truncated: true},
		{name: "상한이 턴 수와 같으면 자르지 않는다", limit: totalTurns,
			wantLimit: totalTurns, wantTurns: totalTurns},
		{name: "상한을 넘겨 요청하면 상한으로 자른다", limit: maxSessionTurns + 500,
			wantLimit: maxSessionTurns, wantTurns: totalTurns},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, err := f.reader.SessionMetrics(context.Background(),
				SessionMetricsQuery{SessionID: id, TurnLimit: tc.limit})
			if err != nil {
				t.Fatalf("SessionMetrics: %v", err)
			}
			if m.TurnLimit != tc.wantLimit {
				t.Errorf("TurnLimit = %d, want %d — 상한이 응답에 없으면 화면이 잘림을 설명할 수 없다",
					m.TurnLimit, tc.wantLimit)
			}
			if len(m.Turns) != tc.wantTurns {
				t.Errorf("턴 = %d, want %d", len(m.Turns), tc.wantTurns)
			}
			if m.TurnsTruncated != tc.truncated {
				t.Errorf("TurnsTruncated = %v, want %v", m.TurnsTruncated, tc.truncated)
			}
			// 잘려도 상단 값은 전체다.
			if m.Totals.TurnCount != totalTurns || m.Totals.LLMCalls != totalTurns {
				t.Errorf("상단 값이 잘림에 영향받았다: %+v", m.Totals)
			}
			if want := pricing.NanoUSD(totalTurns) * 1_000_000_000; m.Totals.Cost.Total.NanoUSD != want {
				t.Errorf("비용 = %d nano, want %d", m.Totals.Cost.Total.NanoUSD, want)
			}
		})
	}
}

// 인수조건: nullable 토큰·비용·시간 필드를 안전하게 집계한다.
//
// NULL 은 0 이 아니다. 비용을 모르는 호출을 0 으로 눕히면 "무료 호출" 과 같아지고,
// 시각을 모르는 턴을 0 으로 눕히면 1970 년에 시작한 턴이 된다.
func TestSessionMetricsHandlesNullableColumns(t *testing.T) {
	f := newFixture(t)
	f.write(store.Batch{
		Sessions: []session.Session{newSession("s-null", metricsAt, func(s *session.Session) {
			// 활동 시간을 한 번도 관측하지 못한 세션이다. 0초와 다르다.
			s.ActiveSeconds = 0
		})},
		Events: []store.EventRecord{
			llmRecord("s-null", "t-null", metricsAt, 1, llmSpec{Model: "claude-sonnet-4-5", Cost: 1, Input: 10, Output: 2}),
			llmRecord("s-null", "t-null", metricsAt, 2, llmSpec{Model: "claude-sonnet-4-5", Cost: 3, Input: 5, Output: 1}),
			toolRecord("s-null", "t-null", "call-null", metricsAt, 3, toolSpec{ToolName: "Read"}),
		},
	})
	id := f.sessionID(vendorClaude, "s-null")

	// 두 번째 호출의 수치를 전부 비운다 — 벤더가 아무것도 보고하지 않은 호출이다.
	execSQL(t, f, `UPDATE llm_calls SET cost_usd = NULL, input_tokens = NULL,
	  output_tokens = NULL, cache_read_tokens = NULL, cache_write_tokens = NULL,
	  duration_ms = NULL, model = NULL
	  WHERE turn_id IN (SELECT id FROM turns WHERE session_id = ?) AND cost_usd = 3`, id)
	// 활동 시간과 종료 시각을 관측하지 못한 상태로 되돌린다.
	execSQL(t, f, `UPDATE sessions SET active_time_sec = NULL, ended_at = NULL WHERE id = ?`, id)

	m, err := f.reader.SessionMetrics(context.Background(), SessionMetricsQuery{SessionID: id})
	if err != nil {
		t.Fatalf("SessionMetrics: %v", err)
	}
	if !m.Found {
		t.Fatal("Found = false")
	}

	if m.ActiveSeconds != nil {
		t.Errorf("ActiveSeconds = %v, want null — 미관측을 0초로 눕히면 안 된다", *m.ActiveSeconds)
	}
	if m.EndedAt != nil || m.Status != StatusRunning {
		t.Errorf("ended_at 이 NULL 이면 running 이다 (ADR 0009): %+v / %s", m.EndedAt, m.Status)
	}
	// 남은 호출의 토큰만 세야 한다. NULL 이 0 으로 섞이면 이 값이 그대로 있지만,
	// 아래 카운터가 "값이 없었다" 를 따로 말한다.
	if m.Totals.Tokens.Input != 10 || m.Totals.Tokens.Output != 2 {
		t.Errorf("토큰 = %d/%d, want 10/2", m.Totals.Tokens.Input, m.Totals.Tokens.Output)
	}
	if m.Totals.LLMCalls != 2 {
		t.Errorf("LLMCalls = %d, want 2 — 수치가 없어도 호출은 일어났다", m.Totals.LLMCalls)
	}
	if m.Totals.Cost.ReportedCalls != 1 || m.Totals.Cost.UnavailableCalls != 1 {
		t.Errorf("비용 출처 = %+v, want 보고 1 · 불가 1", m.Totals.Cost)
	}
	if m.Totals.Cost.Complete {
		t.Error("Complete = true — 비용을 모르는 호출이 있으면 합계는 하한이다")
	}
	if m.Totals.Cost.Total.NanoUSD != 1_000_000_000 {
		t.Errorf("비용 = %d nano, want 1 USD", m.Totals.Cost.Total.NanoUSD)
	}
	// duration_ms 를 아무도 보고하지 않았다. 0 은 "0ms" 가 아니라 "더할 것이 없었다" 다.
	if m.Totals.ToolDurationMS != 0 || m.Totals.LLMDurationMS != 0 {
		t.Errorf("duration = %d/%d, want 0", m.Totals.LLMDurationMS, m.Totals.ToolDurationMS)
	}
	if len(m.Turns) != 1 {
		t.Fatalf("턴 = %d, want 1", len(m.Turns))
	}
	// turns.ended_at 은 쓰기 경로가 채우지 않는다. 모르는 길이는 null 이다.
	if m.Turns[0].EndedAt != nil || m.Turns[0].DurationSeconds != nil {
		t.Errorf("턴 길이 = %v, want null", m.Turns[0].DurationSeconds)
	}
}

// 캐시 절감액은 가격표 서브태스크(internal/pricing)의 계산기를 쓴다. 여기서 다시 만들지 않는다.
//
// claude-sonnet-4-5 의 단가는 입력 3.00 · 캐시 읽기 0.30 · 캐시 쓰기 3.75 USD/MTok 이다.
// 100만 토큰씩 읽고 쓰면 읽기는 2.70 USD 를 아끼고 쓰기는 0.75 USD 를 더 쓴다 —
// **쓰기 절감액이 음수인 것은 오류가 아니라 다음 호출의 읽기 절감을 사는 선투자다.**
func TestSessionMetricsUsesPricingForCacheSavings(t *testing.T) {
	f := newFixture(t)
	f.write(store.Batch{Events: []store.EventRecord{
		llmRecord("s-cache", "t-cache", metricsAt, 1, llmSpec{
			Model: "claude-sonnet-4-5", Input: 1000, Output: 100,
			CacheRead: 1_000_000, CacheWrit: 1_000_000,
		}),
	}})
	id := f.sessionID(vendorClaude, "s-cache")
	// 보고 비용을 지운다 — 토큰 단가로 추정하는 경로를 지나가게 한다.
	execSQL(t, f, `UPDATE llm_calls SET cost_usd = NULL
	  WHERE turn_id IN (SELECT id FROM turns WHERE session_id = ?)`, id)

	m, err := f.reader.SessionMetrics(context.Background(), SessionMetricsQuery{SessionID: id})
	if err != nil {
		t.Fatalf("SessionMetrics: %v", err)
	}
	saving := m.Totals.CacheSavings
	switch {
	case !saving.Complete || saving.AvailableCalls != 1:
		t.Fatalf("절감액 = %+v, want 1건 계산됨", saving)
	case saving.Read.NanoUSD != 2_700_000_000:
		t.Errorf("읽기 절감 = %d nano, want 2.7 USD", saving.Read.NanoUSD)
	case saving.Write.NanoUSD != -750_000_000:
		t.Errorf("쓰기 절감 = %d nano, want -0.75 USD (쓰기가 입력보다 비싸다)", saving.Write.NanoUSD)
	case saving.Total.NanoUSD != 1_950_000_000:
		t.Errorf("절감액 합 = %d nano, want 1.95 USD", saving.Total.NanoUSD)
	case saving.Total.USD != 1.95:
		t.Errorf("표시용 USD = %v, want 1.95", saving.Total.USD)
	}

	// 비용은 추정으로 잡히고 절감액과 **섞이지 않는다.**
	if m.Totals.Cost.EstimatedCalls != 1 || m.Totals.Cost.ReportedCalls != 0 {
		t.Errorf("비용 출처 = %+v, want 추정 1건", m.Totals.Cost)
	}
	// 입력 1000 × 3000 + 출력 100 × 15000 + 캐시읽기 1e6 × 300 + 캐시쓰기 1e6 × 3750 nano.
	const wantCost = 1000*3000 + 100*15000 + 1_000_000*300 + 1_000_000*3750
	if m.Totals.Cost.Total.NanoUSD != wantCost {
		t.Errorf("비용 = %d nano, want %d", m.Totals.Cost.Total.NanoUSD, wantCost)
	}
	// 어느 판의 가격표로 계산했는지 응답에 남아야 되짚을 수 있다.
	if m.PricingTableVersion != pricing.DefaultVersion ||
		m.PricingEffectiveDate != pricing.DefaultEffectiveDate {
		t.Errorf("가격표 = %s/%s", m.PricingTableVersion, m.PricingEffectiveDate)
	}
}

// 모르는 모델은 추측하지 않는다. 비용도 절감액도 unavailable 이고, 그 사실이 카운터에 남는다.
func TestSessionMetricsMarksUnknownModelIncomplete(t *testing.T) {
	f := newFixture(t)
	f.write(store.Batch{Events: []store.EventRecord{
		llmRecord("s-unknown", "t-unknown", metricsAt, 1, llmSpec{
			Model: "claude-opus-9", Input: 100, Output: 10, CacheRead: 50,
		}),
	}})
	id := f.sessionID(vendorClaude, "s-unknown")
	execSQL(t, f, `UPDATE llm_calls SET cost_usd = NULL
	  WHERE turn_id IN (SELECT id FROM turns WHERE session_id = ?)`, id)

	m, err := f.reader.SessionMetrics(context.Background(), SessionMetricsQuery{SessionID: id})
	if err != nil {
		t.Fatalf("SessionMetrics: %v", err)
	}
	if m.Totals.Cost.UnavailableCalls != 1 || m.Totals.Cost.Complete {
		t.Errorf("비용 = %+v, want 불가 1건", m.Totals.Cost)
	}
	if m.Totals.CacheSavings.UnavailableCalls != 1 || m.Totals.CacheSavings.Complete {
		t.Errorf("절감액 = %+v, want 불가 1건", m.Totals.CacheSavings)
	}
	if m.Totals.CacheSavings.Total.NanoUSD != 0 {
		t.Errorf("모르는 모델의 절감액이 0 이 아니다: %d", m.Totals.CacheSavings.Total.NanoUSD)
	}
	// 토큰은 그대로 세야 한다 — 비용을 모르는 것과 토큰을 모르는 것은 다르다.
	if m.Totals.Tokens.Input != 100 || m.Totals.Tokens.CacheRead != 50 {
		t.Errorf("토큰 = %+v", m.Totals.Tokens)
	}
}

// 공개 응답 타입의 json 태그가 곧 TS 필드명이다 (ADR 0004).
func TestSessionMetricsJSONTagsAreSnakeCase(t *testing.T) {
	for _, v := range []any{
		SessionMetricsQuery{}, SessionMetrics{}, SessionTotals{},
		TurnMetrics{}, TurnTotals{}, TokenTotals{}, CostTotals{}, SavingsTotals{},
	} {
		assertSnakeCaseTags(t, v)
	}
}
