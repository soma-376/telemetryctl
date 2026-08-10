package otlpdecode

import (
	"fmt"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	logscolpb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metricscolpb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	tracecolpb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"

	"github.com/your-org/pulsemetry/internal/contract"
)

// ScrubPolicy 는 상위 Collector 로 나가기 전에 지울 것의 목록이다.
//
// **allowlist 가 아니라 denylist 다.** 지울 것만 지우고 나머지는 그대로 흘려보낸다.
// 상위 Collector 는 우리가 모르는 속성도 받을 수 있어야 하고, 로컬 프록시가 벤더의 새 속성을
// 조용히 삼키면 회사 파이프라인이 우리 버전에 묶여 버린다. 로컬 저장 쪽 allowlist(attrs.go)와
// 방향이 반대라는 점이 이 패키지에서 가장 헷갈리는 지점이다 (ADR 0003 Negative).
type ScrubPolicy struct {
	// DropAttributes 는 제거할 속성 키다. resource·scope·데이터포인트·로그 레코드·스팬·
	// 스팬 이벤트까지 모든 계층에 같은 규칙을 적용한다 — 벤더가 어느 계층에 원문을 붙일지
	// 계약으로 고정돼 있지 않다.
	DropAttributes map[string]bool
	// ClearBodySuffixes 는 body 를 비울 로그 레코드의 이름 접미사다. 원문이 속성이 아니라
	// body 에 실려 오는 경우를 잡는다. 접미사로 맞추는 이유는 벤더 접두가 다르기 때문이다.
	ClearBodySuffixes []string
}

// IsZero 는 지울 것이 없는 정책인지 알려준다. 이 경우에도 포워더는 재인코딩을 건너뛰면 안 된다 —
// Content-Type 미러링과 ID 표기 정규화는 그대로 필요하다.
func (p ScrubPolicy) IsZero() bool {
	return len(p.DropAttributes) == 0 && len(p.ClearBodySuffixes) == 0
}

// privacyRules 는 contract.Privacy 필드 하나가 무엇을 막는지 적은 표다.
// manifest 스키마에 Privacy 필드가 늘면 여기에 줄을 추가한다 (ADR 0003 Follow-up).
//
// 각 그룹의 키는 그 플래그가 켜졌을 때에만 벤더가 채우는 것들이다. prompt_length·
// tool_input_bytes 처럼 길이·개수만 담는 속성은 어느 그룹에도 넣지 않는다 — 원문이 아니고,
// 회사가 이미 직결 시절에도 받던 값이다.
var privacyRules = []struct {
	allowed  func(contract.Privacy) bool
	keys     []string
	suffixes []string
}{
	{
		allowed:  func(p contract.Privacy) bool { return p.CollectUserPrompts },
		keys:     []string{"prompt", "user_prompt", "prompt_text"},
		suffixes: []string{"user_prompt"},
	},
	{
		allowed:  func(p contract.Privacy) bool { return p.CollectAssistantResponses },
		keys:     []string{"response", "assistant_response", "response_text", "completion"},
		suffixes: []string{"assistant_response"},
	},
	{
		// tool details = 툴이 무엇을 대상으로 어떻게 불렸는지. 파일 경로·명령이 여기 있다.
		allowed: func(p contract.Privacy) bool { return p.CollectToolDetails },
		keys: []string{
			"tool_input", "tool_parameters", "tool_arguments", "tool.arguments",
			"command", "full_command", "file_path", "file_paths",
		},
	},
	{
		allowed:  func(p contract.Privacy) bool { return p.CollectToolContent },
		keys:     []string{"tool_result", "tool_output", "tool_response"},
		suffixes: []string{"tool_result"},
	},
	{
		allowed: func(p contract.Privacy) bool { return p.CollectUserEmail },
		keys:    []string{"user.email", "user_email"},
	},
	{
		allowed: func(p contract.Privacy) bool { return p.CollectRawAPIBodies },
		keys: []string{
			"request_body", "response_body", "raw_request", "raw_response",
			"http.request.body", "http.response.body",
		},
	},
}

