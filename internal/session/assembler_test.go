package session

import (
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/event"
)

// 유휴 임계값 경계. 임계값이 한쪽으로 밀리면 진행 중 세션이 조기 마감돼 "3 agents active"가
// 0 이 되거나, 끝난 세션이 영원히 running 으로 남는다.
// idleSec 은 기본 유휴 임계값(초)이다. 테스트가 값을 하드코딩하면 임계값을 조정할 때마다
// 무관한 테스트가 함께 깨진다.
const idleSec = int64(DefaultIdleThreshold / time.Second)

func TestUnsetSuccessDoesNotCountAsError(t *testing.T) {
	const start = 1_700_000_000
	a := New()
	a.Add(logEv("s1", "claude_code.tool_result", start, tool("Read")))   // 미상
	a.Add(logEv("s1", "claude_code.tool_result", start+1, tool("Read"))) // 미상
	a.Add(logEv("s1", "claude_code.tool_result", start+2, tool("Bash"), success(true)))
	a.Add(logEv("s1", "claude_code.tool_result", start+3, tool("Bash"), success(false)))

	s, _ := a.Session("s1")
	if s.ToolCalls != 4 {
		t.Fatalf("tool_calls = %d, want 4", s.ToolCalls)
	}
	if s.ToolErrors != 1 {
		t.Fatalf("tool_errors = %d, want 1 (미상 2건이 실패로 세어졌다)", s.ToolErrors)
	}
	// 타임라인에도 미상이 실패로 굳지 않아야 한다.
	if _, ok := s.Tools[0].Success.Get(); ok {
		t.Error("미상 이벤트의 Success 가 설정된 값으로 바뀜")
	}
}

func TestToolDecisionsAndTimeline(t *testing.T) {
	const start = 1_700_000_000
	a := New()
	a.Add(logEv("s1", "claude_code.tool_decision", start, tool("Edit"), decide("accept")))
	a.Add(logEv("s1", "claude_code.tool_result", start+1, tool("Edit"), success(true), target("/repo/a.go")))
	a.Add(logEv("s1", "claude_code.tool_decision", start+2, tool("Bash"), decide("reject")))

	s, _ := a.Session("s1")
	if s.ToolRejects != 1 {
		t.Fatalf("tool_rejects = %d, want 1", s.ToolRejects)
	}
	if s.ToolCalls != 1 {
		t.Fatalf("tool_calls = %d, want 1 (결정 이벤트는 호출이 아니다)", s.ToolCalls)
	}
	if len(s.Tools) != 2 {
		t.Fatalf("타임라인 %d건, want 2 (수락된 결과 + 거부)", len(s.Tools))
	}
	if s.Tools[0].Decision != "accept" {
		t.Errorf("수락 결정이 결과 행에 붙지 않음: %+v", s.Tools[0])
	}
	if s.Tools[0].Action != ActionEdit || s.Tools[0].TargetName != "a.go" {
		t.Errorf("action·target 파생 실패: %+v", s.Tools[0])
	}
	if s.Tools[1].Decision != "reject" {
		t.Errorf("거부가 타임라인에서 사라짐: %+v", s.Tools[1])
	}
	// 전체 경로가 어디에도 남지 않아야 한다 (ADR 0003).
	for _, te := range s.Tools {
		if te.TargetName == "/repo/a.go" {
			t.Fatal("전체 경로가 target_name 에 들어감")
		}
	}
}

