package otlpdecode

import (
	"encoding/hex"
	"fmt"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	logscolpb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metricscolpb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"

	"github.com/your-org/pulsemetry/internal/event"
)

// Options 는 디코더가 페이로드에서 알아낼 수 없는 값이다.
type Options struct {
	// InstallationID 는 events.installation_id 로 그대로 들어간다. 비어 있으면 모든 이벤트가
	// Validate 에서 거부된다 — 호출자가 반드시 채운다.
	InstallationID string
	// Vendor 는 service.name 과 시그널 이름으로 벤더를 못 알아낸 경우의 폴백이다.
	Vendor string
	// MaxContentBytes 는 원문 항목당 상한이다. 0 이면 DefaultMaxContentBytes.
	MaxContentBytes int
}

func (o Options) maxContent() int {
	if o.MaxContentBytes > 0 {
		return o.MaxContentBytes
	}
	return DefaultMaxContentBytes
}

// Rejected 는 페이로드 안에서 버린 항목 수다. 수신기가 OTLP PartialSuccess 의
// rejected_data_points·rejected_log_records 에 그대로 쓴다.
//
// 부분 실패를 이렇게 다루는 이유: 데이터포인트 하나가 깨졌다고 배치 전체를 버리면
// Claude Code 한 세션이 통째로 사라진다. 정상분은 살리고 버린 수만 정확히 보고한다.
type Rejected struct {
	// Datapoints 는 events 계약(NOT NULL·양수 ts)을 못 맞춘 메트릭 데이터포인트다.
	Datapoints int
	// LogRecords 는 이름이나 타임스탬프가 없어 정규화할 수 없는 로그 레코드다.
	LogRecords int
	// UnspecifiedTemporality 는 Sum.aggregation_temporality 가 UNSPECIFIED 라 폐기한 수다.
	// 조용히 2배 집계되느니 버린다 (계획서 제약 §4).
	UnspecifiedTemporality int
	// UnsupportedMetricType 은 Sum 이 아닌 메트릭 타입이라 폐기한 데이터포인트 수다.
	UnsupportedMetricType int
}

func (r Rejected) Total() int {
	return r.Datapoints + r.LogRecords + r.UnspecifiedTemporality + r.UnsupportedMetricType
}

func (r Rejected) add(o Rejected) Rejected {
	r.Datapoints += o.Datapoints
	r.LogRecords += o.LogRecords
	r.UnspecifiedTemporality += o.UnspecifiedTemporality
	r.UnsupportedMetricType += o.UnsupportedMetricType
	return r
}

// Result 는 페이로드 하나의 디코드 결과다.
//
// Contents 와 Targets 는 Events 와 나란히 놓인 희소 슬라이스다 — 이벤트마다 있지도 않고
// events 테이블에 자리도 없어서 Event 안에 넣지 않았다. 둘 다 EventIndex 로 이벤트를 가리킨다.
type Result struct {
	Events []event.Event
	// Contents 는 원문이다. Events 와 분리돼 있고 event_content 로만 간다 —
	// 상위 Collector 로는 절대 나가지 않는다 (ADR 0003).
	Contents []Content
	// Targets 는 이벤트가 건드린 파일이다. session_files 와 tool_events.target_* 의 원천이고
	// 값은 정규화된 해시+basename 뿐이다.
	Targets  []Target
	Rejected Rejected
}

// Decode 는 시그널 종류에 맞는 디코더를 부른다. traces 는 정규화하지 않고 빈 결과를 준다 —
// events.signal 이 metric|log 두 가지뿐이라 스팬을 담을 자리가 없다. 전달은 그대로 이뤄진다.
func Decode(kind PayloadKind, data []byte, enc Encoding, opt Options) (Result, error) {
	switch kind {
	case PayloadMetrics:
		return DecodeMetrics(data, enc, opt)
	case PayloadLogs:
		return DecodeLogs(data, enc, opt)
	case PayloadTraces:
		return Result{}, nil
	}
	return Result{}, unsupportedKind(kind)
}

