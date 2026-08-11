package rollup

import (
	"math"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/event"
)

const (
	testVendor  = "claude_code"
	testInstall = "install-1"
	testSession = "sess-1"
)

// hourStart 는 정각이다. 시간 경계 테스트가 이 값과 -1ns 를 쓴다.
var hourStart = time.Date(2026, 8, 10, 15, 0, 0, 0, time.UTC)

type eventOpt func(*event.Event)

func at(t time.Time) eventOpt      { return func(e *event.Event) { e.TS = event.NanoFromTime(t) } }
func afterSec(n int) eventOpt      { return at(hourStart.Add(time.Duration(n) * time.Second)) }
func inSession(id string) eventOpt { return func(e *event.Event) { e.SessionID = id } }
func withSeq(n int) eventOpt       { return func(e *event.Event) { e.Sequence = n } }

// since 는 cumulative 계열의 start_time_unix_nano 를 정한다.
// hourStart 기준이라 since(0) 이면 계열이 hourStart 부터 값을 쌓기 시작했다는 뜻이다.
// 집계기의 관측 시작점(첫 이벤트의 TS)이 보통 hourStart 이므로 since(0) 은 "우리가 보기
// 시작한 뒤에 시작한 계열" = 첫 관측을 통째로 세는 경우다.
func since(n int) eventOpt {
	return func(e *event.Event) {
		e.StartTS = event.NanoFromTime(hourStart.Add(time.Duration(n) * time.Second))
	}
}

func withAttr(f func(*event.Attributes)) eventOpt {
	return func(e *event.Event) { f(&e.Attr) }
}

func typed(v string) eventOpt    { return withAttr(func(a *event.Attributes) { a.Type = v }) }
func decision(v string) eventOpt { return withAttr(func(a *event.Attributes) { a.Decision = v }) }

func newMetric(name string, temp event.Temporality, value float64, mods ...eventOpt) event.Event {
	e := event.Event{
		Vendor:         testVendor,
		InstallationID: testInstall,
		Signal:         event.SignalMetric,
		Name:           name,
		TS:             event.NanoFromTime(hourStart),
		SessionID:      testSession,
		Temporality:    temp,
		Measure:        event.Measures{Value: event.Some(value)},
	}
	for _, m := range mods {
		m(&e)
	}
	return e
}

func newLog(name string, mods ...eventOpt) event.Event {
	e := event.Event{
		Vendor:         testVendor,
		InstallationID: testInstall,
		Signal:         event.SignalLog,
		Name:           name,
		TS:             event.NanoFromTime(hourStart),
		SessionID:      testSession,
	}
	for _, m := range mods {
		m(&e)
	}
	return e
}

// rowFor 는 특정 (dim, key) 행을 찾는다. 없으면 실패시킨다.
func rowFor(t *testing.T, rows []Row, dim Dim, key string) Row {
	t.Helper()
	for _, r := range rows {
		if r.Dim == dim && r.Key == key {
			return r
		}
	}
	t.Fatalf("행이 없음: dim=%s key=%q (rows=%d)", dim, key, len(rows))
	return Row{}
}

func hasRow(rows []Row, dim Dim, key string) bool {
	for _, r := range rows {
		if r.Dim == dim && r.Key == key {
			return true
		}
	}
	return false
}

func totalRow(t *testing.T, rows []Row) Row {
	t.Helper()
	return rowFor(t, rows, DimTotal, "")
}

// ── delta 합산 ────────────────────────────────────────────────────────────────

func TestDeltaValuesAreSummed(t *testing.T) {
	tests := []struct {
		name   string
		events []event.Event
		want   Bucket
	}{
		{
			name: "cost 세 건",
			events: []event.Event{
				newMetric("claude_code.cost.usage", event.TemporalityDelta, 0.5, afterSec(0)),
				newMetric("claude_code.cost.usage", event.TemporalityDelta, 0.25, afterSec(60)),
				newMetric("claude_code.cost.usage", event.TemporalityDelta, 1.0, afterSec(120)),
			},
			want: Bucket{CostUSD: 1.75},
		},
		{
			name: "토큰 종류별로 다른 컬럼",
			events: []event.Event{
				newMetric("claude_code.token.usage", event.TemporalityDelta, 100, typed("input"), afterSec(0)),
				newMetric("claude_code.token.usage", event.TemporalityDelta, 40, typed("output"), afterSec(0)),
				newMetric("claude_code.token.usage", event.TemporalityDelta, 900, typed("cacheRead"), afterSec(0)),
				newMetric("claude_code.token.usage", event.TemporalityDelta, 7, typed("cacheCreation"), afterSec(0)),
				newMetric("claude_code.token.usage", event.TemporalityDelta, 50, typed("input"), afterSec(60)),
			},
			want: Bucket{InputTokens: 150, OutputTokens: 40, CacheReadTokens: 900, CacheCreationTokens: 7},
		},
		{
			name: "라인 수 added/removed",
			events: []event.Event{
				newMetric("claude_code.lines_of_code.count", event.TemporalityDelta, 12, typed("added"), afterSec(0)),
				newMetric("claude_code.lines_of_code.count", event.TemporalityDelta, 3, typed("removed"), afterSec(0)),
				newMetric("claude_code.lines_of_code.count", event.TemporalityDelta, 8, typed("added"), afterSec(60)),
			},
			want: Bucket{LinesAdded: 20, LinesRemoved: 3},
		},
		{
			name: "편집 툴 승인·거부",
			events: []event.Event{
				newMetric("claude_code.code_edit_tool.decision", event.TemporalityDelta, 1, decision("accept"), afterSec(0)),
				newMetric("claude_code.code_edit_tool.decision", event.TemporalityDelta, 1, decision("accept"), afterSec(60)),
				newMetric("claude_code.code_edit_tool.decision", event.TemporalityDelta, 1, decision("reject"), afterSec(90)),
			},
			want: Bucket{ToolAccepts: 2, ToolRejects: 1},
		},
		{
			name: "로그 이벤트는 건수로 센다",
			events: []event.Event{
				newLog("claude_code.user_prompt", afterSec(0)),
				newLog("claude_code.user_prompt", afterSec(10)),
				newLog("claude_code.tool_result", afterSec(20)),
				newLog("claude_code.api_request", afterSec(30)),
				newLog("claude_code.api_error", afterSec(40)),
			},
			want: Bucket{Prompts: 2, ToolCalls: 1, APIRequests: 1, APIErrors: 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows, stats := Aggregate(tt.events)
			if got := totalRow(t, rows).Bucket; got != tt.want {
				t.Errorf("total 버킷 = %+v, want %+v", got, tt.want)
			}
			if stats.Counted != int64(len(tt.events)) {
				t.Errorf("Counted = %d, want %d (stats=%+v)", stats.Counted, len(tt.events), stats)
			}
		})
	}
}