// 총량은 메트릭, 건수는 로그. 같은 사실이 양쪽으로 오므로 출처를 하나로 고르지 않으면
// 비용과 토큰이 두 배가 된다.
func TestCountersComeFromOneSourceEach(t *testing.T) {
	const start = 1_700_000_000
	a := New()
	// 로그 api_request 가 cost·token 을 실어 오지만 세션 총량에는 반영되지 않는다.
	req := logEv("s1", "claude_code.api_request", start, attempt(2), response(120))
	req.Event.Measure.CostUSD = event.Some(9.9)
	req.Event.Measure.InputTokens = event.Some(int64(999))
	a.Add(req)
	a.Add(logEv("s1", "claude_code.api_error", start+1))
	a.Add(metricEv("s1", "claude_code.cost.usage", start+2, 0.25))
	a.Add(metricEv("s1", "claude_code.token.usage", start+2, 100, typ("input")))
	a.Add(metricEv("s1", "claude_code.token.usage", start+2, 40, typ("cacheRead")))
	a.Add(metricEv("s1", "claude_code.active_time.total", start+3, 12.5))

	s, _ := a.Session("s1")
	switch {
	case s.CostUSD != 0.25:
		t.Errorf("cost = %v, want 0.25 (로그의 cost 가 이중 집계됨)", s.CostUSD)
	case s.InputTokens != 100:
		t.Errorf("input_tokens = %d, want 100", s.InputTokens)
	case s.CacheReadTokens != 40:
		t.Errorf("cache_read_tokens = %d, want 40 (type 표기 정규화 실패)", s.CacheReadTokens)
	case s.APIRequests != 1 || s.APIErrors != 1:
		t.Errorf("api_requests=%d api_errors=%d, want 1/1", s.APIRequests, s.APIErrors)
	case s.Retries != 1:
		t.Errorf("retries = %d, want 1 (attempt=2)", s.Retries)
	case s.Responses != 1:
		t.Errorf("responses = %d, want 1", s.Responses)
	case s.ActiveSeconds != 12.5:
		t.Errorf("active_seconds = %v, want 12.5", s.ActiveSeconds)
	}
}

// 누적 판정은 event.CumulativeState.Step 이 소유하고 rollup 도 같은 것을 쓴다.
// 여기서는 세션 쪽 배선(기준점 주입·계열 키·폐기 카운트)이 그 규칙을 그대로 통과시키는지 본다.
func TestTemporalityHandling(t *testing.T) {
	const start = 1_700_000_000

	tests := []struct {
		name      string
		values    []float64
		temp      event.Temporality
		mods      []func(*Input)
		want      float64
		discarded int64
	}{
		{name: "delta 는 합산", values: []float64{0.1, 0.2, 0.3},
			temp: event.TemporalityDelta, want: 0.6},

		// start_time 이 없으면 이 계열이 데몬보다 먼저 쌓이고 있었는지 알 수 없다.
		// 이미 저장된 구간을 다시 더하느니 첫 관측을 기준선으로만 잡는다 — rollup 과 같은 판정.
		{name: "cumulative + start_time 없음 → 첫 관측은 기준선", values: []float64{0.1, 0.3, 0.6},
			temp: event.TemporalityCumulative, want: 0.5},

		// 계열이 조립기가 보기 시작한 뒤에 시작했으면 이전 포인트가 존재하지 않았다.
		// 값 전체가 이 세션 것이다.
		{name: "cumulative + 관측 시작 이후 계열 → 첫 관측도 전부", values: []float64{0.1, 0.3, 0.6},
			temp: event.TemporalityCumulative, mods: []func(*Input){startedAt(start)}, want: 0.6},

		{name: "cumulative 리셋(start_time 없음)", values: []float64{0.5, 0.2},
			temp: event.TemporalityCumulative, want: 0.2},

		{name: "unspecified 는 폐기", values: []float64{0.1, 0.2},
			temp: event.TemporalityUnspecified, discarded: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := New()
			for i, v := range tt.values {
				mods := append([]func(*Input){temporality(tt.temp)}, tt.mods...)
				a.Add(metricEv("s1", "claude_code.cost.usage", start+int64(i), v, mods...))
			}
			s, _ := a.Session("s1")
			if diff := s.CostUSD - tt.want; diff > 1e-9 || diff < -1e-9 {
				t.Fatalf("cost = %v, want %v", s.CostUSD, tt.want)
			}
			if s.Diag.DiscardedPoints != tt.discarded {
				t.Fatalf("DiscardedPoints = %d, want %d", s.Diag.DiscardedPoints, tt.discarded)
			}
		})
	}
}

// 수집 구간이 바뀌면 값이 줄지 않아도 리셋이다 — 벤더가 재시작한 뒤 다음 내보내기까지
// 직전 값을 이미 넘어선 경우. 값의 증감만 보던 예전 규칙은 이걸 차분으로 읽어
// 재시작 이후 누적분을 통째로 잃었다.
func TestCumulativeResetDetectedByStartTime(t *testing.T) {
	const start = 1_700_000_000

	a := New()
	a.Add(metricEv("s1", "claude_code.cost.usage", start, 1.0,
		temporality(event.TemporalityCumulative), startedAt(start)))
	a.Add(metricEv("s1", "claude_code.cost.usage", start+60, 1.5,
		temporality(event.TemporalityCumulative), startedAt(start)))
	a.Add(metricEv("s1", "claude_code.cost.usage", start+120, 1.8,
		temporality(event.TemporalityCumulative), startedAt(start+90)))

	s, _ := a.Session("s1")
	// 1.0(첫 관측 전부) + 0.5(차이) + 1.8(재시작 후 누적 전부)
	if diff := s.CostUSD - 3.3; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("cost = %v, want 3.3 — start_time 변화를 리셋으로 못 잡았다", s.CostUSD)
	}
}