// DecodeMetrics 는 ExportMetricsServiceRequest 를 정규화한다.
// 페이로드 자체가 깨졌을 때만 error 를 돌려준다 — 그 외 손실은 전부 Rejected 로 보고한다.
func DecodeMetrics(data []byte, enc Encoding, opt Options) (Result, error) {
	var req metricscolpb.ExportMetricsServiceRequest
	if err := unmarshalPayload(data, enc, &req); err != nil {
		return Result{}, fmt.Errorf("otlpdecode: metrics 페이로드 디코드: %w", err)
	}
	d := newDecoder(opt)
	for _, rm := range req.GetResourceMetrics() {
		base := carrier{}
		base.applyAll(rm.GetResource().GetAttributes())
		for _, sm := range rm.GetScopeMetrics() {
			scoped := base
			scoped.applyAll(sm.GetScope().GetAttributes())
			for _, m := range sm.GetMetrics() {
				d.metric(scoped, m)
			}
		}
	}
	return d.result(), nil
}

// DecodeLogs 는 ExportLogsServiceRequest 를 정규화한다.
func DecodeLogs(data []byte, enc Encoding, opt Options) (Result, error) {
	var req logscolpb.ExportLogsServiceRequest
	if err := unmarshalPayload(data, enc, &req); err != nil {
		return Result{}, fmt.Errorf("otlpdecode: logs 페이로드 디코드: %w", err)
	}
	d := newDecoder(opt)
	for _, rl := range req.GetResourceLogs() {
		base := carrier{}
		base.applyAll(rl.GetResource().GetAttributes())
		for _, sl := range rl.GetScopeLogs() {
			scoped := base
			scoped.applyAll(sl.GetScope().GetAttributes())
			for _, rec := range sl.GetLogRecords() {
				d.logRecord(scoped, rec)
			}
		}
	}
	return d.result(), nil
}

// unmarshalPayload 는 인코딩에 맞춰 역직렬화한다.
//
// protojson 에 DiscardUnknown 을 켜는 이유는 상위 호환 때문이다. 벤더가 우리가 모르는
// 필드를 넣었다고 배치 전체를 400 으로 버리면 새 Claude Code 버전이 나오는 순간 수집이 멎는다.
func unmarshalPayload(data []byte, enc Encoding, msg proto.Message) error {
	if enc == EncodingJSON {
		normalized, err := hexIDsToBase64(data)
		if err != nil {
			return err
		}
		return protojson.UnmarshalOptions{DiscardUnknown: true}.Unmarshal(normalized, msg)
	}
	return proto.Unmarshal(data, msg)
}

// seqKey 는 event.Event.Sequence 를 매기는 단위다. 계약대로 (Name, TS, Attr) 까지 같은
// 데이터포인트만 같은 키를 갖는다 — claude_code.token.usage 의 input·output 처럼 속성이
// 다른 포인트는 각자 0 번이 되고, 진짜 같은 포인트만 0,1,2… 로 갈라진다.
type seqKey struct {
	name string
	ts   event.UnixNano
	attr event.Attributes
}

type decoder struct {
	opt      Options
	events   []event.Event
	contents []Content
	targets  []Target
	rejected Rejected
	seq      map[seqKey]int
}

func newDecoder(opt Options) *decoder {
	return &decoder{opt: opt, seq: map[seqKey]int{}}
}

func (d *decoder) result() Result {
	return Result{Events: d.events, Contents: d.contents, Targets: d.targets, Rejected: d.rejected}
}