// 재시도는 attempt 미설정과 1 을 재시도로 세면 안 된다.
func TestRetriesCountedFromAttempt(t *testing.T) {
	tests := []struct {
		name    string
		attempt event.Opt[int64]
		want    int64
	}{
		{"미설정", event.Opt[int64]{}, 0},
		{"첫 시도", event.Some(int64(1)), 0},
		{"두 번째 시도", event.Some(int64(2)), 1},
		{"세 번째 시도", event.Some(int64(3)), 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newLog("claude_code.api_request")
			e.Measure.Attempt = tt.attempt
			rows, _ := Aggregate([]event.Event{e})
			if got := totalRow(t, rows).Retries; got != tt.want {
				t.Errorf("Retries = %d, want %d", got, tt.want)
			}
		})
	}
}

// ── cumulative ───────────────────────────────────────────────────────────────

func TestCumulativeAddsOnlyTheDifference(t *testing.T) {
	events := []event.Event{
		newMetric("claude_code.cost.usage", event.TemporalityCumulative, 100, afterSec(0)),
		newMetric("claude_code.cost.usage", event.TemporalityCumulative, 250, afterSec(60)),
		newMetric("claude_code.cost.usage", event.TemporalityCumulative, 400, afterSec(120)),
	}

	t.Run("start_time 이 없으면 첫 관측을 기준선으로만 기록", func(t *testing.T) {
		rows, stats := Aggregate(events)
		if got := totalRow(t, rows).CostUSD; got != 300 {
			t.Errorf("cost = %v, want 300 (150+150)", got)
		}
		if stats.Baselines != 1 {
			t.Errorf("Baselines = %d, want 1", stats.Baselines)
		}
		if stats.CumulativeResets != 0 {
			t.Errorf("CumulativeResets = %d, want 0", stats.CumulativeResets)
		}
	})

	t.Run("관측 시작 이후에 시작한 계열은 첫 관측도 전부 더한다", func(t *testing.T) {
		fresh := make([]event.Event, len(events))
		for i, e := range events {
			since(0)(&e)
			fresh[i] = e
		}
		rows, stats := Aggregate(fresh)
		if got := totalRow(t, rows).CostUSD; got != 400 {
			t.Errorf("cost = %v, want 400", got)
		}
		if stats.Baselines != 0 {
			t.Errorf("Baselines = %d, want 0", stats.Baselines)
		}
	})

	// 데몬 재시작 시나리오. 계열이 우리보다 먼저 시작했으면 앞 구간은 이전 인스턴스가 이미
	// 저장했을 수 있으므로 기준선만 잡는다 — 여기서 값을 통째로 더하면 비용이 배로 잡힌다.
	t.Run("관측 시작 전부터 쌓이던 계열은 기준선만 잡는다", func(t *testing.T) {
		old := make([]event.Event, len(events))
		for i, e := range events {
			since(-3600)(&e)
			old[i] = e
		}
		rows, stats := Aggregate(old)
		if got := totalRow(t, rows).CostUSD; got != 300 {
			t.Errorf("cost = %v, want 300 — 데몬 재시작 구간이 이중 집계됐다", got)
		}
		if stats.Baselines != 1 {
			t.Errorf("Baselines = %d, want 1", stats.Baselines)
		}
	})
}

// start_time 이 바뀌면 값이 줄지 않아도 리셋이다. 값의 증감만 보던 규칙으로는 못 잡는
// 경우 — 벤더가 재시작한 뒤 다음 내보내기까지 직전 값을 이미 넘어선 상황이다.
func TestCumulativeResetDetectedByStartTime(t *testing.T) {
	events := []event.Event{
		newMetric("claude_code.cost.usage", event.TemporalityCumulative, 100, since(0), afterSec(0)),
		newMetric("claude_code.cost.usage", event.TemporalityCumulative, 150, since(0), afterSec(60)),
		// 벤더 재시작: 새 수집 구간에서 이미 180 까지 쌓였다. 값은 오히려 커졌다.
		newMetric("claude_code.cost.usage", event.TemporalityCumulative, 180, since(90), afterSec(120)),
	}
	rows, stats := Aggregate(events)

	// 100(첫 관측 전부) + 50(차이) + 180(재시작 후 누적 전부) = 330
	if got := totalRow(t, rows).CostUSD; got != 330 {
		t.Errorf("cost = %v, want 330 — start_time 변화를 리셋으로 못 잡았다", got)
	}
	if stats.CumulativeResets != 1 {
		t.Errorf("CumulativeResets = %d, want 1", stats.CumulativeResets)
	}
}