// PolicyFromPrivacy 는 회사 manifest 의 Privacy 를 제거 규칙으로 옮긴다.
// 무엇을 지울지는 전부 여기서 나오고 포워더에는 하드코딩된 규칙이 없다 — manifest 가 항목을
// 허용으로 바꾸면 그 항목은 자동으로 통과된다.
func PolicyFromPrivacy(p contract.Privacy) ScrubPolicy {
	policy := ScrubPolicy{DropAttributes: map[string]bool{}}
	for _, rule := range privacyRules {
		if rule.allowed(p) {
			continue
		}
		for _, key := range rule.keys {
			policy.DropAttributes[key] = true
		}
		policy.ClearBodySuffixes = append(policy.ClearBodySuffixes, rule.suffixes...)
	}
	return policy
}

// ScrubStats 는 무엇을 얼마나 지웠는지다. 포워더가 로그로 남겨 정책이 실제로 동작 중인지
// 확인할 수 있게 한다 (값은 절대 남기지 않는다 — 개수만이다).
type ScrubStats struct {
	AttributesRemoved int
	BodiesCleared     int
}

// Scrub 은 받은 페이로드에서 정책이 금지한 것을 지우고 **같은 인코딩으로** 다시 만든다.
//
// 입력 인코딩을 보존하는 것이 계약이다. protobuf 로 받았으면 protobuf 로, protojson 으로
// 받았으면 protojson 으로 낸다 — 상위 Collector 는 우리가 미러링한 Content-Type 을 믿는다.
func Scrub(kind PayloadKind, data []byte, enc Encoding, p ScrubPolicy) ([]byte, ScrubStats, error) {
	var (
		msg   proto.Message
		stats ScrubStats
	)
	switch kind {
	case PayloadMetrics:
		req := &metricscolpb.ExportMetricsServiceRequest{}
		if err := unmarshalPayload(data, enc, req); err != nil {
			return nil, stats, fmt.Errorf("otlpdecode: metrics 페이로드 디코드: %w", err)
		}
		scrubMetrics(req, p, &stats)
		msg = req
	case PayloadLogs:
		req := &logscolpb.ExportLogsServiceRequest{}
		if err := unmarshalPayload(data, enc, req); err != nil {
			return nil, stats, fmt.Errorf("otlpdecode: logs 페이로드 디코드: %w", err)
		}
		scrubLogs(req, p, &stats)
		msg = req
	case PayloadTraces:
		req := &tracecolpb.ExportTraceServiceRequest{}
		if err := unmarshalPayload(data, enc, req); err != nil {
			return nil, stats, fmt.Errorf("otlpdecode: traces 페이로드 디코드: %w", err)
		}
		scrubTraces(req, p, &stats)
		msg = req
	default:
		return nil, stats, unsupportedKind(kind)
	}

	out, err := marshalPayload(msg, enc)
	if err != nil {
		return nil, stats, err
	}
	return out, stats, nil
}

func marshalPayload(msg proto.Message, enc Encoding) ([]byte, error) {
	if enc == EncodingJSON {
		out, err := protojson.Marshal(msg)
		if err != nil {
			return nil, fmt.Errorf("otlpdecode: protojson 인코딩: %w", err)
		}
		return base64IDsToHex(out)
	}
	out, err := proto.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("otlpdecode: protobuf 인코딩: %w", err)
	}
	return out, nil
}

func scrubMetrics(req *metricscolpb.ExportMetricsServiceRequest, p ScrubPolicy, s *ScrubStats) {
	for _, rm := range req.GetResourceMetrics() {
		if rm.GetResource() != nil {
			rm.Resource.Attributes = filterAttrs(rm.Resource.Attributes, p, s)
		}
		for _, sm := range rm.GetScopeMetrics() {
			if sm.GetScope() != nil {
				sm.Scope.Attributes = filterAttrs(sm.Scope.Attributes, p, s)
			}
			for _, m := range sm.GetMetrics() {
				m.Metadata = filterAttrs(m.GetMetadata(), p, s)
				scrubMetricPoints(m, p, s)
			}
		}
	}
}

