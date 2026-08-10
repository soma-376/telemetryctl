package session

import (
	"math"
	"strings"

	"github.com/your-org/pulsemetry/internal/event"
)

// seriesKey 는 cumulative 차분의 단위다.
//
// 속성 전체를 키에 넣는다. rollup 의 seriesID 와 같은 식별이어야 같은 스트림에서 두 집계가
// 갈리지 않는다 — 예전처럼 type·model 만 보면 tool_name 만 다른 두 계열이 하나로 섞여
// 서로의 직전값을 덮어쓰고, 그 순간 sessions 와 rollup_hourly 의 숫자가 달라진다.
// event.Attributes 는 문자열 필드뿐이라 comparable 이므로 구조체를 통째로 키로 쓴다.
//
// rollup 과 다른 것은 field 하나다. 디코더가 token.usage 를 종류별 컬럼에 채워 주면 이벤트
// 하나가 input·output·cache_read·cache_creation 네 필드에 각각 기여하므로, 그 넷은 서로 다른
// 계열이어야 한다. rollup 은 이벤트 하나가 컬럼 하나로만 가서 이 구분이 필요 없다.
//
// vendor·installation_id·session_id 는 넣지 않는다. 세션 상태 하나에 매달린 맵이라
// 세 값이 이미 고정돼 있다.
type seriesKey struct {
	name  string
	field string
	attr  event.Attributes
}

// metricDelta 는 데이터포인트가 세션 합계에 더해야 할 증분을 돌려준다.
//
// 벤더 기본값을 가정하지 않는다 — 사용자가 OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE
// 를 덮어쓸 수 있고, cumulative 를 delta 로 오인하면 세션 비용이 조용히 배로 잡힌다.
//
//	delta       → 값 그대로
//	cumulative  → event.CumulativeState.Step 의 판정을 그대로 따른다
//	unspecified → 폐기하고 센다 (계획서 「반드시 알아야 할 제약」 4)
//
// cumulative 판정을 event 패키지에서 가져오는 이유는 rollup 과 한 벌이어야 하기 때문이다.
// 화면에서 Today 카드는 rollup_hourly, 세션 상세는 sessions 를 읽는데 두 숫자가 어긋나면
// 사용자가 어느 쪽도 믿을 수 없다. 규칙이 두 벌이던 시절 실제로 갈렸다.
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
		k := seriesKey{name: e.Name, field: field, attr: e.Attr}
		delta, next, _ := s.cumulative[k].Step(event.CumulativePoint{
			Value: v, Start: e.StartTS, WatchFrom: s.watchFrom,
		})
		s.cumulative[k] = next
		return delta, true
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
