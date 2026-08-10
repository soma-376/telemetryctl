package receiver

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/otlpdecode"
)

// 계획서 「테스트 전략」 §4 가 지정한 라우팅이다. 그 외 경로는 404 여야 한다 —
// 로컬에 열린 HTTP 표면이 의도한 것보다 넓어지면 안 된다.
func TestRouting(t *testing.T) {
	rc, _, _ := newTestReceiver(t, nil)

	tests := []struct {
		name   string
		method string
		path   string
		want   int
	}{
		{"metrics", http.MethodPost, "/v1/metrics", http.StatusOK},
		{"logs", http.MethodPost, "/v1/logs", http.StatusOK},
		{"traces", http.MethodPost, "/v1/traces", http.StatusOK},
		{"후행 슬래시도 같은 시그널", http.MethodPost, "/v1/logs/", http.StatusOK},
		{"healthz 는 GET", http.MethodGet, HealthPath, http.StatusOK},
		{"healthz HEAD", http.MethodHead, HealthPath, http.StatusOK},
		{"healthz 에 POST 는 405", http.MethodPost, HealthPath, http.StatusMethodNotAllowed},
		{"OPTIONS 는 405", http.MethodOptions, "/v1/logs", http.StatusMethodNotAllowed},
		{"healthz OPTIONS 도 405", http.MethodOptions, HealthPath, http.StatusMethodNotAllowed},
		{"시그널에 GET 은 405", http.MethodGet, "/v1/logs", http.StatusMethodNotAllowed},
		{"루트는 404", http.MethodGet, "/", http.StatusNotFound},
		{"알 수 없는 시그널은 404", http.MethodPost, "/v1/profiles", http.StatusNotFound},
		{"디버그 경로 없음", http.MethodGet, "/debug/pprof/", http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := authedRequest(tt.method, tt.path, "application/json", minimalLogsJSON())
			rec := do(rc, req)
			if rec.Code != tt.want {
				t.Errorf("%s %s = %d, want %d (body=%q)", tt.method, tt.path, rec.Code, tt.want, rec.Body.String())
			}
			assertNoCORS(t, rec)
		})
	}
}

// 인증 3중 중 두 가지(헤더)를 여기서 검증한다. 어느 쪽이 빠져도 401 이고,
// 응답은 어느 검사에서 걸렸는지 알려 주지 않는다.
func TestAuth(t *testing.T) {
	rc, _, _ := newTestReceiver(t, nil)

	tests := []struct {
		name    string
		headers map[string]string
		want    int
	}{
		{
			name:    "정상",
			headers: map[string]string{"Authorization": "Bearer " + testToken, LocalHeader: "1"},
			want:    http.StatusOK,
		},
		{
			name:    "bearer 스킴 대소문자 무시",
			headers: map[string]string{"Authorization": "bearer " + testToken, LocalHeader: "1"},
			want:    http.StatusOK,
		},
		{
			name:    "Authorization 없음",
			headers: map[string]string{LocalHeader: "1"},
			want:    http.StatusUnauthorized,
		},
		{
			name:    "토큰 불일치",
			headers: map[string]string{"Authorization": "Bearer wrong-token", LocalHeader: "1"},
			want:    http.StatusUnauthorized,
		},
		{
			name:    "토큰 접두만 일치",
			headers: map[string]string{"Authorization": "Bearer " + testToken[:5], LocalHeader: "1"},
			want:    http.StatusUnauthorized,
		},
		{
			name:    "스킴 없이 토큰만",
			headers: map[string]string{"Authorization": testToken, LocalHeader: "1"},
			want:    http.StatusUnauthorized,
		},
		{
			name:    "X-Pulsemetry-Local 누락",
			headers: map[string]string{"Authorization": "Bearer " + testToken},
			want:    http.StatusUnauthorized,
		},
		{
			name:    "X-Pulsemetry-Local 값이 다름",
			headers: map[string]string{"Authorization": "Bearer " + testToken, LocalHeader: "0"},
			want:    http.StatusUnauthorized,
		},
		{
			name:    "둘 다 없음",
			headers: map[string]string{},
			want:    http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/logs", bytes.NewReader(minimalLogsJSON()))
			req.Header.Set("Content-Type", "application/json")
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			rec := do(rc, req)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d", rec.Code, tt.want)
			}
			if tt.want == http.StatusUnauthorized && strings.Contains(rec.Body.String(), testToken) {
				t.Fatal("401 응답 본문에 토큰이 되비쳐 나감")
			}
		})
	}
}