// 수집 구간이 그대로인데 값이 줄었다면 리셋이 아니다. 순서가 뒤집힌 포인트를 리셋으로 보고
// 값 전체를 더하면 그 양이 두 번 들어간다.
func TestCumulativeDecreaseWithinSameStartIsNotReset(t *testing.T) {
	events := []event.Event{
		newMetric("claude_code.cost.usage", event.TemporalityCumulative, 100, since(0), afterSec(0)),
		newMetric("claude_code.cost.usage", event.TemporalityCumulative, 150, since(0), afterSec(60)),
		newMetric("claude_code.cost.usage", event.TemporalityCumulative, 120, since(0), afterSec(90)), // 순서 뒤집힘
		newMetric("claude_code.cost.usage", event.TemporalityCumulative, 200, since(0), afterSec(120)),
	}
	rows, stats := Aggregate(events)

	// 100 + 50 + 0 + 50 = 200 — 마지막 누적값과 정확히 같다.
	if got := totalRow(t, rows).CostUSD; got != 200 {
		t.Errorf("cost = %v, want 200 (마지막 누적값)", got)
	}
	if stats.CumulativeResets != 0 {
		t.Errorf("CumulativeResets = %d, want 0 — 순서 뒤집힘을 리셋으로 오판했다", stats.CumulativeResets)
	}
}

// 계획서 리스크 표 "cumulative 이중 집계 → 비용 10배" 를 직접 겨냥한다.
// cumulative 계열을 delta 로 오인하면 매 포인트의 누적값이 통째로 더해져 합계가 폭증한다.
func TestCumulativeIsNeverSummedLikeDelta(t *testing.T) {
	const points = 10
	build := func(mods ...eventOpt) []event.Event {
		var out []event.Event
		for i := 1; i <= points; i++ {
			v := float64(i) // 누적값: 1, 2, ... 10
			out = append(out, newMetric("claude_code.cost.usage", event.TemporalityCumulative, v,
				append([]eventOpt{afterSec(i * 60)}, mods...)...))
		}
		return out
	}
	naiveSum := 0.0
	for i := 1; i <= points; i++ {
		naiveSum += float64(i)
	}
	if naiveSum != 55 {
		t.Fatalf("테스트 전제가 깨짐: naiveSum = %v", naiveSum)
	}

	for _, tc := range []struct {
		name   string
		events []event.Event
		want   float64
	}{
		{"start_time 없음(기준선)", build(), 9},
		// 첫 포인트가 afterSec(60) 이라 관측 시작점도 거기다. 계열이 그 시각부터 쌓였으면
		// 첫 관측이 통째로 우리 것이다.
		{"관측 시작 이후에 시작한 계열", build(since(60)), 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, _ := Aggregate(tc.events)
			got := totalRow(t, rows).CostUSD
			if got != tc.want {
				t.Errorf("cost = %v, want %v", got, tc.want)
			}
			// 마지막 누적값을 넘으면 이미 이중 집계다.
			if got > float64(points) {
				t.Errorf("cost %v 가 마지막 누적값 %d 를 초과 — cumulative 를 delta 로 오인함", got, points)
			}
			if got == naiveSum {
				t.Errorf("cost 가 단순 합 %v 와 같음 — cumulative 처리가 없다", naiveSum)
			}
		})
	}
}

// start_time 을 못 받았을 때의 폴백 규칙이다. 카운터가 뒤로 가면 음수를 더하지 않고
// 새 값 전체를 더한다. 이 픽스처에는 start_time 이 없으므로 첫 관측은 기준선이다.
func TestCumulativeReset(t *testing.T) {
	tests := []struct {
		name       string
		values     []float64
		want       float64 // 첫 관측을 기준선으로 잡은 값
		wantResets int64
	}{
		{"리셋 없음", []float64{10, 20, 30}, 20, 0},
		{"중간에 0 부터 다시", []float64{100, 250, 30, 80}, 230, 1},
		{"리셋 직후 값이 0", []float64{50, 0, 5}, 5, 1},
		{"여러 번 리셋", []float64{10, 4, 9, 2}, 11, 2}, // 기준선 0 + 4 + 5 + 2
		{"같은 값 반복은 0 을 더한다", []float64{10, 10, 10}, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var events []event.Event
			for i, v := range tt.values {
				events = append(events, newMetric("claude_code.cost.usage", event.TemporalityCumulative, v, afterSec(i*60)))
			}
			rows, stats := Aggregate(events)
			if got := totalRow(t, rows).CostUSD; got != tt.want {
				t.Errorf("cost = %v, want %v", got, tt.want)
			}
			if stats.CumulativeResets != tt.wantResets {
				t.Errorf("CumulativeResets = %d, want %d", stats.CumulativeResets, tt.wantResets)
			}
			if got := totalRow(t, rows).CostUSD; got < 0 {
				t.Errorf("음수가 집계됨: %v", got)
			}
		})
	}
}