// metric 은 메트릭 하나의 데이터포인트를 이벤트로 옮긴다.
//
// Sum 만 정규화한다. events 스키마의 수치 표현이 value REAL 한 칸뿐이고 rollup 이 아는
// 결합 규칙이 delta 합산과 cumulative 차분 둘뿐이라, 다른 타입은 넣을 자리도 해석 규칙도 없다.
//   - Gauge 는 last-value 의미라 합산하면 틀린다. delta 로 가장하면 조용히 부풀고,
//     cumulative 로 가장하면 값이 줄 때 음수 증분이 나온다.
//   - Histogram·ExponentialHistogram·Summary 는 버킷·분위수라 한 칸에 담기지 않는다.
//
// 셋 다 Claude Code·Codex 가 현재 내보내지 않는 타입이다. 그래서 폐기하되 벤더가 나중에
// 내보내기 시작하면 UnsupportedMetricType 카운터로 즉시 드러나게 해 둔다.
func (d *decoder) metric(base carrier, m *metricspb.Metric) {
	sum := m.GetSum()
	if sum == nil {
		d.rejected.UnsupportedMetricType += otherDataPointCount(m)
		return
	}
	temporality, ok := temporalityOf(sum.GetAggregationTemporality())
	if !ok {
		// 계획서 제약 §4 — 가정하지 않는다. delta 로 오인하면 cumulative 카운터가
		// 매 수집 주기마다 전체 누적값으로 더해져 비용이 10배가 된다.
		d.rejected.UnspecifiedTemporality += len(sum.GetDataPoints())
		return
	}
	for _, dp := range sum.GetDataPoints() {
		c := base
		c.applyAll(dp.GetAttributes())

		ev := event.Event{
			Signal:      event.SignalMetric,
			Name:        m.GetName(),
			TS:          metricTimestamp(dp, &c),
			SessionID:   c.sessionID,
			EventID:     c.eventID,
			Temporality: temporality,
			StartTS:     startTimestamp(dp),
			Attr:        c.attr,
			Measure:     c.measure,
		}
		ev.Measure.Unit = m.GetUnit()
		if v, ok := dataPointValue(dp); ok {
			ev.Measure.Value = event.Some(v)
		}
		if !d.emit(ev, &c) {
			d.rejected.Datapoints++
		}
	}
}

// logRecord 는 로그 레코드 하나를 이벤트로 옮긴다.
func (d *decoder) logRecord(base carrier, rec *logspb.LogRecord) {
	if rec == nil {
		d.rejected.LogRecords++
		return
	}
	c := base
	c.applyAll(rec.GetAttributes())

	name := rec.GetEventName()
	if name == "" {
		name = c.eventName
	}
	if kind, ok := bodyContentKind(name); ok {
		if body := anyText(rec.GetBody()); body != "" && !c.content[contentOrdinal(kind)].set {
			c.content[contentOrdinal(kind)] = rawContent{body: body, set: true}
		}
	}

	ev := event.Event{
		Signal:    event.SignalLog,
		Name:      name,
		TS:        logTimestamp(rec, &c),
		SessionID: c.sessionID,
		EventID:   c.eventID,
		TraceID:   idHex(rec.GetTraceId()),
		SpanID:    idHex(rec.GetSpanId()),
		Attr:      c.attr,
		Measure:   c.measure,
	}
	if !d.emit(ev, &c) {
		d.rejected.LogRecords++
	}
}

// emit 은 벤더·installation_id·sequence 를 채우고 검증을 통과한 이벤트만 결과에 넣는다.
func (d *decoder) emit(ev event.Event, c *carrier) bool {
	ev.Vendor = vendorOf(c.serviceName, ev.Name, d.opt.Vendor)
	ev.InstallationID = d.opt.InstallationID
	applyContentMeasures(&ev.Measure, c)
	if err := ev.Validate(); err != nil {
		return false
	}

	key := seqKey{name: ev.Name, ts: ev.TS, attr: ev.Attr}
	ev.Sequence = d.seq[key]
	d.seq[key]++

	index := len(d.events)
	d.events = append(d.events, ev)
	dedupKey := ev.DedupKey()
	d.appendContents(index, dedupKey, c)
	d.appendTarget(index, dedupKey, c)
	return true
}

func (d *decoder) appendContents(index int, dedupKey string, c *carrier) {
	max := d.opt.maxContent()
	for i := range c.content {
		if !c.content[i].set {
			continue
		}
		body, truncated := capContent(c.content[i].body, max)
		d.contents = append(d.contents, Content{
			EventIndex: index,
			DedupKey:   dedupKey,
			Content: event.Content{
				Kind:      contentKindByOrdinal[i],
				Body:      body,
				Truncated: truncated,
			},
		})
	}
}