func scrubMetricPoints(m *metricspb.Metric, p ScrubPolicy, s *ScrubStats) {
	for _, dp := range m.GetSum().GetDataPoints() {
		dp.Attributes = filterAttrs(dp.Attributes, p, s)
	}
	for _, dp := range m.GetGauge().GetDataPoints() {
		dp.Attributes = filterAttrs(dp.Attributes, p, s)
	}
	for _, dp := range m.GetHistogram().GetDataPoints() {
		dp.Attributes = filterAttrs(dp.Attributes, p, s)
	}
	for _, dp := range m.GetExponentialHistogram().GetDataPoints() {
		dp.Attributes = filterAttrs(dp.Attributes, p, s)
	}
	for _, dp := range m.GetSummary().GetDataPoints() {
		dp.Attributes = filterAttrs(dp.Attributes, p, s)
	}
}

func scrubLogs(req *logscolpb.ExportLogsServiceRequest, p ScrubPolicy, s *ScrubStats) {
	for _, rl := range req.GetResourceLogs() {
		if rl.GetResource() != nil {
			rl.Resource.Attributes = filterAttrs(rl.Resource.Attributes, p, s)
		}
		for _, sl := range rl.GetScopeLogs() {
			if sl.GetScope() != nil {
				sl.Scope.Attributes = filterAttrs(sl.Scope.Attributes, p, s)
			}
			for _, rec := range sl.GetLogRecords() {
				name := rec.GetEventName()
				if name == "" {
					name = attrString(rec.GetAttributes(), "event.name")
				}
				rec.Attributes = filterAttrs(rec.Attributes, p, s)
				if rec.GetBody() != nil && matchesSuffix(name, p.ClearBodySuffixes) {
					// 이름이 원문 이벤트인데 body 가 무엇인지 우리는 확정할 수 없다.
					// 남겨서 유출되느니 비운다 — 회사는 이 이벤트의 본문을 수집하지 않기로 했다.
					rec.Body = nil
					s.BodiesCleared++
				}
			}
		}
	}
}

func scrubTraces(req *tracecolpb.ExportTraceServiceRequest, p ScrubPolicy, s *ScrubStats) {
	for _, rs := range req.GetResourceSpans() {
		if rs.GetResource() != nil {
			rs.Resource.Attributes = filterAttrs(rs.Resource.Attributes, p, s)
		}
		for _, ss := range rs.GetScopeSpans() {
			if ss.GetScope() != nil {
				ss.Scope.Attributes = filterAttrs(ss.Scope.Attributes, p, s)
			}
			for _, span := range ss.GetSpans() {
				span.Attributes = filterAttrs(span.Attributes, p, s)
				for _, e := range span.GetEvents() {
					e.Attributes = filterAttrs(e.Attributes, p, s)
				}
				for _, l := range span.GetLinks() {
					l.Attributes = filterAttrs(l.Attributes, p, s)
				}
			}
		}
	}
}

// filterAttrs 는 정책이 지목한 키만 뺀 슬라이스를 돌려준다. 나머지 키는 순서까지 그대로다.
func filterAttrs(kvs []*commonpb.KeyValue, p ScrubPolicy, s *ScrubStats) []*commonpb.KeyValue {
	if len(kvs) == 0 || len(p.DropAttributes) == 0 {
		return kvs
	}
	kept := kvs[:0]
	for _, kv := range kvs {
		if kv != nil && p.DropAttributes[kv.GetKey()] {
			s.AttributesRemoved++
			continue
		}
		kept = append(kept, kv)
	}
	return kept
}

func attrString(kvs []*commonpb.KeyValue, key string) string {
	for _, kv := range kvs {
		if kv.GetKey() == key {
			return anyString(kv.GetValue())
		}
	}
	return ""
}

func matchesSuffix(name string, suffixes []string) bool {
	if name == "" {
		return false
	}
	for _, suffix := range suffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}
