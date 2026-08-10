package session

import (
	"strings"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/event"
)

// 유휴 10분 경계. 임계값이 한쪽으로 밀리면 진행 중 세션이 조기 마감돼 "3 agents active"가
// 0 이 되거나, 끝난 세션이 영원히 running 으로 남는다.
func TestIdleThresholdBoundary(t *testing.T) {
	const start = 1_700_000_000

	tests := []struct {
		name    string
		elapsed int64
		want    Status
		ended   bool
	}{
		{"직후", 0, StatusRunning, false},
		{"9분59초", 599, StatusRunning, false},
		{"정확히 10분", 600, StatusCompleted, true},
		{"10분 1초", 601, StatusCompleted, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := New()
			a.Add(logEv("s1", "claude_code.user_prompt", start))

			closed := a.Advance(event.UnixSec(start + tt.elapsed))
			s := only(t, a.Snapshot())

			if s.Status != tt.want {
				t.Fatalf("status = %q, want %q", s.Status, tt.want)
			}
			if got := len(closed) == 1; got != tt.ended {
				t.Fatalf("Advance 가 마감한 세션 수 = %d, 마감 기대 = %v", len(closed), tt.ended)
			}
			end, ok := s.EndedAt.Get()
			if ok != tt.ended {
				t.Fatalf("EndedAt 설정 여부 = %v, want %v", ok, tt.ended)
			}
			// ended_at 은 마지막 이벤트 시각이다. 마감을 감지한 시각으로 두면
			// 모든 세션 소요 시간에 유휴 10분이 유령처럼 붙는다.
			if ok && end != start {
				t.Fatalf("EndedAt = %d, want %d (마지막 이벤트 시각)", end, start)
			}
		})
	}
}

// 임계값은 관측 후 조정할 값이라 바꿀 수 있어야 한다.
func TestIdleThresholdIsConfigurable(t *testing.T) {
	const start = 1_700_000_000
	a := New(WithIdleThreshold(30 * time.Second))
	a.Add(logEv("s1", "claude_code.user_prompt", start))

	if got := a.Advance(start + 29); len(got) != 0 {
		t.Fatalf("29초에 마감됨")
	}
	if got := a.Advance(start + 30); len(got) != 1 {
		t.Fatalf("30초에 마감되지 않음")
	}
}

// 같은 session.id 가 마감 뒤에 다시 등장하는 경우. 사용자가 10분 넘게 생각하다 같은
// 대화를 이어가면 실제로 일어난다.
func TestSessionReappearsAfterClose(t *testing.T) {
	const start = 1_700_000_000
	a := New()

	a.Add(logEv("s1", "claude_code.user_prompt", start))
	a.Add(metricEv("s1", "claude_code.cost.usage", start, 0.5))
	a.Advance(start + 600)

	if s, _ := a.Session("s1"); s.Status != StatusCompleted {
		t.Fatalf("1차 마감 실패: %q", s.Status)
	}

	// 20분 뒤 같은 session.id 로 이벤트가 다시 온다.
	const resume = start + 1200
	a.Add(logEv("s1", "claude_code.user_prompt", resume))
	a.Add(metricEv("s1", "claude_code.cost.usage", resume, 0.25))

	s, _ := a.Session("s1")
	switch {
	case s.Status != StatusRunning:
		t.Fatalf("재등장 후 status = %q, want running", s.Status)
	case s.EndedAt.Valid():
		t.Fatalf("재등장 후에도 EndedAt 이 남아 있음")
	case s.StartedAt != start:
		t.Fatalf("StartedAt 이 바뀜: %d", s.StartedAt)
	case s.LastEventAt != resume:
		t.Fatalf("LastEventAt = %d, want %d", s.LastEventAt, resume)
	case s.Prompts != 2:
		t.Fatalf("prompts = %d, want 2 (수치가 이어서 누적돼야 한다)", s.Prompts)
	case s.CostUSD != 0.75:
		t.Fatalf("cost = %v, want 0.75", s.CostUSD)
	case s.Diag.Reopens != 1:
		t.Fatalf("Reopens = %d, want 1", s.Diag.Reopens)
	}

	// 2차 마감은 새 마지막 이벤트 기준이다.
	a.Advance(resume + 600)
	s, _ = a.Session("s1")
	if end, ok := s.EndedAt.Get(); !ok || end != resume {
		t.Fatalf("2차 EndedAt = (%d, %v), want (%d, true)", end, ok, resume)
	}
}