// 구간이 그대로인데 값이 줄면 리셋이 아니다. 값 전체를 더하면 그 양이 두 번 들어간다.
func TestCumulativeDecreaseWithinSameStartIsNotReset(t *testing.T) {
	const start = 1_700_000_000

	a := New()
	for i, v := range []float64{1.0, 1.5, 1.2, 2.0} { // 세 번째가 순서 뒤집힘
		a.Add(metricEv("s1", "claude_code.cost.usage", start+int64(i)*60, v,
			temporality(event.TemporalityCumulative), startedAt(start)))
	}

	s, _ := a.Session("s1")
	// 1.0 + 0.5 + 0 + 0.5 = 2.0 — 마지막 누적값과 정확히 같다.
	if diff := s.CostUSD - 2.0; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("cost = %v, want 2.0 (마지막 누적값)", s.CostUSD)
	}
}

// 속성만 다른 두 계열이 섞이면 서로의 직전값을 덮어써 차이가 번갈아 음수가 되고
// 매 포인트가 리셋으로 오판된다. rollup 은 속성 전체로 계열을 가르므로 세션도 같아야 한다.
func TestCumulativeSeriesAreKeyedByAttributes(t *testing.T) {
	const start = 1_700_000_000

	a := New()
	for i, tc := range []struct {
		model string
		value float64
	}{
		{"opus", 1.0}, {"haiku", 0.1}, {"opus", 1.6}, {"haiku", 0.3},
	} {
		in := metricEv("s1", "claude_code.cost.usage", start+int64(i), tc.value,
			temporality(event.TemporalityCumulative), startedAt(start))
		in.Event.Attr.Model = tc.model
		a.Add(in)
	}

	s, _ := a.Session("s1")
	// opus 1.0 + 0.6, haiku 0.1 + 0.2 = 1.9. 계열이 섞이면 이 값이 나오지 않는다.
	if diff := s.CostUSD - 1.9; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("cost = %v, want 1.9 — 계열이 섞였다", s.CostUSD)
	}
}

// 디코더가 token.usage 를 종류별 컬럼에 채워 줄 수도, Value+type 으로 줄 수도 있다.
// 둘 중 하나만 타야 토큰이 두 배가 되지 않는다.
func TestTokenMetricAcceptsBothShapes(t *testing.T) {
	const start = 1_700_000_000

	typed := metricEv("s1", "claude_code.token.usage", start, 0)
	typed.Event.Measure.Value = event.Opt[float64]{}
	typed.Event.Measure.InputTokens = event.Some(int64(70))
	typed.Event.Measure.OutputTokens = event.Some(int64(30))
	typed.Event.Measure.CacheCreationTokens = event.Some(int64(5))

	a := New()
	a.Add(typed)
	a.Add(metricEv("s1", "claude_code.token.usage", start+1, 12, typ("output")))

	s, _ := a.Session("s1")
	switch {
	case s.InputTokens != 70:
		t.Errorf("input = %d, want 70", s.InputTokens)
	case s.OutputTokens != 42:
		t.Errorf("output = %d, want 42 (70+12 두 형태가 모두 반영돼야 한다)", s.OutputTokens)
	case s.CacheCreationTokens != 5:
		t.Errorf("cache_creation = %d, want 5", s.CacheCreationTokens)
	}
}

