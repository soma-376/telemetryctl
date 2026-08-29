package dashboard

import (
	"context"
	"testing"

	"github.com/your-org/pulsemetry/internal/event"
	"github.com/your-org/pulsemetry/internal/store"
)

// payload 가 **명시한** 재시도만 센다. 이 표가 계약이다.
func TestRetriesInPayload(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    int64
	}{
		{name: "빈 payload", payload: "", want: 0},
		{name: "객체가 아니면 0", payload: `[1,2,3]`, want: 0},
		{name: "재시도 정보가 없으면 0", payload: `{"tool_name":"Edit","success":true}`, want: 0},

		{name: "첫 시도는 재시도가 아니다", payload: `{"attempt":1}`, want: 0},
		{name: "3번째 시도는 재시도 2회", payload: `{"attempt":3}`, want: 2},
		{name: "attempt_number 도 같은 뜻", payload: `{"attempt_number":2}`, want: 1},
		{name: "0번째 시도는 세지 않는다", payload: `{"attempt":0}`, want: 0},
		{name: "음수 시도 번호는 세지 않는다", payload: `{"attempt":-2}`, want: 0},

		{name: "retry_count 는 횟수 그대로", payload: `{"retry_count":4}`, want: 4},
		{name: "retries 도 횟수", payload: `{"retries":2}`, want: 2},

		{name: "is_retry 참이면 1회", payload: `{"is_retry":true}`, want: 1},
		{name: "is_retry 거짓이면 0", payload: `{"is_retry":false}`, want: 0},
		{name: "retry 참이면 1회", payload: `{"retry":true}`, want: 1},
		{name: "retried 참이면 1회", payload: `{"retried":true}`, want: 1},
		{
			// 숫자를 참/거짓으로 읽으면 "1회" 인지 "참" 인지 알 수 없다. 횟수는 retry_count 로 온다.
			name: "retry 가 숫자면 flag 로 읽지 않는다", payload: `{"retry":1}`, want: 0,
		},

		{
			name:    "OTLP/JSON 왕복의 문자열 정수도 받는다",
			payload: `{"attempt":"4"}`, want: 3,
		},
		{name: "문자열 참", payload: `{"is_retry":"true"}`, want: 1},
		{name: "소수점 표기의 정수", payload: `{"retry_count":3.0}`, want: 3},
		{name: "정수가 아닌 값은 무시", payload: `{"retry_count":3.5}`, want: 0},
		{name: "숫자가 아닌 문자열은 무시", payload: `{"attempt":"두 번째"}`, want: 0},

		{
			name:    "attributes 안도 본다",
			payload: `{"attributes":{"attempt":3}}`, want: 2,
		},
		{
			// 더 깊은 곳을 뒤지면 어디에 무엇을 넣어도 세지는 셈이라 계약이 아니게 된다.
			name:    "두 겹보다 깊은 곳은 보지 않는다",
			payload: `{"body":{"attributes":{"attempt":9}}}`, want: 0,
		},
		{
			// 같은 사실의 두 표기다. 더하면 정확히 두 배가 된다.
			name:    "여러 키가 있으면 가장 큰 값 하나",
			payload: `{"attempt":3,"retry_count":2,"is_retry":true}`, want: 2,
		},
		{
			name:    "최상위와 attributes 중 큰 값",
			payload: `{"retry_count":1,"attributes":{"retry_count":5}}`, want: 5,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := retriesInPayload([]byte(tc.payload)); got != tc.want {
				t.Errorf("retriesInPayload(%s) = %d, want %d", tc.payload, got, tc.want)
			}
		})
	}
}