// 마감 뒤 도착한 낙오 이벤트는 세션을 되살리지만 마감 시각을 흔들지 않는다 —
// 다음 Advance 가 같은 ended_at 으로 다시 마감한다.
func TestStragglerEventDoesNotMoveEnd(t *testing.T) {
	const start = 1_700_000_000
	a := New()
	a.Add(logEv("s1", "claude_code.user_prompt", start))
	a.Add(logEv("s1", "claude_code.api_request", start+10))
	a.Advance(start + 700)

	a.Add(logEv("s1", "claude_code.api_request", start+5)) // 배치가 늦게 도착
	if s, _ := a.Session("s1"); s.Status != StatusRunning {
		t.Fatalf("낙오 이벤트로 되살아나지 않음: %q", s.Status)
	}
	a.Advance(start + 700)

	s, _ := a.Session("s1")
	if end, _ := s.EndedAt.Get(); end != start+10 {
		t.Fatalf("EndedAt = %d, want %d", end, start+10)
	}
	if s.Status != StatusCompleted {
		t.Fatalf("status = %q", s.Status)
	}
}

// handoff — 같은 project_hash 에서 30분 내에 다른 벤더 세션이 시작된 경우.
func TestHandoffDetection(t *testing.T) {
	const start = 1_700_000_000

	tests := []struct {
		name      string
		second    []func(*Input)
		gap       int64 // 첫 세션의 마지막 이벤트로부터 두 번째 세션 시작까지
		want      Status
		wantOther Status
	}{
		{
			name:      "다른 벤더가 10분 뒤 시작",
			second:    []func(*Input){vendor("codex"), project("/repo/telemetryctl")},
			gap:       600,
			want:      StatusHandoff,
			wantOther: StatusCompleted,
		},
		{
			name:      "같은 벤더는 handoff 가 아니다",
			second:    []func(*Input){vendor("claude_code"), project("/repo/telemetryctl")},
			gap:       600,
			want:      StatusCompleted,
			wantOther: StatusCompleted,
		},
		{
			name:      "다른 프로젝트는 handoff 가 아니다",
			second:    []func(*Input){vendor("codex"), project("/repo/other")},
			gap:       600,
			want:      StatusCompleted,
			wantOther: StatusCompleted,
		},
		{
			name:      "30분 창 밖은 handoff 가 아니다",
			second:    []func(*Input){vendor("codex"), project("/repo/telemetryctl")},
			gap:       1801,
			want:      StatusCompleted,
			wantOther: StatusCompleted,
		},
		{
			name:      "정확히 30분은 창 안이다",
			second:    []func(*Input){vendor("codex"), project("/repo/telemetryctl")},
			gap:       1800,
			want:      StatusHandoff,
			wantOther: StatusCompleted,
		},
		{
			name:      "project_hash 가 없으면 판정하지 않는다",
			second:    []func(*Input){vendor("codex")},
			gap:       600,
			want:      StatusCompleted,
			wantOther: StatusCompleted,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			first := []func(*Input){project("/repo/telemetryctl")}
			if tt.name == "project_hash 가 없으면 판정하지 않는다" {
				first = nil
			}

			a := New()
			a.Add(logEv("first", "claude_code.user_prompt", start, first...))
			a.Add(logEv("second", "claude_code.user_prompt", start+tt.gap, tt.second...))
			a.Advance(event.UnixSec(start + tt.gap + 600))

			got, _ := a.Session("first")
			if got.Status != tt.want {
				t.Fatalf("first.status = %q, want %q (근거=%q)", got.Status, tt.want, got.Diag.StatusReason)
			}
			if got.Status == StatusHandoff && got.Diag.StatusReason == "" {
				t.Error("handoff 판정 근거가 비어 있음")
			}
			other, _ := a.Session("second")
			if other.Status != tt.wantOther {
				t.Fatalf("second.status = %q, want %q", other.Status, tt.wantOther)
			}
		})
	}
}

func TestHandoffWindowIsConfigurable(t *testing.T) {
	const start = 1_700_000_000
	a := New(WithHandoffWindow(time.Minute))
	a.Add(logEv("first", "claude_code.user_prompt", start, project("/repo/x")))
	a.Add(logEv("second", "claude_code.user_prompt", start+120, vendor("codex"), project("/repo/x")))
	a.Advance(start + 1000)

	if s, _ := a.Session("first"); s.Status != StatusCompleted {
		t.Fatalf("창을 1분으로 줄였는데 2분 뒤 세션이 handoff 로 잡힘: %q", s.Status)
	}
}

