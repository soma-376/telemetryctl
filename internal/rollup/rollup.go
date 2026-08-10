// Package rollup 은 정규화 이벤트를 rollup_hourly 의 (hour, dim, key) 버킷으로 집계한다.
// 결과는 Row 슬라이스이고, 저장은 6단계 store 소관이라 이 패키지는 SQL 을 모른다.
//
// IO 가 없다. "지금 시각" 도 읽지 않는다 — 버킷·계열 상태·중복 창은 전부 이벤트의
// 타임스탬프와 도착 순서에서만 파생된다.
//
// 계획서는 이 패키지를 "순수 함수" 로 규정했지만 cumulative 메트릭 때문에 완전한 순수 함수가
// 될 수 없다. cumulative 는 값 자체가 아니라 직전 값과의 차이가 기여분이므로 계열별 직전 값을
// 들고 있어야 한다. 그래서 Aggregator 만 상태를 가지고, 나머지(매핑·팬아웃·시간 버킷)는
// 전부 상태 없는 함수로 둔다.
//
// Aggregator 는 동시 사용에 안전하지 않다. 수신기의 워커 여러 개가 하나의 집계기를 공유하면
// 배선 단계에서 직렬화한다 — 여기에 뮤텍스를 넣으면 계열 상태의 순서 의존성(직전 값 갱신)이
// 락으로 가려져 오히려 추적이 어려워진다.
package rollup

import (
	"math"
	"sort"

	"github.com/your-org/pulsemetry/internal/event"
)

// 기본 용량. 둘 다 "조용히 무한히 자라지 않는다" 가 목적이라 넉넉하되 유한하다.
//
//   - dedup: 중복이 도착하는 창은 exporter 재시도·Collector 재전송이라 분 단위다.
//     로그 5초·메트릭 60초 주기에서 16384 건이면 그 창을 훨씬 넘게 덮는다.
//   - series: 계열 = (세션 × 메트릭 이름 × 속성 조합). 동시에 살아 있는 세션이 수십 개라도
//     4096 을 넘기 어렵다. 넘으면 가장 오래 안 쓴 계열부터 버린다.
const (
	defaultDedupCapacity  = 16384
	defaultSeriesCapacity = 4096
)

type options struct {
	dedupCapacity  int
	seriesCapacity int
}

// Option 은 Aggregator 생성 옵션이다. 용량 값이 0 이하면 기본값을 쓴다 —
// 실수로 0 을 넘겨 중복 제거나 계열 상태가 통째로 꺼지는 편이 더 위험하다.
type Option func(*options)

func WithDedupCapacity(n int) Option {
	return func(o *options) {
		if n > 0 {
			o.dedupCapacity = n
		}
	}
}

func WithSeriesCapacity(n int) Option {
	return func(o *options) {
		if n > 0 {
			o.seriesCapacity = n
		}
	}
}

// Disposition 은 이벤트 하나의 처리 결과다. 집계에 들어간 것과 버려진 것을 호출자가 구분할 수
// 있어야 한다 — 조용히 버리면 화면의 숫자가 왜 작은지 아무도 설명하지 못한다.
type Disposition uint8

const (
	// Counted 는 집계에 반영됐다는 뜻이다. cumulative 차이가 0 인 경우도 포함한다.
	Counted Disposition = iota
	// Duplicate 는 같은 DedupKey 를 이미 본 경우다.
	Duplicate
	// Invalid 는 event.Validate() 가 거부한 경우다.
	Invalid
	// Unmapped 는 매핑 테이블에 없는 이름이거나, 이름은 있지만 컬럼을 고르는 속성
	// (type·decision)이 알 수 없는 값인 경우다.
	Unmapped
	// UnusableValue 는 값이 필요한 메트릭에 값이 없거나 음수·NaN·Inf 인 경우다.
	UnusableValue
	// DroppedTemporality 는 §4 가 폐기하라고 한 UNSPECIFIED 메트릭이다.
	DroppedTemporality
	// Baseline 은 cumulative 첫 관측을 기준선으로만 기록한 경우다 — 그 계열이 우리가 보기
	// 시작하기 전부터 쌓이고 있었다는 뜻이다 (seriesStore.observe).
	Baseline
)

func (d Disposition) String() string {
	switch d {
	case Counted:
		return "counted"
	case Duplicate:
		return "duplicate"
	case Invalid:
		return "invalid"
	case Unmapped:
		return "unmapped"
	case UnusableValue:
		return "unusable_value"
	case DroppedTemporality:
		return "dropped_temporality"
	case Baseline:
		return "baseline"
	default:
		return "unknown"
	}
}

