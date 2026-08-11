// 이 파일만 외부 테스트 패키지(session_test)다.
//
// rollup 을 import 해야 하는데 session 은 rollup 을 import 하지 않고 앞으로도 하면 안 된다.
// 외부 테스트 패키지에 두면 그 의존이 테스트 바이너리에만 생겨 생산 코드의 의존 그래프가
// 그대로 남는다 (session → event, rollup → event, 순환 없음).
//
// 왜 session 디렉터리인가: 여기가 규칙을 맞춘 쪽이다. 다음에 session 의 누적 처리를 건드리는
// 사람이 같은 디렉터리에서 이 테스트를 만나야 한다. 배선(9단계)이 생기면 그쪽으로 옮겨도 된다.
package session_test

import (
	"math"
	"testing"

	"github.com/your-org/pulsemetry/internal/event"
	"github.com/your-org/pulsemetry/internal/rollup"
	"github.com/your-org/pulsemetry/internal/session"
)

const (
	agreeVendor  = "claude_code"
	agreeInstall = "inst-agree"
	agreeStart   = event.UnixSec(1_700_000_000)
)

// stream 은 데몬이 두 집계기에 똑같이 먹이는 이벤트 스트림을 만든다.
//
// 메트릭의 모양은 디코더가 실제로 만드는 것과 같다 — Measure.Value + Attr.Type,
// 종류별 컬럼은 비운다. 그래야 rollup 의 매핑 표와 session 의 metricScalar 가 같은 값을 본다.
func stream() []event.Event {
	sec := func(n int64) event.UnixNano { return (agreeStart + event.UnixSec(n)).Nano() }

	metric := func(name string, temp event.Temporality, v float64, ts int64, mods ...func(*event.Event)) event.Event {
		e := event.Event{
			Vendor: agreeVendor, InstallationID: agreeInstall,
			Signal: event.SignalMetric, Name: name, TS: sec(ts),
			SessionID: "s1", Temporality: temp,
			Measure: event.Measures{Value: event.Some(v)},
		}
		for _, m := range mods {
			m(&e)
		}
		return e
	}
	// startedAt 을 붙이지 않으면 start_time 이 없는 계열이다 — 오프셋 0 과 "없음"을
	// 같은 값으로 표현하면 두 경우가 조용히 섞인다.
	startedAt := func(n int64) func(*event.Event) {
		return func(e *event.Event) { e.StartTS = sec(n) }
	}
	typed := func(v string) func(*event.Event) {
		return func(e *event.Event) { e.Attr.Type = v }
	}
	modeled := func(v string) func(*event.Event) {
		return func(e *event.Event) { e.Attr.Model = v }
	}
	sess := func(id string) func(*event.Event) {
		return func(e *event.Event) { e.SessionID = id }
	}

	cumulative, delta := event.TemporalityCumulative, event.TemporalityDelta

	return []event.Event{
		// 관측 기준점을 잡는 첫 이벤트. 두 집계기 모두 이 TS 를 기준점으로 삼는다.
		{Vendor: agreeVendor, InstallationID: agreeInstall, Signal: event.SignalLog,
			Name: "claude_code.user_prompt", TS: sec(0), SessionID: "s1"},

		// ① 관측 시작과 함께 시작한 누적 계열 → 첫 관측을 통째로 센다. (0.5 + 0.75)
		metric("claude_code.cost.usage", cumulative, 0.5, 10, startedAt(0), modeled("opus")),
		metric("claude_code.cost.usage", cumulative, 1.25, 70, startedAt(0), modeled("opus")),
		// ② 벤더 재시작: start_time 이 밀렸고 값은 오히려 커졌다 → 리셋. (+1.4)
		metric("claude_code.cost.usage", cumulative, 1.4, 130, startedAt(100), modeled("opus")),

		// ③ 속성만 다른 별개 계열. 하나로 섞이면 리셋 판정이 폭주해 값이 갈린다. (0.1 + 0.3)
		metric("claude_code.cost.usage", cumulative, 0.1, 11, startedAt(0), modeled("haiku")),
		metric("claude_code.cost.usage", cumulative, 0.4, 71, startedAt(0), modeled("haiku")),

		// ④ 우리보다 먼저 시작한 계열 → 첫 관측은 기준선. (0 + 800)
		metric("claude_code.token.usage", cumulative, 1000, 12, startedAt(-3600), typed("input")),
		metric("claude_code.token.usage", cumulative, 1800, 72, startedAt(-3600), typed("input")),

		// ⑤ 같은 구간에서 값이 뒤로 간 포인트 → 리셋이 아니다. (300 + 200 + 0 + 400)
		metric("claude_code.token.usage", cumulative, 300, 13, startedAt(0), typed("output")),
		metric("claude_code.token.usage", cumulative, 500, 73, startedAt(0), typed("output")),
		metric("claude_code.token.usage", cumulative, 400, 74, startedAt(0), typed("output")),
		metric("claude_code.token.usage", cumulative, 900, 75, startedAt(0), typed("output")),

		// ⑥ start_time 이 없는 계열 → 첫 관측은 기준선, 값이 줄면 폴백 리셋. (0 + 380 + 100)
		metric("claude_code.token.usage", cumulative, 220, 14, typed("cacheRead")),
		metric("claude_code.token.usage", cumulative, 600, 76, typed("cacheRead")),
		metric("claude_code.token.usage", cumulative, 100, 90, typed("cacheRead")),

		// ⑦ delta 는 그대로 합산된다.
		metric("claude_code.token.usage", delta, 80, 20, typed("cacheCreation")),
		metric("claude_code.lines_of_code.count", delta, 37, 30, typed("added")),
		metric("claude_code.lines_of_code.count", delta, 11, 31, typed("removed")),
		metric("claude_code.active_time.total", delta, 12.5, 40),

		// ⑧ 두 번째 세션. 계열 식별에 세션이 들어가 첫 세션의 값을 덮어쓰지 않는다. (2.0 + 1.0)
		metric("claude_code.cost.usage", cumulative, 2.0, 50, startedAt(45), sess("s2"), modeled("opus")),
		metric("claude_code.cost.usage", cumulative, 3.0, 110, startedAt(45), sess("s2"), modeled("opus")),
		metric("claude_code.lines_of_code.count", delta, 5, 51, sess("s2"), typed("added")),

		// ⑨ UNSPECIFIED 는 양쪽 모두 폐기한다.
		metric("claude_code.cost.usage", event.TemporalityUnspecified, 99, 120, sess("s2")),
	}
}