// /healthz 는 인증 없이 열려 있어야 한다. status 명령이 데몬 생존을 확정하는 경로다.
func TestHealthzNeedsNoAuth(t *testing.T) {
	rc, _, _ := newTestReceiver(t, nil)

	rec := do(rc, httptest.NewRequest(http.MethodGet, HealthPath, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz = %d, want 200", rec.Code)
	}
	var body healthBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("healthz 본문 파싱: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, want ok", body.Status)
	}
	if body.QueueCapacity != DefaultQueueSize {
		t.Errorf("queue_capacity = %d, want %d", body.QueueCapacity, DefaultQueueSize)
	}
	if strings.Contains(rec.Body.String(), testToken) {
		t.Fatal("healthz 응답에 토큰이 들어 있음")
	}
	assertNoCORS(t, rec)
}

func TestUnsupportedMediaType(t *testing.T) {
	rc, _, _ := newTestReceiver(t, nil)

	tests := []struct {
		name            string
		contentType     string
		contentEncoding string
		want            int
	}{
		{name: "protobuf", contentType: "application/x-protobuf", want: http.StatusOK},
		{name: "protobuf 별칭", contentType: "application/protobuf", want: http.StatusOK},
		{name: "json", contentType: "application/json", want: http.StatusOK},
		{name: "json + charset", contentType: "application/json; charset=utf-8", want: http.StatusOK},
		{name: "Content-Type 없음", contentType: "", want: http.StatusUnsupportedMediaType},
		{name: "text/plain", contentType: "text/plain", want: http.StatusUnsupportedMediaType},
		{name: "grpc", contentType: "application/grpc", want: http.StatusUnsupportedMediaType},
		{
			name:            "brotli 압축은 지원하지 않음",
			contentType:     "application/json",
			contentEncoding: "br",
			want:            http.StatusUnsupportedMediaType,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// protobuf 경로에는 빈 본문을 쓴다 — 빈 메시지는 유효하다.
			body := []byte(nil)
			if strings.Contains(tt.contentType, "json") {
				body = minimalLogsJSON()
			}
			req := authedRequest(http.MethodPost, "/v1/logs", tt.contentType, body)
			if tt.contentEncoding != "" {
				req.Header.Set("Content-Encoding", tt.contentEncoding)
			}
			rec := do(rc, req)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d (body=%q)", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}

// 4 MiB 상한. 초과분은 413 이다.
func TestRequestEntityTooLarge(t *testing.T) {
	const max = 1 << 10
	rc, _, _ := newTestReceiver(t, func(o *Options) { o.MaxBodyBytes = max })

	tests := []struct {
		name   string
		filler int
		want   int
	}{
		{name: "상한 이하", filler: max - 64, want: http.StatusOK},
		{name: "상한 초과", filler: max * 4, want: http.StatusRequestEntityTooLarge},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := append([]byte(`{"filler":"`), bytes.Repeat([]byte("a"), tt.filler)...)
			body = append(body, `"}`...)
			rec := do(rc, authedRequest(http.MethodPost, "/v1/logs", "application/json", body))
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d (본문 %d바이트, 상한 %d)", rec.Code, tt.want, len(body), max)
			}
		})
	}
}

// 프레이밍은 통과했지만 OTLP 메시지가 아닌 페이로드는 워커에서 조용히 버려진다.
// 이미 200 으로 응답한 뒤라 클라이언트에 알릴 방법이 없고, 흔적은 카운터와 로그에만 남는다.
func TestDecodeErrorIsCountedAfterAccept(t *testing.T) {
	rc, sink, logs := newTestReceiver(t, nil)

	// 문법은 맞는 JSON 객체지만 resourceLogs 의 타입이 스키마와 어긋난다.
	// 프레이밍 검사는 통과하므로 핸들러는 200 으로 받아들이고 워커가 실패한다.
	body := []byte(`{"resourceLogs": 5}`)
	if !validPayloadFraming(body, otlpdecode.EncodingJSON) {
		t.Fatal("이 픽스처는 프레이밍 검사를 통과해야 의미가 있다")
	}
	if rec := do(rc, authedRequest(http.MethodPost, "/v1/logs", "application/json", body)); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if err := rc.Close(); err != nil {
		t.Fatal(err)
	}
	if got := rc.Stats().DecodeErrors; got != 1 {
		t.Errorf("decode_errors = %d, want 1", got)
	}
	if got := len(sink.snapshot()); got != 0 {
		t.Errorf("디코드 실패 배치가 sink 로 넘어갔다: %d", got)
	}
	if !strings.Contains(logs.String(), "수신 배치 디코드 실패") {
		t.Errorf("실패가 로그에 남지 않았다: %q", logs.String())
	}
}