// 티켓의 구현 경계: **같은 도구가 반복됐다는 이유로 재시도를 추측하지 않는다.**
//
// 실패한 Edit 뒤에 같은 Edit 가 두 번 더 오는, 사람이 보면 "재시도" 라고 부를 모양을
// 그대로 만든다. payload 에 명시가 없으므로 재시도는 0 이어야 한다.
func TestSessionMetricsDoesNotInferRetryFromRepeatedTools(t *testing.T) {
	f := newFixture(t)
	f.write(store.Batch{Events: []store.EventRecord{
		toolRecord("s-repeat", "t-repeat", "call-r1", metricsAt, 1, toolSpec{
			ToolName: "Edit", Success: event.Some(false), ErrorType: "conflict",
			Target: workspaceA + "/apply.go",
		}),
		toolRecord("s-repeat", "t-repeat", "call-r2", metricsAt, 2, toolSpec{
			ToolName: "Edit", Success: event.Some(false), ErrorType: "conflict",
			Target: workspaceA + "/apply.go",
		}),
		toolRecord("s-repeat", "t-repeat", "call-r3", metricsAt, 3, toolSpec{
			ToolName: "Edit", Success: event.Some(true),
			Target: workspaceA + "/apply.go",
		}),
	}})

	m, err := f.reader.SessionMetrics(context.Background(), SessionMetricsQuery{
		SessionID: f.sessionID(vendorClaude, "s-repeat"),
	})
	if err != nil {
		t.Fatalf("SessionMetrics: %v", err)
	}
	if m.Totals.Retries != 0 {
		t.Errorf("Retries = %d, want 0 — 같은 도구의 반복을 재시도로 추측했다", m.Totals.Retries)
	}
	if m.Totals.ToolCalls != 3 || m.Totals.ToolErrors != 2 {
		t.Errorf("툴 = %d건 · 실패 %d건, want 3/2", m.Totals.ToolCalls, m.Totals.ToolErrors)
	}
}

// payload 를 실제로 심어 조회 경로 전체를 지나가게 한다.
//
// 쓰기 경로는 events.payload 를 항상 NULL 로 두므로(store/resolve.go) SQL 로 직접
// 심는다 — store 의 purge 테스트(PROJ-86)가 쓰는 것과 같은 방법이다. CHECK 가
// json_valid(payload, 8) 이라 텍스트 JSON 이 아니라 jsonb(?) 로 바인딩해야 한다.
func TestSessionMetricsCountsRetriesFromPayload(t *testing.T) {
	f := newFixture(t)
	f.write(store.Batch{Events: []store.EventRecord{
		llmRecord("s-retry", "t-a", metricsAt, 1, llmSpec{Model: "claude-sonnet-4-5", Cost: 1}),
		llmRecord("s-retry", "t-b", metricsAt, 2, llmSpec{Model: "claude-sonnet-4-5", Cost: 1}),
		llmRecord("s-retry", "t-b", metricsAt, 3, llmSpec{Model: "claude-sonnet-4-5", Cost: 1}),
	}})
	id := f.sessionID(vendorClaude, "s-retry")

	// seq 는 턴 안의 도착 순서다. 턴별로 서로 다른 payload 를 심어 귀속을 확인한다.
	plant := func(turnKey string, seq int64, payload string) {
		execSQL(t, f, `UPDATE events SET payload = jsonb(?)
		  WHERE seq = ? AND turn_id = (SELECT id FROM turns WHERE session_id = ? AND turn_key = ?)`,
			payload, seq, id, turnKey)
	}
	plant("t-a", 1, `{"attempt":2}`)                               // 재시도 1회
	plant("t-b", 1, `{"attributes":{"retry_count":3}}`)            // 재시도 3회
	plant("t-b", 2, `{"tool_name":"Edit","http.status_code":200}`) // 명시 없음 → 0

	m, err := f.reader.SessionMetrics(context.Background(), SessionMetricsQuery{SessionID: id})
	if err != nil {
		t.Fatalf("SessionMetrics: %v", err)
	}
	if m.Totals.Retries != 4 {
		t.Fatalf("Retries = %d, want 4 (1 + 3)", m.Totals.Retries)
	}
	byKey := map[string]int64{}
	for _, turn := range m.Turns {
		byKey[turn.TurnKey] = turn.Retries
	}
	if byKey["t-a"] != 1 || byKey["t-b"] != 3 {
		t.Errorf("턴별 재시도 = %v, want t-a:1 t-b:3", byKey)
	}
	// 상단 값은 턴별 값의 합이다.
	if m.Totals.Retries != byKey["t-a"]+byKey["t-b"] {
		t.Errorf("상단 %d ≠ 턴 합 %d", m.Totals.Retries, byKey["t-a"]+byKey["t-b"])
	}
}