// Stats 는 집계기가 사는 동안 누적한 처리 결과다. Flush 로 초기화되지 않는다 —
// status 명령과 로그가 "얼마나 버렸는지" 를 보려면 버킷보다 오래 살아야 한다.
type Stats struct {
	Counted    int64
	Duplicates int64
	Invalid    int64
	Unmapped   int64
	// UnusableValue 는 값이 없거나 쓸 수 없는(음수·NaN·Inf) 메트릭 수다.
	UnusableValue int64
	// DroppedTemporality 가 §4 의 "UNSPECIFIED 폐기 카운트" 다.
	DroppedTemporality int64
	// Baselines 는 기준선만 기록하고 0 을 더한 cumulative 첫 관측 수다.
	Baselines int64
	// CumulativeResets 는 카운터가 뒤로 간 것을 리셋으로 판정한 횟수다.
	CumulativeResets int64
	// SeriesEvicted 는 용량 초과로 버린 계열 상태 수다. 계속 늘면 용량이 모자라다는 뜻이고,
	// 버려진 계열의 다음 포인트는 콜드 스타트로 잡힌다.
	SeriesEvicted int64
}

// Aggregator 는 이벤트를 받아 시간 버킷에 누적한다.
type Aggregator struct {
	buckets map[rowKey]*Bucket
	dedup   *dedupSet
	series  *seriesStore
	stats   Stats

	// watchFrom 은 이 집계기가 처음 이벤트를 받은 시각이다. cumulative 첫 관측을 판정하는
	// 기준점이다 (event.CumulativePoint.WatchFrom). 시계를 읽지 않고 이벤트 타임스탬프에서만
	// 파생하므로 같은 입력이면 결과도 같다.
	//
	// session.Assembler 도 같은 방식으로 기준점을 잡는다. 같은 스트림을 두 패키지에 먹였을 때
	// 총량이 일치하려면 기준점이 같아야 하기 때문이다.
	watchFrom event.UnixNano
	watching  bool
}