// 속성 조합이 다르면 다른 계열이다. 한 계열로 묶이면 서로의 직전 값을 덮어써
// 차이가 번갈아 음수가 되고 리셋 판정이 폭주한다.
func TestCumulativeSeriesAreKeyedByAttributes(t *testing.T) {
	events := []event.Event{
		newMetric("claude_code.token.usage", event.TemporalityCumulative, 100, typed("input"), since(0), afterSec(0)),
		newMetric("claude_code.token.usage", event.TemporalityCumulative, 10, typed("output"), since(0), afterSec(0)),
		newMetric("claude_code.token.usage", event.TemporalityCumulative, 300, typed("input"), since(0), afterSec(60)),
		newMetric("claude_code.token.usage", event.TemporalityCumulative, 25, typed("output"), since(0), afterSec(60)),
	}
	rows, stats := Aggregate(events)

	total := totalRow(t, rows).Bucket
	if total.InputTokens != 300 || total.OutputTokens != 25 {
		t.Errorf("input=%d output=%d, want 300/25", total.InputTokens, total.OutputTokens)
	}
	if stats.CumulativeResets != 0 {
		t.Errorf("CumulativeResets = %d, want 0 — 계열이 섞였다", stats.CumulativeResets)
	}
}

// 동시에 도는 두 세션의 카운터는 독립적이다. 세션을 계열 키에서 빼면 값이 오르내려
// 리셋으로 오판된다.
func TestCumulativeSeriesAreKeyedBySession(t *testing.T) {
	events := []event.Event{
		newMetric("claude_code.cost.usage", event.TemporalityCumulative, 100, inSession("a"), since(0), afterSec(0)),
		newMetric("claude_code.cost.usage", event.TemporalityCumulative, 5, inSession("b"), since(0), afterSec(1)),
		newMetric("claude_code.cost.usage", event.TemporalityCumulative, 130, inSession("a"), since(0), afterSec(60)),
		newMetric("claude_code.cost.usage", event.TemporalityCumulative, 9, inSession("b"), since(0), afterSec(61)),
	}
	rows, stats := Aggregate(events)
	if got := totalRow(t, rows).CostUSD; got != 139 {
		t.Errorf("cost = %v, want 139 (130+9)", got)
	}
	if stats.CumulativeResets != 0 {
		t.Errorf("CumulativeResets = %d, want 0 — 세션이 섞였다", stats.CumulativeResets)
	}
}

// ── UNSPECIFIED 폐기 ──────────────────────────────────────────────────────────

func TestUnspecifiedTemporalityIsDroppedAndCounted(t *testing.T) {
	events := []event.Event{
		newMetric("claude_code.cost.usage", event.TemporalityUnspecified, 42, afterSec(0)),
		newMetric("claude_code.cost.usage", event.TemporalityUnspecified, 7, afterSec(60)),
		newMetric("claude_code.cost.usage", event.TemporalityDelta, 1, afterSec(120)),
	}
	rows, stats := Aggregate(events)

	if got := totalRow(t, rows).CostUSD; got != 1 {
		t.Errorf("cost = %v, want 1 — UNSPECIFIED 가 집계에 들어갔다", got)
	}
	if stats.DroppedTemporality != 2 {
		t.Errorf("DroppedTemporality = %d, want 2", stats.DroppedTemporality)
	}
	if stats.Counted != 1 {
		t.Errorf("Counted = %d, want 1", stats.Counted)
	}

	// 개별 처리 결과도 호출자에게 보여야 한다.
	a := New()
	if got := a.Add(newMetric("claude_code.cost.usage", event.TemporalityUnspecified, 42)); got != DroppedTemporality {
		t.Errorf("Add() = %v, want %v", got, DroppedTemporality)
	}
}

// 로그에는 temporality 개념이 없다. 제로값(=Unspecified)을 메트릭과 같은 규칙으로 폐기하면
// prompts·tool_calls·api_requests 가 통째로 0 이 된다.
func TestLogSignalIgnoresTemporality(t *testing.T) {
	e := newLog("claude_code.user_prompt")
	if e.Temporality != event.TemporalityUnspecified {
		t.Fatalf("테스트 전제가 깨짐: 로그의 Temporality = %v", e.Temporality)
	}
	rows, stats := Aggregate([]event.Event{e})
	if got := totalRow(t, rows).Prompts; got != 1 {
		t.Errorf("prompts = %d, want 1", got)
	}
	if stats.DroppedTemporality != 0 {
		t.Errorf("DroppedTemporality = %d, want 0", stats.DroppedTemporality)
	}
}

// ── 시간 경계 ────────────────────────────────────────────────────────────────

func TestHourBoundary(t *testing.T) {
	prevHour := event.HourOf(event.NanoFromTime(hourStart.Add(-time.Nanosecond)))
	thisHour := event.HourOf(event.NanoFromTime(hourStart))
	if prevHour == thisHour {
		t.Fatalf("테스트 전제가 깨짐: 두 버킷이 같다 (%d)", prevHour)
	}

	tests := []struct {
		name string
		ts   time.Time
		want event.Hour
	}{
		{"정각 직전 1ns 는 앞 버킷", hourStart.Add(-time.Nanosecond), prevHour},
		{"정각은 자기 버킷", hourStart, thisHour},
		{"정각 직후 1ns", hourStart.Add(time.Nanosecond), thisHour},
		{"59분 59초", hourStart.Add(59*time.Minute + 59*time.Second), thisHour},
		{"다음 정각", hourStart.Add(time.Hour), thisHour + 3600},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows, _ := Aggregate([]event.Event{newLog("claude_code.user_prompt", at(tt.ts))})
			total := totalRow(t, rows)
			if total.Hour != tt.want {
				t.Errorf("hour = %d (%s), want %d (%s)",
					total.Hour, total.Hour.Time(), tt.want, tt.want.Time())
			}
			if total.Hour.Time().UTC().Minute() != 0 || total.Hour.Time().UTC().Second() != 0 {
				t.Errorf("버킷이 정각이 아님: %s", total.Hour.Time())
			}
		})
	}
}

