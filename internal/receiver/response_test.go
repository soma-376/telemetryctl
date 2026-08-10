package receiver

import (
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	logscolpb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metricscolpb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	tracecolpb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"

	"github.com/your-org/pulsemetry/internal/otlpdecode"
)

// response.go 는 proto 라이브러리 없이 손으로 바이트를 만든다 (otlpdecode 가 못박은
// proto 격리를 지키려고). 손으로 만든 것이 진짜 OTLP 메시지인지 확인할 방법은
// **실제 proto 정의로 역직렬화해 보는 것뿐**이라, 이 검증만 테스트 파일에서 한다.
// 프로덕션 코드에는 proto import 가 없다 — 이 파일을 지워도 패키지는 그대로 빌드된다.

func TestPartialSuccessProtoRoundTrip(t *testing.T) {
	tests := []struct {
		name         string
		kind         otlpdecode.PayloadKind
		rejected     int64
		message      string
		wantRejected int64
	}{
		{name: "큐 포화 경고 (rejected=0)", kind: otlpdecode.PayloadLogs, message: queueFullMessage},
		{name: "로그 일부 거부", kind: otlpdecode.PayloadLogs, rejected: 7, message: "rejected", wantRejected: 7},
		{name: "메트릭 일부 거부", kind: otlpdecode.PayloadMetrics, rejected: 3, message: "rejected", wantRejected: 3},
		{name: "스팬 일부 거부", kind: otlpdecode.PayloadTraces, rejected: 5, message: "rejected", wantRejected: 5},
		{name: "큰 값도 varint 로 정확히", kind: otlpdecode.PayloadMetrics, rejected: 1 << 40, message: "x", wantRejected: 1 << 40},
		{name: "멀티바이트 메시지", kind: otlpdecode.PayloadLogs, message: "큐가 가득 찼습니다"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := partialSuccessBody(tt.kind, otlpdecode.EncodingProtobuf, tt.rejected, tt.message)
			gotRejected, gotMessage := unmarshalPartialSuccess(t, tt.kind, raw)
			if gotRejected != tt.wantRejected {
				t.Errorf("rejected = %d, want %d", gotRejected, tt.wantRejected)
			}
			if gotMessage != tt.message {
				t.Errorf("error_message = %q, want %q", gotMessage, tt.message)
			}
		})
	}
}

// JSON 쪽도 protojson 이 실제로 읽을 수 있어야 한다. 필드 이름이나 int64 표기가
// 어긋나면 exporter 는 응답을 해석하지 못하고 전송 실패로 처리한다.
func TestPartialSuccessJSONParsesWithProtojson(t *testing.T) {
	tests := []struct {
		name         string
		kind         otlpdecode.PayloadKind
		rejected     int64
		message      string
		wantRejected int64
	}{
		{name: "logs 경고", kind: otlpdecode.PayloadLogs, message: queueFullMessage},
		{name: "logs 거부 수", kind: otlpdecode.PayloadLogs, rejected: 4, message: "m", wantRejected: 4},
		{name: "metrics 거부 수", kind: otlpdecode.PayloadMetrics, rejected: 9, message: "m", wantRejected: 9},
		{name: "traces 거부 수", kind: otlpdecode.PayloadTraces, rejected: 2, message: "m", wantRejected: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := partialSuccessBody(tt.kind, otlpdecode.EncodingJSON, tt.rejected, tt.message)
			gotRejected, gotMessage := unmarshalPartialSuccessJSON(t, tt.kind, raw)
			if gotRejected != tt.wantRejected {
				t.Errorf("rejected = %d, want %d (본문 %s)", gotRejected, tt.wantRejected, raw)
			}
			if gotMessage != tt.message {
				t.Errorf("error_message = %q, want %q", gotMessage, tt.message)
			}
		})
	}
}

// 성공 응답은 partial_success 가 없는 빈 메시지다 — protobuf 0 바이트, JSON "{}".
func TestSuccessBodyIsEmptyMessage(t *testing.T) {
	if got := partialSuccessBody(otlpdecode.PayloadLogs, otlpdecode.EncodingProtobuf, 0, ""); len(got) != 0 {
		t.Errorf("protobuf 성공 본문 = %v, want 빈 바이트", got)
	}
	if got := string(partialSuccessBody(otlpdecode.PayloadLogs, otlpdecode.EncodingJSON, 0, "")); got != "{}" {
		t.Errorf("JSON 성공 본문 = %q, want {}", got)
	}

	// 빈 protobuf 본문이 실제로 빈 Export*ServiceResponse 로 읽혀야 한다.
	var resp logscolpb.ExportLogsServiceResponse
	if err := proto.Unmarshal(nil, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.GetPartialSuccess() != nil {
		t.Error("빈 메시지에 partial_success 가 있다")
	}
}

func unmarshalPartialSuccess(t *testing.T, kind otlpdecode.PayloadKind, raw []byte) (int64, string) {
	t.Helper()
	switch kind {
	case otlpdecode.PayloadLogs:
		var resp logscolpb.ExportLogsServiceResponse
		if err := proto.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("손으로 만든 바이트가 ExportLogsServiceResponse 가 아니다: %v", err)
		}
		return resp.GetPartialSuccess().GetRejectedLogRecords(), resp.GetPartialSuccess().GetErrorMessage()
	case otlpdecode.PayloadTraces:
		var resp tracecolpb.ExportTraceServiceResponse
		if err := proto.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("손으로 만든 바이트가 ExportTraceServiceResponse 가 아니다: %v", err)
		}
		return resp.GetPartialSuccess().GetRejectedSpans(), resp.GetPartialSuccess().GetErrorMessage()
	default:
		var resp metricscolpb.ExportMetricsServiceResponse
		if err := proto.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("손으로 만든 바이트가 ExportMetricsServiceResponse 가 아니다: %v", err)
		}
		return resp.GetPartialSuccess().GetRejectedDataPoints(), resp.GetPartialSuccess().GetErrorMessage()
	}
}