// 벤더 접두가 없는 이름과 모르는 이름도 세션을 깨지 않아야 한다.
func TestUnprefixedAndUnknownEventNames(t *testing.T) {
	const start = 1_700_000_000
	a := New()
	a.Add(logEv("s1", "user_prompt", start))                  // 접두 없음
	a.Add(logEv("s1", "codex.something.unheard_of", start+1)) // 모르는 이름

	s, _ := a.Session("s1")
	if s.Prompts != 1 {
		t.Errorf("prompts = %d, want 1 (접두 없는 이름을 못 알아봄)", s.Prompts)
	}
	if s.LastEventAt != start+1 {
		t.Errorf("모르는 이름이 last_event_at 을 갱신하지 않음: %d", s.LastEventAt)
	}
}
func TestMCPUsage(t *testing.T) {
	const start = 1_700_000_000
	a := New()
	a.Add(logEv("s1", "claude_code.mcp.connection", start, mcp("github"), success(true)))
	a.Add(logEv("s1", "claude_code.mcp.connection", start, mcp("postgres"), success(false)))
	a.Add(logEv("s1", "claude_code.mcp.connection", start+1, mcp("postgres"), success(false)))
	a.Add(logEv("s1", "claude_code.tool_result", start+2, tool("mcp__sentry__list"), mcp("sentry"), success(true)))

	s, _ := a.Session("s1")
	if len(s.MCP) != 3 {
		t.Fatalf("MCP 행 %d개, want 3", len(s.MCP))
	}
	// 서버 이름 오름차순
	byName := map[string]MCPUsage{}
	for _, m := range s.MCP {
		byName[m.ServerName] = m
	}
	// "연결됐지만 한 번도 안 쓴 서버" 카드의 원천
	if g := byName["github"]; !g.Connected || g.ToolCalls != 0 {
		t.Errorf("github = %+v, want connected/0 calls", g)
	}
	if p := byName["postgres"]; p.ConnectFailures != 2 {
		t.Errorf("postgres connect_failures = %d, want 2", p.ConnectFailures)
	}
	if s := byName["sentry"]; !s.Connected || s.ToolCalls != 1 {
		t.Errorf("sentry = %+v, want connected/1 call", s)
	}
}

func TestAddRejectsUnusableEvents(t *testing.T) {
	const start = 1_700_000_000

	noSession := logEv("", "claude_code.user_prompt", start)
	invalid := logEv("s1", "", start)

	a := New()
	if a.Add(noSession) {
		t.Error("session.id 없는 이벤트가 받아들여짐")
	}
	if a.Add(invalid) {
		t.Error("Validate 를 통과 못 하는 이벤트가 받아들여짐")
	}
	if len(a.Snapshot()) != 0 {
		t.Fatal("거부된 이벤트로 세션이 만들어짐")
	}
}

func TestDurationAndSnapshotOrdering(t *testing.T) {
	const start = 1_700_000_000
	a := New()
	a.Add(logEv("late", "claude_code.user_prompt", start+100))
	a.Add(logEv("early", "claude_code.user_prompt", start))
	a.Add(logEv("early", "claude_code.api_request", start+30))

	got := a.Snapshot()
	if len(got) != 2 || got[0].SessionID != "early" {
		t.Fatalf("스냅샷이 started_at 오름차순이 아님: %+v", got)
	}
	if got[0].DurationMS != 30_000 {
		t.Fatalf("duration_ms = %d, want 30000", got[0].DurationMS)
	}
}

func TestPruneDropsSessionsByLastActivity(t *testing.T) {
	const start = 1_700_000_000
	a := New()
	a.Add(logEv("done", "claude_code.user_prompt", start))
	a.Add(logEv("live", "claude_code.user_prompt", start+idleSec))

	if n := a.Prune(start + 1); n != 1 {
		t.Fatalf("Prune = %d, want 1", n)
	}
	if _, ok := a.Session("done"); ok {
		t.Error("오래된 세션이 남아 있음")
	}
	if _, ok := a.Session("live"); !ok {
		t.Error("진행 중 세션이 지워짐")
	}
}

func TestAssembleBatchHelper(t *testing.T) {
	const start = 1_700_000_000
	got := Assemble([]Input{
		logEv("s1", "claude_code.user_prompt", start, prompt("리시버 붙이기")),
	})

	// 마감은 담지 않는다 — 생명주기는 DB 가 소유한다 (ADR 0021).
	s := only(t, got)
	if s.Status != StatusRunning || s.Prompts != 1 {
		t.Fatalf("Assemble 결과가 예상과 다름: %+v", s)
	}
}

// 훅만 보고 조립기가 상태를 만들면, 활동 관측이 없는 세션의 스냅샷이 생명주기 정본
// 행세를 하며 스윕이 닫은 DB 행을 매 틱 되살린다. 행은 store 가 직접 만든다.
func TestStartLifecycleDoesNotCreateState(t *testing.T) {
	a := New()
	a.StartLifecycle("hook-only", 1_000)

	if got := a.Snapshot(); len(got) != 0 {
		t.Fatalf("스냅샷 = %d개, want 0: %+v", len(got), got)
	}
}