// 경계에 걸친 두 이벤트가 한 행으로 합쳐지지 않아야 한다.
func TestHourBoundarySplitsRows(t *testing.T) {
	rows, _ := Aggregate([]event.Event{
		newLog("claude_code.user_prompt", at(hourStart.Add(-time.Nanosecond))),
		newLog("claude_code.user_prompt", at(hourStart)),
	})

	var totals []Row
	for _, r := range rows {
		if r.Dim == DimTotal {
			totals = append(totals, r)
		}
	}
	if len(totals) != 2 {
		t.Fatalf("total 행 수 = %d, want 2 — 경계가 한 버킷으로 합쳐졌다", len(totals))
	}
	for _, r := range totals {
		if r.Prompts != 1 {
			t.Errorf("hour=%s prompts=%d, want 1", r.Hour.Time(), r.Prompts)
		}
	}
	if totals[0].Hour >= totals[1].Hour {
		t.Errorf("정렬이 시간 오름차순이 아님: %v", totals)
	}
}

// ── 중복 제거 ────────────────────────────────────────────────────────────────

func TestDedupSkipsRepeatedEvents(t *testing.T) {
	e := newLog("claude_code.user_prompt")
	rows, stats := Aggregate([]event.Event{e, e, e})

	if got := totalRow(t, rows).Prompts; got != 1 {
		t.Errorf("prompts = %d, want 1", got)
	}
	if stats.Duplicates != 2 || stats.Counted != 1 {
		t.Errorf("Duplicates=%d Counted=%d, want 2/1", stats.Duplicates, stats.Counted)
	}
}

// Sequence 가 다르면 같은 순간의 서로 다른 데이터포인트다 — 접으면 안 된다.
func TestDedupDoesNotFoldDistinctEvents(t *testing.T) {
	events := []event.Event{
		newMetric("claude_code.token.usage", event.TemporalityDelta, 10, typed("input"), withSeq(0)),
		newMetric("claude_code.token.usage", event.TemporalityDelta, 20, typed("output"), withSeq(1)),
		newLog("claude_code.user_prompt", withSeq(0)),
		newLog("claude_code.user_prompt", withSeq(1)),
	}
	rows, stats := Aggregate(events)
	total := totalRow(t, rows).Bucket
	if total.InputTokens != 10 || total.OutputTokens != 20 || total.Prompts != 2 {
		t.Errorf("버킷 = %+v — 서로 다른 이벤트가 접혔다", total)
	}
	if stats.Duplicates != 0 {
		t.Errorf("Duplicates = %d, want 0", stats.Duplicates)
	}
}

// 재전송된 cumulative 포인트가 계열의 직전 값을 갱신하면 그 다음 진짜 포인트의 차이가
// 0 이 돼 조용히 누락된다. 중복 판정이 계열 갱신보다 먼저여야 한다.
func TestDedupRunsBeforeSeriesUpdate(t *testing.T) {
	first := newMetric("claude_code.cost.usage", event.TemporalityCumulative, 100, since(0), afterSec(0))
	second := newMetric("claude_code.cost.usage", event.TemporalityCumulative, 150, since(0), afterSec(60))

	a := New()
	a.Add(first)
	a.Add(second)
	if got := a.Add(second); got != Duplicate {
		t.Fatalf("재전송 처리 = %v, want %v", got, Duplicate)
	}
	a.Add(newMetric("claude_code.cost.usage", event.TemporalityCumulative, 170, since(0), afterSec(120)))

	if got := totalRow(t, a.Rows()).CostUSD; got != 170 {
		t.Errorf("cost = %v, want 170", got)
	}
	if a.Stats().CumulativeResets != 0 {
		t.Errorf("CumulativeResets = %d, want 0", a.Stats().CumulativeResets)
	}
}

// 중복 창은 유계다. 창 밖으로 밀려난 키는 다시 통과한다 — 그 지점부터는 store 의
// dedup_key UNIQUE 가 책임진다. 경계 동작을 명시적으로 고정해 둔다.
func TestDedupWindowIsBounded(t *testing.T) {
	a := New(WithDedupCapacity(2))
	e0 := newLog("claude_code.user_prompt", withSeq(0))
	e1 := newLog("claude_code.user_prompt", withSeq(1))
	e2 := newLog("claude_code.user_prompt", withSeq(2))

	for _, e := range []event.Event{e0, e1, e2} {
		if d := a.Add(e); d != Counted {
			t.Fatalf("첫 등장이 %v 로 처리됨", d)
		}
	}
	// e0 은 창 밖으로 밀려났다.
	if d := a.Add(e0); d != Counted {
		t.Errorf("창 밖 키 재등장 = %v, want %v", d, Counted)
	}
	// e2 는 아직 창 안이다.
	if d := a.Add(e2); d != Duplicate {
		t.Errorf("창 안 키 재등장 = %v, want %v", d, Duplicate)
	}
}

// ── dim 팬아웃 ───────────────────────────────────────────────────────────────

func TestFanOutAcrossDims(t *testing.T) {
	p := event.NormalizePath("/Users/jy/dev/telemetryctl")
	e := newMetric("claude_code.token.usage", event.TemporalityDelta, 120, typed("input"),
		withAttr(func(a *event.Attributes) {
			a.Model = "claude-opus-5"
			a.ToolName = "Edit"
			*a = a.WithProject(p)
		}))

	rows, _ := Aggregate([]event.Event{e})
	if len(rows) != len(dimOrder) {
		t.Fatalf("행 수 = %d, want %d: %+v", len(rows), len(dimOrder), rows)
	}

	want := map[Dim]string{
		DimTotal:   "",
		DimVendor:  testVendor,
		DimModel:   "claude-opus-5",
		DimTool:    "Edit",
		DimProject: p.Hash,
		DimType:    "input",
	}
	for dim, key := range want {
		r := rowFor(t, rows, dim, key)
		if r.InputTokens != 120 {
			t.Errorf("dim=%s key=%q input=%d, want 120", dim, key, r.InputTokens)
		}
	}

	// 경로 전체가 key 에 실리면 안 된다 (ADR 0003).
	for _, r := range rows {
		if r.Key != "" && (r.Key == "/Users/jy/dev/telemetryctl" || len(r.Key) > 0 && r.Key[0] == '/') {
			t.Errorf("key 에 경로가 들어감: %q", r.Key)
		}
	}
}

