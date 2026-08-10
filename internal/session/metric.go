package session

import (
	"math"
	"strings"

	"github.com/your-org/pulsemetry/internal/event"
)

// seriesKey 는 cumulative 차분의 단위다. 같은 메트릭이라도 type·model 이 다르면 다른
// 시계열이라 각각의 직전값을 따로 들고 있어야 한다.
type seriesKey struct {
	name  string
	typ   string
	model string
	field string
}

// metricDelta 는 데이터포인트가 세션 합계에 더해야 할 증분을 돌려준다.
//
// 벤더 기본값을 가정하지 않는다 — 사용자가 OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE
// 를 덮어쓸 수 있고, cumulative 를 delta 로 오인하면 세션 비용이 조용히 배로 잡힌다.
//
//	delta       → 값 그대로
//	cumulative  → 같은 시계열의 직전값과의 차. 값이 줄었으면 카운터 리셋으로 보고 값 전체
//	unspecified → 폐기하고 센다 (계획서 「반드시 알아야 할 제약」 4)
//
// 로그 시그널에는 temporality 개념이 없으므로 값을 그대로 쓴다.
func (s *state) metricDelta(e event.Event, field string, v float64) (float64, bool) {
	if e.Signal != event.SignalMetric {
		return v, true
	}
	switch e.Temporality {
	case event.TemporalityDelta:
		return v, true
	case event.TemporalityCumulative:
		k := seriesKey{name: e.Name, typ: e.Attr.Type, model: e.Attr.Model, field: field}
		last, seen := s.cumulative[k]
		s.cumulative[k] = v
		switch {
		case !seen:
			// 세션 안에서 처음 본 누적값이다. 벤더 프로세스와 세션의 수명이 사실상 같아
			// 값 전체를 이 세션의 누계로 본다. 프로세스를 재사용하는 벤더가 나오면 과대 집계된다.
			return v, true
		case v >= last:
			return v - last, true
		default:
			return v, true
		}
	default:
		s.discarded++
		return 0, false
	}
}

// addInt·addFloat 는 증분을 반영한다. 음수 증분은 버린다 — 토큰·비용·라인·활동 시간은
// 줄어들 수 없고, 음수를 그대로 더하면 화면에 음수 비용이 뜬다.
func (s *state) addInt(e event.Event, field string, v float64, dst *int64) {
	if d, ok := s.metricDelta(e, field, v); ok && d > 0 {
		*dst += int64(math.Round(d))
	}
}

func (s *state) addFloat(e event.Event, field string, v float64, dst *float64) {
	if d, ok := s.metricDelta(e, field, v); ok && d > 0 {
		*dst += d
	}
}

// metricScalar 는 데이터포인트 값을 꺼낸다. 디코더가 Value 대신 전용 컬럼에 넣었을 수도
// 있어 cost 는 CostUSD 도 본다.
func metricScalar(e event.Event) (float64, bool) {
	if v, ok := e.Measure.Value.Get(); ok {
		return v, true
	}
	if v, ok := e.Measure.CostUSD.Get(); ok {
		return v, true
	}
	return 0, false
}

// normalizeType 은 type 속성의 표기 차이를 흡수한다 — cacheRead · cache_read · CacheRead
// 가 전부 같은 값이어야 토큰이 갈라지지 않는다.
func normalizeType(s string) string {
	return strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(s, "_", ""), "-", ""))
}