// TestSessionAndRollupAgreeOnTotals 는 이 리팩터의 핵심 회귀 방어선이다.
//
// 화면에서 Today 카드는 rollup_hourly 를, 세션 상세는 sessions 를 읽는다. 같은 이벤트
// 스트림에서 두 숫자가 다르면 사용자는 어느 쪽도 믿을 수 없다. 누적 판정 규칙이 두 벌이던
// 시절 실제로 갈렸다 — session 은 첫 관측을 통째로 세고 rollup 은 기준선으로 잡았다.
func TestSessionAndRollupAgreeOnTotals(t *testing.T) {
	events := stream()

	rows, rollupStats := rollup.Aggregate(events)
	total := totalBucket(t, rows)

	inputs := make([]session.Input, len(events))
	for i, e := range events {
		inputs[i] = session.Input{Event: e}
	}
	sessions := session.Assemble(inputs, agreeStart+7200)

	var got struct {
		cost                                    float64
		input, output, cacheRead, cacheCreation int64
		linesAdded, linesRemoved                int64
		activeSeconds                           float64
	}
	for _, s := range sessions {
		got.cost += s.CostUSD
		got.input += s.InputTokens
		got.output += s.OutputTokens
		got.cacheRead += s.CacheReadTokens
		got.cacheCreation += s.CacheCreationTokens
		got.linesAdded += s.LinesAdded
		got.linesRemoved += s.LinesRemoved
		got.activeSeconds += s.ActiveSeconds
	}
	if len(sessions) != 2 {
		t.Fatalf("세션 %d개, want 2", len(sessions))
	}

	checks := []struct {
		name             string
		fromRollup, from float64
	}{
		{"cost_usd", total.CostUSD, got.cost},
		{"input_tokens", float64(total.InputTokens), float64(got.input)},
		{"output_tokens", float64(total.OutputTokens), float64(got.output)},
		{"cache_read_tokens", float64(total.CacheReadTokens), float64(got.cacheRead)},
		{"cache_creation_tokens", float64(total.CacheCreationTokens), float64(got.cacheCreation)},
		{"lines_added", float64(total.LinesAdded), float64(got.linesAdded)},
		{"lines_removed", float64(total.LinesRemoved), float64(got.linesRemoved)},
		{"active_seconds", total.ActiveSeconds, got.activeSeconds},
	}
	for _, c := range checks {
		if math.Abs(c.fromRollup-c.from) > 1e-9 {
			t.Errorf("%s 가 갈렸다: rollup_hourly=%v sessions=%v", c.name, c.fromRollup, c.from)
		}
	}

	// 전제 확인 — 값이 전부 0 이면 위 단언이 공허하게 통과한다.
	if got.cost <= 0 || got.input <= 0 || got.output <= 0 || got.linesAdded <= 0 {
		t.Fatalf("스트림이 아무것도 만들지 않았다: %+v", got)
	}
	// 실제로 어떤 값이어야 하는지도 못 박는다. 두 구현이 같은 방향으로 함께 틀리면
	// 위 비교만으로는 못 잡는다. 주석의 번호는 stream() 의 구간 번호다.
	for _, c := range []struct {
		name string
		got  float64
		want float64
	}{
		{"cost_usd", got.cost, 0.5 + 0.75 + 1.4 + 0.1 + 0.3 + 2.0 + 1.0}, // ①②③⑧
		{"input_tokens", float64(got.input), 800},                        // ④ 기준선 뒤 차이만
		{"output_tokens", float64(got.output), 300 + 200 + 0 + 400},      // ⑤ 뒤집힌 포인트는 0
		{"cache_read_tokens", float64(got.cacheRead), 0 + 380 + 100},     // ⑥ 기준선 + 폴백 리셋
		{"cache_creation_tokens", float64(got.cacheCreation), 80},        // ⑦
		{"lines_added", float64(got.linesAdded), 37 + 5},                 // ⑦⑧
		{"lines_removed", float64(got.linesRemoved), 11},                 // ⑦
		{"active_seconds", got.activeSeconds, 12.5},                      // ⑦
	} {
		if math.Abs(c.got-c.want) > 1e-9 {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}

	// UNSPECIFIED 폐기도 양쪽이 같은 수를 센다.
	var discarded int64
	for _, s := range sessions {
		discarded += s.Diag.DiscardedPoints
	}
	if discarded != rollupStats.DroppedTemporality {
		t.Errorf("UNSPECIFIED 폐기 수가 다르다: sessions=%d rollup=%d",
			discarded, rollupStats.DroppedTemporality)
	}
}

// 도착 순서를 바꿔도 두 집계는 서로 같아야 한다. 누적 처리는 순서에 의존하므로
// 총량 자체는 달라질 수 있지만, **두 패키지가 같은 순서를 보면 같은 답을 내야 한다**.
func TestAgreementHoldsUnderReversedArrival(t *testing.T) {
	events := stream()
	reversed := make([]event.Event, len(events))
	for i, e := range events {
		reversed[len(events)-1-i] = e
	}

	rows, _ := rollup.Aggregate(reversed)
	total := totalBucket(t, rows)

	inputs := make([]session.Input, len(reversed))
	for i, e := range reversed {
		inputs[i] = session.Input{Event: e}
	}

	var cost float64
	var lines int64
	for _, s := range session.Assemble(inputs, agreeStart+7200) {
		cost += s.CostUSD
		lines += s.LinesAdded
	}
	if math.Abs(total.CostUSD-cost) > 1e-9 {
		t.Errorf("역순 도착에서 cost 가 갈렸다: rollup=%v sessions=%v", total.CostUSD, cost)
	}
	if total.LinesAdded != lines {
		t.Errorf("역순 도착에서 lines_added 가 갈렸다: rollup=%d sessions=%d", total.LinesAdded, lines)
	}
}

func totalBucket(t *testing.T, rows []rollup.Row) rollup.Bucket {
	t.Helper()
	for _, r := range rows {
		if r.Dim == rollup.DimTotal {
			return r.Bucket
		}
	}
	t.Fatal("total 행이 없다")
	return rollup.Bucket{}
}