func New(opts ...Option) *Aggregator {
	cfg := options{
		dedupCapacity:  defaultDedupCapacity,
		seriesCapacity: defaultSeriesCapacity,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return &Aggregator{
		buckets: make(map[rowKey]*Bucket),
		dedup:   newDedupSet(cfg.dedupCapacity),
		series:  newSeriesStore(cfg.seriesCapacity),
	}
}

// Add 는 이벤트 하나를 집계하고 처리 결과를 돌려준다.
// 반환값은 호출자가 개별 이벤트를 로깅할 때 쓰고, 누계는 Stats 가 준다.
func (a *Aggregator) Add(e event.Event) Disposition {
	if err := e.Validate(); err != nil {
		a.stats.Invalid++
		return Invalid
	}
	// 첫 유효 이벤트가 "여기서부터 보고 있었다" 는 기준점이다. 중복 판정보다 먼저 찍는다 —
	// 첫 이벤트가 마침 재전송이더라도 우리가 그때부터 보고 있었다는 사실은 같다.
	if !a.watching {
		a.watching, a.watchFrom = true, e.TS
	}
	// 중복 판정이 가장 먼저다. 뒤로 미루면 재전송된 cumulative 포인트가 계열의 직전 값을
	// 갱신해 버려 그 다음 진짜 포인트의 차이가 0 이 된다.
	if a.dedup.seen(e.DedupKey()) {
		a.stats.Duplicates++
		return Duplicate
	}

	delta, disp := a.contribution(e)
	if disp != Counted {
		a.count(disp)
		return disp
	}
	a.stats.Counted++
	a.fanOut(e, delta)
	return Counted
}

// AddAll 은 이벤트 배치를 순서대로 집계한다. cumulative 는 순서에 의존하므로
// 호출자가 도착 순서를 유지해 넘겨야 한다.
func (a *Aggregator) AddAll(events []event.Event) {
	for _, e := range events {
		a.Add(e)
	}
}

// Rows 는 누적된 버킷을 (hour, dim, key) 순으로 정렬해 돌려준다. 상태를 바꾸지 않는다.
func (a *Aggregator) Rows() []Row {
	rows := make([]Row, 0, len(a.buckets))
	for k, b := range a.buckets {
		rows = append(rows, Row{Hour: k.hour, Dim: k.dim, Key: k.key, Bucket: *b})
	}
	sort.Slice(rows, func(i, j int) bool {
		switch {
		case rows[i].Hour != rows[j].Hour:
			return rows[i].Hour < rows[j].Hour
		case rows[i].Dim != rows[j].Dim:
			return dimRank(rows[i].Dim) < dimRank(rows[j].Dim)
		default:
			return rows[i].Key < rows[j].Key
		}
	})
	return rows
}

// Flush 는 Rows 를 돌려주고 버킷만 비운다.
// 계열 상태와 중복 제거 기록은 남긴다 — 같이 버리면 다음 cumulative 포인트가 콜드 스타트로
// 잡히고 플러시 직후 재전송된 이벤트가 두 번 집계된다. 버킷은 store 가 UPSERT 로 누적하므로
// 비워도 값이 사라지지 않는다.
func (a *Aggregator) Flush() []Row {
	rows := a.Rows()
	a.buckets = make(map[rowKey]*Bucket)
	return rows
}

func (a *Aggregator) Stats() Stats {
	s := a.stats
	s.Baselines = a.series.baselines
	s.CumulativeResets = a.series.resets
	s.SeriesEvicted = a.series.evicted
	return s
}

func (a *Aggregator) count(d Disposition) {
	switch d {
	case Unmapped:
		a.stats.Unmapped++
	case UnusableValue:
		a.stats.UnusableValue++
	case DroppedTemporality:
		a.stats.DroppedTemporality++
	case Baseline:
		// 카운트는 seriesStore 가 들고 있다 (Stats 에서 합친다).
	}
}

// contribution 은 이벤트 하나가 만드는 수치 기여분을 계산한다.
// 한 번만 계산해 모든 dim 행에 같은 값을 더한다 — dim 별로 다시 계산하면 팬아웃이 어긋난다.
func (a *Aggregator) contribution(e event.Event) (Bucket, Disposition) {
	m, ok := mappings[e.Name]
	if !ok {
		return Bucket{}, Unmapped
	}

	v := 1.0 // 값이 필요 없는 매핑(로그 1건 = 1 회)의 기본 기여량
	if m.needsValue {
		var disp Disposition
		v, disp = a.resolve(e)
		if disp != Counted {
			return Bucket{}, disp
		}
	}

	var b Bucket
	if !m.apply(&b, e, v) {
		return Bucket{}, Unmapped
	}
	return b, Counted
}

// resolve 는 데이터포인트 값을 "이번에 더할 양" 으로 바꾼다. §4 의 분기 지점이다.
func (a *Aggregator) resolve(e event.Event) (float64, Disposition) {
	raw, ok := e.Measure.Value.Get()
	if !ok || !usable(raw) {
		return 0, UnusableValue
	}

	// 로그 시그널에는 temporality 개념이 없다. OTLP 의 aggregation_temporality 는 Sum 에만
	// 있고 LogRecord 에는 대응하는 필드가 없어서 로그 이벤트의 Temporality 는 항상 제로값
	// (=Unspecified)이다. 여기서 §4 의 폐기 규칙을 함께 적용하면 prompts·tool_calls·
	// api_requests 가 통째로 0 이 된다. 로그 한 줄은 그 자체로 "한 번 일어난 일" 이라
	// 의미상 이미 delta 이므로 temporality 를 아예 보지 않고 값을 그대로 쓴다.
	if e.Signal != event.SignalMetric {
		return raw, Counted
	}

	switch e.Temporality {
	case event.TemporalityDelta:
		// 단조 카운터의 delta 는 음수일 수 없다. 음수가 오면 벤더나 디코더가 틀린 것이므로
		// 집계를 깎지 않고 버린다.
		if raw < 0 {
			return 0, UnusableValue
		}
		return raw, Counted
	case event.TemporalityCumulative:
		return a.series.observe(seriesIDOf(e), event.CumulativePoint{
			Value: raw, Start: e.StartTS, WatchFrom: a.watchFrom,
		})
	default:
		// §4: UNSPECIFIED 는 조용히 2배 집계되느니 폐기하고 카운트만 올린다.
		return 0, DroppedTemporality
	}
}

// fanOut 은 같은 기여분을 이벤트가 속한 모든 dim 행에 더한다.
// 이벤트 하나가 total 행과 자기 vendor·model·tool·project·type 행에 동시에 기여한다.
func (a *Aggregator) fanOut(e event.Event, delta Bucket) {
	hour := e.Hour()
	for _, dim := range dimOrder {
		key, ok := dimKey(dim, e)
		if !ok {
			continue
		}
		rk := rowKey{hour: hour, dim: dim, key: key}
		b := a.buckets[rk]
		if b == nil {
			b = &Bucket{}
			a.buckets[rk] = b
		}
		b.add(delta)
	}
}

// Aggregate 는 배치 하나를 집계해 행과 통계를 돌려주는 일회용 진입점이다.
// 계열 상태를 유지할 필요가 없는 호출자(테스트, 재집계)를 위한 것이다.
func Aggregate(events []event.Event, opts ...Option) ([]Row, Stats) {
	a := New(opts...)
	a.AddAll(events)
	return a.Rows(), a.Stats()
}

// usable 은 산술에 넣어도 되는 값인지 본다. NaN 이 한 번 들어가면 그 버킷의 cost_usd 는
// 이후 무엇을 더해도 NaN 이라 영구히 복구되지 않는다.
func usable(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