// abandoned — 마지막 툴 이벤트가 실패이고 이후 성공이 없는 경우.
func TestAbandonedDetection(t *testing.T) {
	const start = 1_700_000_000

	tests := []struct {
		name   string
		events []Input
		want   Status
	}{
		{
			name: "마지막 툴이 실패",
			events: []Input{
				logEv("s1", "claude_code.tool_result", start, tool("Bash"), success(true)),
				logEv("s1", "claude_code.tool_result", start+5, tool("Bash"), success(false)),
			},
			want: StatusAbandoned,
		},
		{
			name: "실패 뒤 성공하면 마감",
			events: []Input{
				logEv("s1", "claude_code.tool_result", start, tool("Bash"), success(false)),
				logEv("s1", "claude_code.tool_result", start+5, tool("Bash"), success(true)),
			},
			want: StatusCompleted,
		},
		{
			name: "성공 여부 미상은 실패가 아니다",
			events: []Input{
				logEv("s1", "claude_code.tool_result", start, tool("Bash"), success(false)),
				logEv("s1", "claude_code.tool_result", start+5, tool("Bash")),
			},
			want: StatusAbandoned, // 미상은 판정을 바꾸지 않는다 — 마지막 "판정 가능한" 결과가 실패
		},
		{
			name: "툴 이벤트가 없으면 마감",
			events: []Input{
				logEv("s1", "claude_code.user_prompt", start),
			},
			want: StatusCompleted,
		},
		{
			name: "도착 순서가 뒤집혀도 ts 로 판정",
			events: []Input{
				logEv("s1", "claude_code.tool_result", start+5, tool("Bash"), success(false)),
				logEv("s1", "claude_code.tool_result", start, tool("Bash"), success(true)),
			},
			want: StatusAbandoned,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := New()
			for _, e := range tt.events {
				a.Add(e)
			}
			a.Advance(start + 1000)

			s, _ := a.Session("s1")
			if s.Status != tt.want {
				t.Fatalf("status = %q, want %q (근거=%q)", s.Status, tt.want, s.Diag.StatusReason)
			}
			if s.Status == StatusAbandoned && s.Diag.StatusReason == "" {
				t.Error("abandoned 판정 근거가 비어 있음 — ADR 0005 가 근거를 남기라고 요구했다")
			}
		})
	}
}

// Success 미설정 이벤트가 tool_errors 를 부풀리지 않는지. 부풀면 그 수치가 화면에 그대로 나간다.
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

func TestTemporalityHandling(t *testing.T) {
	const start = 1_700_000_000

	tests := []struct {
		name      string
		values    []float64
		temp      event.Temporality
		want      float64
		discarded int64
	}{
		{"delta 는 합산", []float64{0.1, 0.2, 0.3}, event.TemporalityDelta, 0.6, 0},
		{"cumulative 는 차분", []float64{0.1, 0.3, 0.6}, event.TemporalityCumulative, 0.6, 0},
		{"cumulative 리셋", []float64{0.5, 0.2}, event.TemporalityCumulative, 0.7, 0},
		{"unspecified 는 폐기", []float64{0.1, 0.2}, event.TemporalityUnspecified, 0, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := New()
			for i, v := range tt.values {
				a.Add(metricEv("s1", "claude_code.cost.usage", start+int64(i), v, temporality(tt.temp)))
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

// handoff 와 abandoned 가 겹치면 handoff 로 확정하되 근거에는 둘 다 남긴다.
func TestHandoffOutranksAbandonedButKeepsBothReasons(t *testing.T) {
	const start = 1_700_000_000
	a := New()
	a.Add(logEv("first", "claude_code.tool_result", start,
		project("/repo/x"), tool("Bash"), success(false)))
	a.Add(logEv("second", "codex.user_prompt", start+300, vendor("codex"), project("/repo/x")))
	a.Advance(start + 1200)

	s, _ := a.Session("first")
	if s.Status != StatusHandoff {
		t.Fatalf("status = %q, want handoff", s.Status)
	}
	if !strings.Contains(s.Diag.StatusReason, "실패") {
		t.Fatalf("근거에 abandoned 사유가 빠짐: %q", s.Diag.StatusReason)
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

func TestPruneDropsOnlyClosedSessions(t *testing.T) {
	const start = 1_700_000_000
	a := New()
	a.Add(logEv("done", "claude_code.user_prompt", start))
	a.Advance(start + 600)
	a.Add(logEv("live", "claude_code.user_prompt", start+600))

	if n := a.Prune(start + 1); n != 1 {
		t.Fatalf("Prune = %d, want 1", n)
	}
	if _, ok := a.Session("done"); ok {
		t.Error("마감된 세션이 남아 있음")
	}
	if _, ok := a.Session("live"); !ok {
		t.Error("진행 중 세션이 지워짐")
	}
}

func TestAssembleBatchHelper(t *testing.T) {
	const start = 1_700_000_000
	got := Assemble([]Input{
		logEv("s1", "claude_code.user_prompt", start, prompt("리시버 붙이기")),
	}, start+600)

	s := only(t, got)
	if s.Status != StatusCompleted || s.Title != "리시버 붙이기" {
		t.Fatalf("Assemble 결과가 예상과 다름: %+v", s)
	}
}
