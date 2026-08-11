package forward

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	logscolpb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metricscolpb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	tracecolpb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/your-org/pulsemetry/internal/contract"
	"github.com/your-org/pulsemetry/internal/otlpdecode"
)

// 픽스처에 심는 표식들. 전부 ASCII 라 protobuf·protojson 어느 쪽으로 인코딩해도
// 바이트 그대로 나타난다 — bytes.Contains 로 유무를 단언할 수 있다.
const (
	secretPrompt   = "SENTINEL-PROMPT-BODY-do-not-forward"
	secretPromptAt = "SENTINEL-PROMPT-ATTR-do-not-forward"
	secretPath     = "/Users/jy/dev/SENTINEL-secret-path/main.go"
	secretEmail    = "sentinel@example.com"
	keptModel      = "claude-sonnet-4-6"
)

// blockAll 은 전부 false 인 Privacy 다. 회사 기본값이며 (§4.6) 이 상태에서 원문이
// 상위로 나가면 안 된다.
func blockAll() contract.Privacy { return contract.Privacy{} }

func testManifest(endpoint string, p contract.Privacy) contract.Manifest {
	return contract.Manifest{
		SchemaVersion: 1,
		OTLP:          contract.OTLP{Endpoint: endpoint, Protocol: "http/protobuf"},
		Signals:       contract.Signals{Logs: true, Metrics: true},
		Privacy:       p,
	}
}

func stringAttr(key, value string) *commonpb.KeyValue {
	return &commonpb.KeyValue{
		Key:   key,
		Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value}},
	}
}

// logsFixture 는 원문·경로·이메일이 전부 들어간 로그 페이로드다. 회사 Privacy 가 전부 false 면
// 이 표식들은 상위로 나가는 본문에서 사라져야 하고, model 같은 무해한 속성은 남아야 한다.
func logsFixture() *logscolpb.ExportLogsServiceRequest {
	return &logscolpb.ExportLogsServiceRequest{
		ResourceLogs: []*logspb.ResourceLogs{{
			Resource: &resourcepb.Resource{
				Attributes: []*commonpb.KeyValue{
					stringAttr("service.name", "claude-code"),
					stringAttr("user.email", secretEmail),
				},
			},
			ScopeLogs: []*logspb.ScopeLogs{{
				Scope: &commonpb.InstrumentationScope{Name: "com.anthropic.claude_code.events"},
				LogRecords: []*logspb.LogRecord{
					{
						TimeUnixNano: 1_700_000_000_000_000_000,
						EventName:    "claude_code.user_prompt",
						Body: &commonpb.AnyValue{
							Value: &commonpb.AnyValue_StringValue{StringValue: secretPrompt},
						},
						Attributes: []*commonpb.KeyValue{
							stringAttr("event.name", "claude_code.user_prompt"),
							stringAttr("prompt", secretPromptAt),
							stringAttr("model", keptModel),
						},
					},
					{
						TimeUnixNano: 1_700_000_001_000_000_000,
						EventName:    "claude_code.tool_result",
						Attributes: []*commonpb.KeyValue{
							stringAttr("event.name", "claude_code.tool_result"),
							stringAttr("tool_name", "Edit"),
							stringAttr("file_path", secretPath),
							stringAttr("model", keptModel),
						},
					},
				},
			}},
		}},
	}
}

func metricsFixture() *metricscolpb.ExportMetricsServiceRequest {
	return &metricscolpb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{{
			Resource: &resourcepb.Resource{
				Attributes: []*commonpb.KeyValue{stringAttr("service.name", "claude-code")},
			},
			ScopeMetrics: []*metricspb.ScopeMetrics{{
				Metrics: []*metricspb.Metric{{
					Name: "claude_code.cost.usage",
					Data: &metricspb.Metric_Sum{Sum: &metricspb.Sum{
						AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA,
						IsMonotonic:            true,
						DataPoints: []*metricspb.NumberDataPoint{{
							TimeUnixNano: 1_700_000_000_000_000_000,
							Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: 0.42},
							Attributes: []*commonpb.KeyValue{
								stringAttr("model", keptModel),
								stringAttr("file_path", secretPath),
							},
						}},
					}},
				}},
			}},
		}},
	}
}

func tracesFixture() *tracecolpb.ExportTraceServiceRequest {
	return &tracecolpb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{{
			ScopeSpans: []*tracepb.ScopeSpans{{
				Spans: []*tracepb.Span{{
					Name:              "tool.Edit",
					TraceId:           []byte("0123456789abcdef"),
					SpanId:            []byte("01234567"),
					StartTimeUnixNano: 1_700_000_000_000_000_000,
					Attributes:        []*commonpb.KeyValue{stringAttr("command", secretPath)},
				}},
			}},
		}},
	}
}