// appendTarget 은 대상 파일이 있는 이벤트에만 행을 만든다. 파일을 건드리지 않은 이벤트
// (프롬프트·API 요청·명령 실행)는 Targets 에 나타나지 않는다.
func (d *decoder) appendTarget(index int, dedupKey string, c *carrier) {
	if c.target.Hash == "" {
		return
	}
	d.targets = append(d.targets, Target{EventIndex: index, DedupKey: dedupKey, Path: c.target})
}

// temporalityOf 는 proto 의 aggregation_temporality 를 실제로 읽어 옮긴다.
// 벤더 기본값을 가정하지 않는다 — 사용자가 OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE
// 를 덮어쓸 수 있다.
func temporalityOf(t metricspb.AggregationTemporality) (event.Temporality, bool) {
	switch t {
	case metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA:
		return event.TemporalityDelta, true
	case metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE:
		return event.TemporalityCumulative, true
	}
	return event.TemporalityUnspecified, false
}

func dataPointValue(dp *metricspb.NumberDataPoint) (float64, bool) {
	switch v := dp.GetValue().(type) {
	case *metricspb.NumberDataPoint_AsDouble:
		return v.AsDouble, true
	case *metricspb.NumberDataPoint_AsInt:
		return float64(v.AsInt), true
	}
	return 0, false
}

// metricTimestamp 는 time_unix_nano 를 쓰고, 없으면 start_time_unix_nano, 그것도 없으면
// event.timestamp 속성을 쓴다. 전부 없으면 0 이 되어 Validate 가 거부한다.
func metricTimestamp(dp *metricspb.NumberDataPoint, c *carrier) event.UnixNano {
	if ts := dp.GetTimeUnixNano(); ts != 0 {
		return event.UnixNano(ts)
	}
	if ts := dp.GetStartTimeUnixNano(); ts != 0 {
		return event.UnixNano(ts)
	}
	return c.tsFallback
}

// startTimestamp 는 NumberDataPoint.start_time_unix_nano 를 event.Event.StartTS 로 옮긴다.
// rollup 이 cumulative 리셋과 콜드 스타트를 판정하는 유일한 근거다 — 값의 증감만으로는
// "카운터가 다시 시작했다" 와 "값이 줄었다" 를 구분할 수 없다.
//
// 로그에는 대응 필드가 없다. Sum 데이터포인트에만 채워진다.
func startTimestamp(dp *metricspb.NumberDataPoint) event.UnixNano {
	return event.UnixNano(dp.GetStartTimeUnixNano())
}

func logTimestamp(rec *logspb.LogRecord, c *carrier) event.UnixNano {
	if ts := rec.GetTimeUnixNano(); ts != 0 {
		return event.UnixNano(ts)
	}
	if ts := rec.GetObservedTimeUnixNano(); ts != 0 {
		return event.UnixNano(ts)
	}
	return c.tsFallback
}

// idHex 는 trace_id·span_id 를 events 의 TEXT 컬럼 표기(소문자 hex)로 옮긴다.
// 전부 0 인 ID 는 "없음"이라는 뜻이므로 빈 문자열로 만든다 (OTLP 스펙).
func idHex(id []byte) string {
	if len(id) == 0 {
		return ""
	}
	for _, b := range id {
		if b != 0 {
			return hex.EncodeToString(id)
		}
	}
	return ""
}

// otherDataPointCount 는 Sum 이 아닌 메트릭이 몇 개의 포인트를 들고 있었는지 센다.
// 폐기 건수를 정확히 보고하기 위한 것뿐이다.
func otherDataPointCount(m *metricspb.Metric) int {
	switch {
	case m.GetGauge() != nil:
		return len(m.GetGauge().GetDataPoints())
	case m.GetHistogram() != nil:
		return len(m.GetHistogram().GetDataPoints())
	case m.GetExponentialHistogram() != nil:
		return len(m.GetExponentialHistogram().GetDataPoints())
	case m.GetSummary() != nil:
		return len(m.GetSummary().GetDataPoints())
	}
	// data oneof 가 비어 있는 메트릭. 포인트는 없지만 버렸다는 사실은 남긴다.
	return 1
}