// 축 정보가 없는 이벤트는 그 축의 행을 만들지 않는다.
func TestFanOutSkipsMissingAxes(t *testing.T) {
	rows, _ := Aggregate([]event.Event{newLog("claude_code.user_prompt")})

	if !hasRow(rows, DimTotal, "") || !hasRow(rows, DimVendor, testVendor) {
		t.Fatalf("total·vendor 행이 없다: %+v", rows)
	}
	for _, dim := range []Dim{DimModel, DimTool, DimProject, DimType} {
		if hasRow(rows, dim, "") {
			t.Errorf("dim=%s 에 빈 key 행이 만들어짐", dim)
		}
	}
	if len(rows) != 2 {
		t.Errorf("행 수 = %d, want 2: %+v", len(rows), rows)
	}
}

// total 은 모든 이벤트를 받고, vendor 별 합은 total 과 같아야 한다.
func TestTotalEqualsSumOfVendors(t *testing.T) {
	mk := func(vendor string, cost float64, sec int) event.Event {
		e := newMetric("claude_code.cost.usage", event.TemporalityDelta, cost, afterSec(sec))
		e.Vendor = vendor
		return e
	}
	rows, _ := Aggregate([]event.Event{
		mk("claude_code", 1.5, 0),
		mk("codex", 0.5, 60),
		mk("claude_code", 2.0, 120),
	})

	total := totalRow(t, rows).CostUSD
	sum := rowFor(t, rows, DimVendor, "claude_code").CostUSD + rowFor(t, rows, DimVendor, "codex").CostUSD
	if total != 4.0 || sum != total {
		t.Errorf("total=%v vendor 합=%v, want 4.0/4.0", total, sum)
	}
}

// ── 값 검증 ─────────────────────────────────────────────────────────────────

func TestUnusableValues(t *testing.T) {
	noValue := newMetric("claude_code.cost.usage", event.TemporalityDelta, 0)
	noValue.Measure.Value = event.Opt[float64]{}

	tests := []struct {
		name string
		ev   event.Event
		want Disposition
	}{
		{"값 없음", noValue, UnusableValue},
		{"음수 delta", newMetric("claude_code.cost.usage", event.TemporalityDelta, -3), UnusableValue},
		{"NaN", newMetric("claude_code.cost.usage", event.TemporalityDelta, math.NaN()), UnusableValue},
		{"+Inf", newMetric("claude_code.cost.usage", event.TemporalityDelta, math.Inf(1)), UnusableValue},
		{"-Inf", newMetric("claude_code.cost.usage", event.TemporalityDelta, math.Inf(-1)), UnusableValue},
		{"값 0 은 정상", newMetric("claude_code.cost.usage", event.TemporalityDelta, 0), Counted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := New()
			if got := a.Add(tt.ev); got != tt.want {
				t.Fatalf("Add() = %v, want %v", got, tt.want)
			}
			for _, r := range a.Rows() {
				if math.IsNaN(r.CostUSD) || math.IsInf(r.CostUSD, 0) || r.CostUSD < 0 {
					t.Fatalf("버킷이 오염됨: %v", r.CostUSD)
				}
			}
		})
	}
}

func TestUnmappedAndInvalid(t *testing.T) {
	tests := []struct {
		name string
		ev   event.Event
		want Disposition
	}{
		{"모르는 이름", newLog("codex.unknown.thing"), Unmapped},
		{"모르는 type 값", newMetric("claude_code.token.usage", event.TemporalityDelta, 5, typed("reasoning")), Unmapped},
		{"type 없음", newMetric("claude_code.lines_of_code.count", event.TemporalityDelta, 5), Unmapped},
		{"모르는 decision 값", newMetric("claude_code.code_edit_tool.decision", event.TemporalityDelta, 1, decision("defer")), Unmapped},
		{"vendor 없음", func() event.Event { e := newLog("claude_code.user_prompt"); e.Vendor = ""; return e }(), Invalid},
		{"ts 0", func() event.Event { e := newLog("claude_code.user_prompt"); e.TS = 0; return e }(), Invalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := New()
			if got := a.Add(tt.ev); got != tt.want {
				t.Fatalf("Add() = %v, want %v", got, tt.want)
			}
			if len(a.Rows()) != 0 {
				t.Errorf("행이 만들어짐: %+v", a.Rows())
			}
		})
	}
}

// 표기 흔들림(cache_read, CacheRead)은 같은 컬럼으로 가야 한다.
func TestAttributeTokenNormalization(t *testing.T) {
	for _, v := range []string{"cacheRead", "cache_read", "CACHE_READ", "cache-read"} {
		rows, _ := Aggregate([]event.Event{
			newMetric("claude_code.token.usage", event.TemporalityDelta, 7, typed(v)),
		})
		if got := totalRow(t, rows).CacheReadTokens; got != 7 {
			t.Errorf("type=%q → cache_read_tokens = %d, want 7", v, got)
		}
	}
}