func encodePayload(t *testing.T, msg proto.Message, enc otlpdecode.Encoding) []byte {
	t.Helper()
	var (
		out []byte
		err error
	)
	if enc == otlpdecode.EncodingJSON {
		out, err = protojson.Marshal(msg)
	} else {
		out, err = proto.Marshal(msg)
	}
	if err != nil {
		t.Fatalf("픽스처 인코딩 실패: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("픽스처가 비었음")
	}
	return out
}

// captured 는 상위가 실제로 받은 요청이다.
type captured struct {
	path        string
	contentType string
	auth        string
	body        []byte
}

// upstream 은 회사 Collector 대역이다.
type upstream struct {
	srv *httptest.Server

	mu     sync.Mutex
	reqs   []captured
	status func(n int) int // n 은 1-based 요청 순번
	hold   chan struct{}   // 닫히기 전까지 핸들러를 붙잡는다 (nil 이면 즉시 응답)
	header map[string]string
}

func newUpstream(t *testing.T, status func(n int) int) *upstream {
	t.Helper()
	u := &upstream{status: status}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := readAll(r)
		u.mu.Lock()
		u.reqs = append(u.reqs, captured{
			path:        r.URL.Path,
			contentType: r.Header.Get("Content-Type"),
			auth:        r.Header.Get("Authorization"),
			body:        body,
		})
		n := len(u.reqs)
		hold := u.hold
		header := u.header
		u.mu.Unlock()

		if hold != nil {
			select {
			case <-hold:
			case <-r.Context().Done():
				return
			}
		}
		for k, v := range header {
			w.Header().Set(k, v)
		}
		code := http.StatusOK
		if u.status != nil {
			code = u.status(n)
		}
		w.WriteHeader(code)
	}))
	t.Cleanup(func() {
		u.release()
		u.srv.Close()
	})
	return u
}

func readAll(r *http.Request) ([]byte, error) {
	var buf bytes.Buffer
	_, err := buf.ReadFrom(r.Body)
	return buf.Bytes(), err
}

// block 은 이후 요청을 붙잡아 둔다. 워커가 막힌 상태를 만들 때 쓴다.
func (u *upstream) block() {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.hold == nil {
		u.hold = make(chan struct{})
	}
}

func (u *upstream) release() {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.hold != nil {
		select {
		case <-u.hold:
		default:
			close(u.hold)
		}
	}
}

func (u *upstream) setHeader(k, v string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.header == nil {
		u.header = map[string]string{}
	}
	u.header[k] = v
}

func (u *upstream) requests() []captured {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]captured(nil), u.reqs...)
}

func (u *upstream) count() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.reqs)
}

// syncBuffer 는 워커 고루틴의 로그를 테스트에서 안전하게 읽기 위한 버퍼다.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// fakeTokens 는 순서대로 토큰을 내주는 공급자다. Invalidate 마다 다음 토큰으로 넘어간다.
type fakeTokens struct {
	mu          sync.Mutex
	tokens      []string
	idx         int
	invalidated []string
	err         error
}

func (f *fakeTokens) Token(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return "", f.err
	}
	return f.tokens[f.idx], nil
}

func (f *fakeTokens) Invalidate(stale string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.invalidated = append(f.invalidated, stale)
	if f.idx < len(f.tokens)-1 {
		f.idx++
	}
}

func (f *fakeTokens) invalidations() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.invalidated...)
}

// harness 는 테스트 하나가 쓰는 포워더 한 벌이다.
type harness struct {
	fwd *Forwarder
	up  *upstream
	log *syncBuffer
}

type harnessOpts struct {
	privacy     contract.Privacy
	tokens      TokenSource
	status      func(n int) int
	queueSize   int
	maxAttempts int
	protocol    string
	endpoint    string
	start       bool
	// logTo 를 주면 여러 포워더의 로그를 한 버퍼에 모을 수 있다 (토큰 유출 테스트용).
	logTo *syncBuffer
}

func newHarness(t *testing.T, o harnessOpts) *harness {
	t.Helper()
	up := newUpstream(t, o.status)
	buf := o.logTo
	if buf == nil {
		buf = &syncBuffer{}
	}

	endpoint := o.endpoint
	if endpoint == "" {
		endpoint = up.srv.URL
	}
	manifest := testManifest(endpoint, o.privacy)
	if o.protocol != "" {
		manifest.OTLP.Protocol = o.protocol
	}
	tokens := o.tokens
	if tokens == nil {
		tokens = StaticToken("test-token")
	}

	fwd, err := New(Options{
		Manifest:    manifest,
		Tokens:      tokens,
		Logger:      log.New(buf, "", 0),
		QueueSize:   o.queueSize,
		MaxAttempts: o.maxAttempts,
		// 테스트에서 실제로 기다리지 않도록 백오프를 아주 짧게 한다.
		// 지터 자체는 backoff() 단위 테스트가 따로 확인한다.
		BaseBackoff: time.Millisecond,
		MaxBackoff:  2 * time.Millisecond,
		Timeout:     3 * time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h := &harness{fwd: fwd, up: up, log: buf}
	if o.start {
		fwd.Start()
	}
	return h
}

// shutdown 은 제한 시간 안에 종료를 마치는지까지 확인한다. 감시자가 없으면 종료가
// 무한정 늘어져도 테스트가 통과해 버린다.
func (h *harness) shutdown(t *testing.T, grace time.Duration) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- h.fwd.Shutdown(ctx) }()
	select {
	case err := <-errCh:
		return err
	case <-time.After(grace + 5*time.Second):
		t.Fatal("Shutdown 이 제한 시간을 한참 넘겨도 돌아오지 않음 — 블로킹")
		return nil
	}
}

// waitFor 는 조건이 참이 될 때까지 짧게 기다린다. 폴링 실패는 테스트 실패다.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("조건이 시간 안에 만족되지 않음: %s", what)
}