func unmarshalPartialSuccessJSON(t *testing.T, kind otlpdecode.PayloadKind, raw []byte) (int64, string) {
	t.Helper()
	opts := protojson.UnmarshalOptions{}
	switch kind {
	case otlpdecode.PayloadLogs:
		var resp logscolpb.ExportLogsServiceResponse
		if err := opts.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("protojson 이 응답을 읽지 못했다 %s: %v", raw, err)
		}
		return resp.GetPartialSuccess().GetRejectedLogRecords(), resp.GetPartialSuccess().GetErrorMessage()
	case otlpdecode.PayloadTraces:
		var resp tracecolpb.ExportTraceServiceResponse
		if err := opts.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("protojson 이 응답을 읽지 못했다 %s: %v", raw, err)
		}
		return resp.GetPartialSuccess().GetRejectedSpans(), resp.GetPartialSuccess().GetErrorMessage()
	default:
		var resp metricscolpb.ExportMetricsServiceResponse
		if err := opts.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("protojson 이 응답을 읽지 못했다 %s: %v", raw, err)
		}
		return resp.GetPartialSuccess().GetRejectedDataPoints(), resp.GetPartialSuccess().GetErrorMessage()
	}
}

func TestValidProtobufFraming(t *testing.T) {
	tests := []struct {
		name string
		body []byte
		want bool
	}{
		{name: "빈 바이트", body: nil, want: true},
		{name: "varint 필드", body: []byte{0x08, 0x96, 0x01}, want: true},
		{name: "LEN 필드", body: []byte{0x0a, 0x03, 'a', 'b', 'c'}, want: true},
		{name: "fixed64", body: []byte{0x09, 1, 2, 3, 4, 5, 6, 7, 8}, want: true},
		{name: "fixed32", body: []byte{0x0d, 1, 2, 3, 4}, want: true},
		{name: "varint 가 잘림", body: []byte{0x08, 0x96}, want: false},
		{name: "LEN 길이가 본문 초과", body: []byte{0x0a, 0x09, 'a'}, want: false},
		{name: "fixed64 가 잘림", body: []byte{0x09, 1, 2, 3}, want: false},
		{name: "와이어타입 3 (group)", body: []byte{0x0b}, want: false},
		{name: "와이어타입 6", body: []byte{0x0e, 0x01}, want: false},
		{name: "필드 번호 0", body: []byte{0x00, 0x01}, want: false},
		{name: "텍스트", body: []byte("not-a-protobuf"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validProtobufFraming(tt.body); got != tt.want {
				t.Errorf("validProtobufFraming(%v) = %v, want %v", tt.body, got, tt.want)
			}
		})
	}
}

// 실제 OTLP protobuf 페이로드는 반드시 프레이밍 검사를 통과해야 한다.
// 여기서 false negative 가 나면 정상 트래픽이 400 으로 거부된다.
func TestRealOTLPProtobufPassesFraming(t *testing.T) {
	req := &logscolpb.ExportLogsServiceRequest{}
	raw, err := proto.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if !validProtobufFraming(raw) {
		t.Fatal("빈 ExportLogsServiceRequest 가 프레이밍 검사에서 거부됐다")
	}

	full, err := proto.Marshal(sampleLogsRequest())
	if err != nil {
		t.Fatal(err)
	}
	if !validProtobufFraming(full) {
		t.Fatal("정상 ExportLogsServiceRequest 가 프레이밍 검사에서 거부됐다")
	}
}

// sampleLogsRequest 는 이벤트 하나가 나오는 최소 OTLP 로그 요청이다.
func sampleLogsRequest() *logscolpb.ExportLogsServiceRequest {
	str := func(s string) *commonpb.AnyValue {
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: s}}
	}
	return &logscolpb.ExportLogsServiceRequest{
		ResourceLogs: []*logspb.ResourceLogs{{
			Resource: &resourcepb.Resource{Attributes: []*commonpb.KeyValue{
				{Key: "service.name", Value: str("claude-code")},
				{Key: "session.id", Value: str("sess-proto")},
			}},
			ScopeLogs: []*logspb.ScopeLogs{{
				LogRecords: []*logspb.LogRecord{{
					TimeUnixNano: 1750000000000000000,
					EventName:    "claude_code.user_prompt",
					Body:         str("안녕"),
				}},
			}},
		}},
	}
}

// protobuf 경로도 끝까지 동작해야 한다. JSON 만 테스트하면 벤더가 protobuf 를 쓰는
// 순간 조용히 절반이 깨진다 — 어느 인코딩을 쓸지 우리가 고정할 수 없다.
func TestProtobufIngestEndToEnd(t *testing.T) {
	rc, sink, _ := newTestReceiver(t, nil)

	raw, err := proto.Marshal(sampleLogsRequest())
	if err != nil {
		t.Fatal(err)
	}
	rec := do(rc, authedRequest("POST", "/v1/logs", "application/x-protobuf", raw))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200 (body=%q)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/x-protobuf" {
		t.Errorf("Content-Type = %q, want application/x-protobuf", got)
	}
	if err := rc.Close(); err != nil {
		t.Fatal(err)
	}
	batches := sink.snapshot()
	if len(batches) != 1 || len(batches[0].Result.Events) != 1 {
		t.Fatalf("protobuf 배치가 정규화되지 않았다: %+v", batches)
	}
	if got := batches[0].Result.Events[0].SessionID; got != "sess-proto" {
		t.Errorf("session_id = %q, want sess-proto", got)
	}
}