// ── 매핑 표 ─────────────────────────────────────────────────────────────────

// 이름 → 컬럼 대응을 한 곳에서 고정한다. 매핑이 바뀌면 여기가 먼저 깨진다.
func TestMappingTable(t *testing.T) {
	tests := []struct {
		name string
		ev   event.Event
		want Bucket
	}{
		{"session.count", newMetric("claude_code.session.count", event.TemporalityDelta, 1), Bucket{SessionsStarted: 1}},
		{"cost.usage", newMetric("claude_code.cost.usage", event.TemporalityDelta, 0.125), Bucket{CostUSD: 0.125}},
		{"active_time.total", newMetric("claude_code.active_time.total", event.TemporalityDelta, 42.5), Bucket{ActiveSeconds: 42.5}},
		{"commit.count", newMetric("claude_code.commit.count", event.TemporalityDelta, 2), Bucket{Commits: 2}},
		{"pull_request.count", newMetric("claude_code.pull_request.count", event.TemporalityDelta, 1), Bucket{PullRequests: 1}},
		{"lines added", newMetric("claude_code.lines_of_code.count", event.TemporalityDelta, 9, typed("added")), Bucket{LinesAdded: 9}},
		{"lines removed", newMetric("claude_code.lines_of_code.count", event.TemporalityDelta, 4, typed("removed")), Bucket{LinesRemoved: 4}},
		{"token input", newMetric("claude_code.token.usage", event.TemporalityDelta, 11, typed("input")), Bucket{InputTokens: 11}},
		{"token output", newMetric("claude_code.token.usage", event.TemporalityDelta, 12, typed("output")), Bucket{OutputTokens: 12}},
		{"token cacheRead", newMetric("claude_code.token.usage", event.TemporalityDelta, 13, typed("cacheRead")), Bucket{CacheReadTokens: 13}},
		{"token cacheCreation", newMetric("claude_code.token.usage", event.TemporalityDelta, 14, typed("cacheCreation")), Bucket{CacheCreationTokens: 14}},
		{"edit accept", newMetric("claude_code.code_edit_tool.decision", event.TemporalityDelta, 1, decision("accept")), Bucket{ToolAccepts: 1}},
		{"edit reject", newMetric("claude_code.code_edit_tool.decision", event.TemporalityDelta, 1, decision("reject")), Bucket{ToolRejects: 1}},
		{"user_prompt", newLog("claude_code.user_prompt"), Bucket{Prompts: 1}},
		{"tool_result", newLog("claude_code.tool_result"), Bucket{ToolCalls: 1}},
		{"api_request", newLog("claude_code.api_request"), Bucket{APIRequests: 1}},
		{"api_error", newLog("claude_code.api_error"), Bucket{APIErrors: 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows, _ := Aggregate([]event.Event{tt.ev})
			if got := totalRow(t, rows).Bucket; got != tt.want {
				t.Errorf("버킷 = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// 비용·토큰은 출처가 하나뿐이다. api_request 로그에도 같은 수치가 실려 오지만 여기서 더하면
// 메트릭과 합쳐져 정확히 2배가 된다 (리스크 표 "비용 10배").
func TestLogDoesNotDuplicateMetricColumns(t *testing.T) {
	log := newLog("claude_code.api_request")
	log.Measure.CostUSD = event.Some(3.5)
	log.Measure.InputTokens = event.Some(int64(1000))
	log.Measure.OutputTokens = event.Some(int64(200))

	rows, _ := Aggregate([]event.Event{
		log,
		newMetric("claude_code.cost.usage", event.TemporalityDelta, 3.5, afterSec(60)),
		newMetric("claude_code.token.usage", event.TemporalityDelta, 1000, typed("input"), afterSec(60)),
	})

	total := totalRow(t, rows).Bucket
	if total.CostUSD != 3.5 {
		t.Errorf("cost = %v, want 3.5 — 로그와 메트릭이 둘 다 더해졌다", total.CostUSD)
	}
	if total.InputTokens != 1000 {
		t.Errorf("input_tokens = %d, want 1000", total.InputTokens)
	}
	if total.APIRequests != 1 {
		t.Errorf("api_requests = %d, want 1", total.APIRequests)
	}
}

// ── 집계기 수명 ──────────────────────────────────────────────────────────────

// Flush 는 버킷만 비운다. 계열 상태와 중복 창을 같이 버리면 다음 cumulative 포인트가
// 콜드 스타트로 잡히고 플러시 직후 재전송된 이벤트가 두 번 집계된다.
func TestFlushClearsBucketsButKeepsState(t *testing.T) {
	a := New()
	first := newMetric("claude_code.cost.usage", event.TemporalityCumulative, 100, since(0), afterSec(0))
	a.Add(first)

	if got := totalRow(t, a.Flush()).CostUSD; got != 100 {
		t.Fatalf("첫 Flush cost = %v, want 100", got)
	}
	if len(a.Rows()) != 0 {
		t.Fatalf("Flush 후 버킷이 안 비었다: %+v", a.Rows())
	}

	// 계열 상태 유지: 150 은 50 만 더해져야 한다.
	a.Add(newMetric("claude_code.cost.usage", event.TemporalityCumulative, 150, since(0), afterSec(60)))
	if got := totalRow(t, a.Rows()).CostUSD; got != 50 {
		t.Errorf("Flush 후 cost = %v, want 50 — 계열 상태가 사라졌다", got)
	}
	// 중복 창 유지
	if d := a.Add(first); d != Duplicate {
		t.Errorf("Flush 후 재전송 = %v, want %v", d, Duplicate)
	}
	// 누계 통계는 Flush 로 초기화되지 않는다.
	if s := a.Stats(); s.Counted != 2 || s.Duplicates != 1 {
		t.Errorf("Stats = %+v, want Counted=2 Duplicates=1", s)
	}
}

func TestRowsAreSortedDeterministically(t *testing.T) {
	events := []event.Event{
		newMetric("claude_code.cost.usage", event.TemporalityDelta, 1, afterSec(3600),
			withAttr(func(a *event.Attributes) { a.Model = "zeta" })),
		newMetric("claude_code.cost.usage", event.TemporalityDelta, 1, afterSec(0),
			withAttr(func(a *event.Attributes) { a.Model = "alpha" })),
		newMetric("claude_code.cost.usage", event.TemporalityDelta, 1, afterSec(60),
			withAttr(func(a *event.Attributes) { a.Model = "beta" })),
	}
	rows, _ := Aggregate(events)

	for i := 1; i < len(rows); i++ {
		prev, cur := rows[i-1], rows[i]
		switch {
		case prev.Hour > cur.Hour:
			t.Fatalf("hour 정렬 깨짐: %v → %v", prev, cur)
		case prev.Hour == cur.Hour && dimRank(prev.Dim) > dimRank(cur.Dim):
			t.Fatalf("dim 정렬 깨짐: %v → %v", prev, cur)
		case prev.Hour == cur.Hour && prev.Dim == cur.Dim && prev.Key > cur.Key:
			t.Fatalf("key 정렬 깨짐: %v → %v", prev, cur)
		}
	}
	// total 이 언제나 먼저 온다.
	if rows[0].Dim != DimTotal {
		t.Errorf("첫 행 dim = %s, want %s", rows[0].Dim, DimTotal)
	}
}

// 계열 상태는 유계다. 밀려난 계열이 다시 오면 콜드 스타트로 잡힌다 —
// 기본 정책에서는 과소 집계이고 과대 집계는 아니다.
func TestSeriesStateIsBounded(t *testing.T) {
	a := New(WithSeriesCapacity(1))
	a.Add(newMetric("claude_code.token.usage", event.TemporalityCumulative, 100, typed("input"), since(0), afterSec(0)))
	a.Add(newMetric("claude_code.token.usage", event.TemporalityCumulative, 10, typed("output"), since(0), afterSec(1)))
	a.Add(newMetric("claude_code.token.usage", event.TemporalityCumulative, 300, typed("input"), since(0), afterSec(60)))

	if got := a.Stats().SeriesEvicted; got == 0 {
		t.Fatal("계열이 하나도 밀려나지 않음 — 용량이 적용되지 않았다")
	}
	if got := len(a.series.state); got > 1 {
		t.Errorf("계열 상태 수 = %d, want <= 1", got)
	}
	// input 계열이 밀려났으므로 300 은 차이(200)가 아니라 콜드 스타트로 잡힌다.
	if got := totalRow(t, a.Rows()).InputTokens; got != 400 {
		t.Errorf("input = %d, want 400 (100 + 콜드 스타트 300)", got)
	}
}

func TestOptionsFallBackToDefaults(t *testing.T) {
	a := New(WithDedupCapacity(0), WithSeriesCapacity(-5))
	if a.dedup.capacity != defaultDedupCapacity {
		t.Errorf("dedup capacity = %d, want %d", a.dedup.capacity, defaultDedupCapacity)
	}
	if a.series.capacity != defaultSeriesCapacity {
		t.Errorf("series capacity = %d, want %d", a.series.capacity, defaultSeriesCapacity)
	}
}

func TestDispositionString(t *testing.T) {
	want := map[Disposition]string{
		Counted: "counted", Duplicate: "duplicate", Invalid: "invalid",
		Unmapped: "unmapped", UnusableValue: "unusable_value",
		DroppedTemporality: "dropped_temporality", Baseline: "baseline",
	}
	for d, s := range want {
		if got := d.String(); got != s {
			t.Errorf("Disposition(%d).String() = %q, want %q", d, got, s)
		}
	}
	if got := Disposition(200).String(); got != "unknown" {
		t.Errorf("알 수 없는 값 = %q, want %q", got, "unknown")
	}
}

// 값을 쓰는 로그 매핑이 나중에 생기더라도 temporality 를 보지 않아야 한다.
// 지금은 그런 매핑이 없어 공개 API 로 닿지 않는 분기라 직접 고정해 둔다.
func TestResolveIgnoresTemporalityForLogs(t *testing.T) {
	e := newLog("claude_code.user_prompt")
	e.Measure.Value = event.Some(3.0)
	e.Temporality = event.TemporalityUnspecified

	v, d := New().resolve(e)
	if d != Counted || v != 3 {
		t.Errorf("resolve() = (%v, %v), want (3, %v)", v, d, Counted)
	}
}

// 정의되지 않은 dim 은 행을 만들지 않고 정렬에서 맨 뒤로 간다.
func TestUnknownDim(t *testing.T) {
	if _, ok := dimKey(Dim("agent"), newLog("claude_code.user_prompt")); ok {
		t.Error("정의되지 않은 dim 이 행을 만들었다")
	}
	if got := dimRank(Dim("agent")); got != len(dimOrder) {
		t.Errorf("dimRank = %d, want %d", got, len(dimOrder))
	}
}

func TestBucketIsZero(t *testing.T) {
	if !(Bucket{}).IsZero() {
		t.Error("제로 버킷이 IsZero 가 아님")
	}
	if (Bucket{CostUSD: 0.0001}).IsZero() {
		t.Error("값이 있는 버킷이 IsZero 로 판정됨")
	}
}