// SinkFunc 어댑터로도 배선할 수 있어야 한다 (9단계가 클로저로 꽂을 수 있게).
func TestSinkFuncAdapter(t *testing.T) {
	got := make(chan Batch, 1)
	rc, err := New(Options{
		Token:  testToken,
		Decode: otlpdecode.Options{InstallationID: "inst_test"},
		Sink: SinkFunc(func(_ context.Context, b Batch) error {
			got <- b
			return nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()

	if rec := do(rc, authedRequest(http.MethodPost, "/v1/logs", "application/json", minimalLogsJSON())); rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	select {
	case b := <-got:
		if len(b.Result.Events) != 1 {
			t.Fatalf("이벤트 수 = %d", len(b.Result.Events))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SinkFunc 이 호출되지 않았다")
	}
}

// gzip 폭탄: 압축된 크기는 작지만 풀면 상한을 훨씬 넘는다.
// 캡을 **압축 해제 후** 크기로 걸어야만 413 이 나온다.
func TestGzipBombCappedAfterDecompression(t *testing.T) {
	const max = 64 << 10
	rc, sink, _ := newTestReceiver(t, func(o *Options) { o.MaxBodyBytes = max })

	bomb := gzipBytes(t, bytes.Repeat([]byte("0"), max*64)) // 4 MiB → 수 KB
	if len(bomb) >= max {
		t.Fatalf("압축된 폭탄이 %d 바이트라 압축 단계에서 이미 걸린다 — 이 테스트가 무의미해짐", len(bomb))
	}

	req := authedRequest(http.MethodPost, "/v1/logs", "application/json", bomb)
	req.Header.Set("Content-Encoding", "gzip")
	rec := do(rc, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("gzip 폭탄 status = %d, want 413", rec.Code)
	}
	if got := rc.Stats().TooLarge; got != 1 {
		t.Errorf("too_large 카운터 = %d, want 1", got)
	}

	// 정상 크기의 gzip 은 통과하고 워커까지 도달해야 한다.
	ok := gzipBytes(t, minimalLogsJSON())
	req = authedRequest(http.MethodPost, "/v1/logs", "application/json", ok)
	req.Header.Set("Content-Encoding", "gzip")
	if rec := do(rc, req); rec.Code != http.StatusOK {
		t.Fatalf("정상 gzip status = %d, want 200", rec.Code)
	}
	if err := rc.Close(); err != nil {
		t.Fatal(err)
	}
	if got := len(sink.snapshot()); got != 1 {
		t.Fatalf("sink 가 받은 배치 = %d, want 1", got)
	}
}

// gzip 헤더가 아닌 본문에 gzip 을 선언하면 400 이다.
func TestGzipHeaderMismatch(t *testing.T) {
	rc, _, _ := newTestReceiver(t, nil)
	req := authedRequest(http.MethodPost, "/v1/logs", "application/json", minimalLogsJSON())
	req.Header.Set("Content-Encoding", "gzip")
	if rec := do(rc, req); rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// 깨진 페이로드는 400 이다. 재시도해도 성공하지 않을 요청이라 즉시 알려 준다.
func TestMalformedPayload(t *testing.T) {
	rc, _, _ := newTestReceiver(t, nil)

	tests := []struct {
		name        string
		contentType string
		body        []byte
		want        int
	}{
		{
			name:        "protobuf 로 위장한 텍스트",
			contentType: "application/x-protobuf",
			body:        []byte("this is definitely not a protobuf message"),
			want:        http.StatusBadRequest,
		},
		{
			name:        "protobuf 길이 접두가 잘림",
			contentType: "application/x-protobuf",
			body:        []byte{0x0a, 0xff},
			want:        http.StatusBadRequest,
		},
		{
			name:        "protobuf LEN 이 본문보다 김",
			contentType: "application/x-protobuf",
			body:        []byte{0x0a, 0x40, 0x01},
			want:        http.StatusBadRequest,
		},
		{
			name:        "protobuf 와이어타입 7",
			contentType: "application/x-protobuf",
			body:        []byte{0x0f, 0x01},
			want:        http.StatusBadRequest,
		},
		{
			name:        "빈 protobuf 는 유효한 빈 메시지",
			contentType: "application/x-protobuf",
			body:        nil,
			want:        http.StatusOK,
		},
		{
			name:        "깨진 JSON",
			contentType: "application/json",
			body:        []byte(`{"resourceLogs": [`),
			want:        http.StatusBadRequest,
		},
		{
			name:        "JSON 최상위가 배열",
			contentType: "application/json",
			body:        []byte(`[]`),
			want:        http.StatusBadRequest,
		},
		{
			name:        "정상 JSON",
			contentType: "application/json",
			body:        minimalLogsJSON(),
			want:        http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(rc, authedRequest(http.MethodPost, "/v1/logs", tt.contentType, tt.body))
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d (body=%q)", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}

// 응답 Content-Type 은 요청 것을 그대로 미러링한다. 어긋나면 exporter 가 파싱에 실패해
// "전송 실패" 로 판단하고 재전송한다 — 우리는 이미 받았는데.
func TestResponseContentTypeMirrorsRequest(t *testing.T) {
	rc, _, _ := newTestReceiver(t, nil)

	tests := []struct {
		name        string
		contentType string
		body        []byte
		wantType    string
		wantBody    string
	}{
		{
			name:        "json 요청 → json 응답",
			contentType: "application/json",
			body:        minimalLogsJSON(),
			wantType:    "application/json",
			wantBody:    "{}",
		},
		{
			name:        "json + charset 도 json 응답",
			contentType: "application/json; charset=utf-8",
			body:        minimalLogsJSON(),
			wantType:    "application/json",
			wantBody:    "{}",
		},
		{
			name:        "protobuf 요청 → protobuf 응답, 본문은 빈 메시지",
			contentType: "application/x-protobuf",
			body:        nil,
			wantType:    "application/x-protobuf",
			wantBody:    "",
		},
		{
			name:        "protobuf 별칭도 x-protobuf 로 답한다",
			contentType: "application/protobuf",
			body:        nil,
			wantType:    "application/x-protobuf",
			wantBody:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(rc, authedRequest(http.MethodPost, "/v1/logs", tt.contentType, tt.body))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if got := rec.Header().Get("Content-Type"); got != tt.wantType {
				t.Errorf("Content-Type = %q, want %q", got, tt.wantType)
			}
			if got := rec.Body.String(); got != tt.wantBody {
				t.Errorf("본문 = %q, want %q", got, tt.wantBody)
			}
		})
	}
}

// §5.4 의 핵심. 큐가 가득 차면 429 가 아니라 200 + PartialSuccess 다 —
// 429 는 exporter 재시도를 유발해 Claude Code 종료를 지연시킨다.
func TestQueueFullRespondsPartialSuccessNot429(t *testing.T) {
	gate := make(chan struct{})
	sink := &collector{gate: gate}
	rc, err := New(Options{
		Token:     testToken,
		Sink:      sink,
		Decode:    otlpdecode.Options{InstallationID: "inst_test"},
		QueueSize: 2,
		Workers:   1,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rc.Close() })

	// 워커 1 + 큐 2 = 최대 3 개만 받아 줄 수 있다. 10 개를 보내면 반드시 포화한다.
	const requests = 10
	partial := 0
	for i := range requests {
		rec := do(rc, authedRequest(http.MethodPost, "/v1/logs", "application/json", minimalLogsJSON()))
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("요청 %d 가 429 로 응답했다 — exporter 재시도를 유발한다", i)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("요청 %d status = %d, want 200", i, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "partialSuccess") {
			partial++
			var resp jsonExportResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("PartialSuccess 파싱: %v", err)
			}
			if resp.PartialSuccess == nil || resp.PartialSuccess.ErrorMessage == "" {
				t.Fatalf("PartialSuccess 에 errorMessage 가 없다: %s", rec.Body.String())
			}
		}
	}
	if partial == 0 {
		t.Fatal("큐가 포화했는데도 PartialSuccess 응답이 하나도 없었다")
	}
	if got := rc.Stats().Dropped; got == 0 {
		t.Fatal("dropped 카운터가 올라가지 않았다")
	}

	// 워커를 풀어 주면 큐에 남아 있던 배치는 정상 처리돼야 한다.
	close(gate)
	if err := rc.Close(); err != nil {
		t.Fatal(err)
	}
	if len(sink.snapshot()) == 0 {
		t.Fatal("큐에 들어갔던 배치가 sink 에 도달하지 않았다")
	}
	if accepted := rc.Stats().Accepted; int(accepted)+partial != requests {
		t.Fatalf("accepted(%d) + dropped(%d) != 요청 수(%d)", accepted, partial, requests)
	}
}

// 워커가 디코드한 결과가 Sink 로 넘어가고, 원본 바이트도 함께 실려야 한다 —
// forward 가 그 바이트를 Scrub 해서 상위로 보낸다.
func TestSinkReceivesDecodedBatch(t *testing.T) {
	rc, sink, _ := newTestReceiver(t, func(o *Options) {
		o.Now = func() time.Time { return time.Unix(1750000000, 0).UTC() }
	})

	body := minimalLogsJSON()
	if rec := do(rc, authedRequest(http.MethodPost, "/v1/logs", "application/json", body)); rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if err := rc.Close(); err != nil {
		t.Fatal(err)
	}

	got := sink.snapshot()
	if len(got) != 1 {
		t.Fatalf("배치 수 = %d, want 1", len(got))
	}
	b := got[0]
	if b.Kind != otlpdecode.PayloadLogs {
		t.Errorf("Kind = %v, want logs", b.Kind)
	}
	if b.Encoding != otlpdecode.EncodingJSON {
		t.Errorf("Encoding = %v, want json", b.Encoding)
	}
	if !bytes.Equal(b.Body, body) {
		t.Error("Body 가 원본 바이트와 다르다 — forward 가 재인코딩할 입력을 잃는다")
	}
	if b.ReceivedAt.Unix() != 1750000000 {
		t.Errorf("ReceivedAt = %v", b.ReceivedAt)
	}
	if len(b.Result.Events) != 1 {
		t.Fatalf("정규화된 이벤트 수 = %d, want 1", len(b.Result.Events))
	}
	if b.Result.Events[0].SessionID != "sess-1" {
		t.Errorf("session_id = %q", b.Result.Events[0].SessionID)
	}
}

// /v1/traces 는 정규화되지 않지만 200 을 받고 Body 는 전달용으로 넘어가야 한다.
func TestTracesPassThrough(t *testing.T) {
	rc, sink, _ := newTestReceiver(t, nil)

	rec := do(rc, authedRequest(http.MethodPost, "/v1/traces", "application/json", []byte(`{"resourceSpans":[]}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if err := rc.Close(); err != nil {
		t.Fatal(err)
	}
	got := sink.snapshot()
	if len(got) != 1 || got[0].Kind != otlpdecode.PayloadTraces {
		t.Fatalf("traces 배치가 sink 에 도달하지 않았다: %+v", got)
	}
	if len(got[0].Result.Events) != 0 {
		t.Error("traces 는 events 로 정규화되지 않아야 한다")
	}
}

// Sink 가 오류를 내도 이미 200 을 응답한 뒤라 클라이언트에 영향이 없어야 하고,
// 카운터에만 남아야 한다.
func TestSinkErrorIsCountedNotPropagated(t *testing.T) {
	rc, _, logs := newTestReceiver(t, func(o *Options) {
		o.Sink = &collector{err: errBadBody}
	})
	rec := do(rc, authedRequest(http.MethodPost, "/v1/logs", "application/json", minimalLogsJSON()))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if err := rc.Close(); err != nil {
		t.Fatal(err)
	}
	if got := rc.Stats().SinkErrors; got != 1 {
		t.Errorf("sink_errors = %d, want 1", got)
	}
	if !strings.Contains(logs.String(), "수신 배치 처리 실패") {
		t.Errorf("실패가 로그에 남지 않았다: %q", logs.String())
	}
}

// New 는 인증 없는 수신기나 InstallationID 없는 수신기를 만들지 못하게 한다.
func TestNewRejectsIncompleteOptions(t *testing.T) {
	base := func() Options {
		return Options{
			Token:  testToken,
			Sink:   &collector{},
			Decode: otlpdecode.Options{InstallationID: "inst_test"},
		}
	}
	tests := []struct {
		name   string
		mutate func(*Options)
	}{
		{name: "토큰 없음", mutate: func(o *Options) { o.Token = "" }},
		{name: "Sink 없음", mutate: func(o *Options) { o.Sink = nil }},
		{name: "InstallationID 없음", mutate: func(o *Options) { o.Decode.InstallationID = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opt := base()
			tt.mutate(&opt)
			rc, err := New(opt)
			if err == nil {
				_ = rc.Close()
				t.Fatal("불완전한 옵션으로 수신기가 만들어졌다")
			}
		})
	}
}

// Close 는 여러 번 불려도 안전해야 한다 (Shutdown 경로와 defer 가 겹칠 수 있다).
func TestCloseIsIdempotent(t *testing.T) {
	rc, _, _ := newTestReceiver(t, nil)
	if err := rc.Close(); err != nil {
		t.Fatal(err)
	}
	if err := rc.Close(); err != nil {
		t.Fatal(err)
	}
	// 닫힌 뒤 들어온 요청은 드롭되지만 panic 하지 않고 200 으로 답해야 한다.
	rec := do(rc, authedRequest(http.MethodPost, "/v1/logs", "application/json", minimalLogsJSON()))
	if rec.Code != http.StatusOK {
		t.Fatalf("종료 후 status = %d, want 200", rec.Code)
	}
}

func gzipBytes(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
