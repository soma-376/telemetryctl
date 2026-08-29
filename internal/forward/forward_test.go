package forward

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	logscolpb "go.opentelemetry.io/proto/otlp/collector/logs/v1"

	"github.com/your-org/pulsemetry/internal/contract"
	"github.com/your-org/pulsemetry/internal/otlpdecode"
)

// --- 구성 검증 -------------------------------------------------------------

func TestNewRejectsProtocols(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		protocol string
		wantErr  error
		wantOK   bool
	}{
		{name: "http/protobuf 은 지원", protocol: "http/protobuf", wantOK: true},
		{name: "http/json 은 지원", protocol: "http/json", wantOK: true},
		{name: "grpc 는 명확한 에러로 거부", protocol: "grpc", wantErr: ErrGRPCUnsupported},
		{name: "빈 프로토콜 거부", protocol: ""},
		{name: "알 수 없는 프로토콜 거부", protocol: "http/thrift"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			manifest := testManifest("https://collector.example.com", blockAll())
			manifest.OTLP.Protocol = tc.protocol
			fwd, err := New(Options{
				Manifest: manifest,
				Tokens:   StaticToken("t"),
				Logger:   log.New(&syncBuffer{}, "", 0),
			})
			if tc.wantOK {
				if err != nil {
					t.Fatalf("New: %v", err)
				}
				if fwd == nil {
					t.Fatal("포워더가 nil")
				}
				return
			}
			if err == nil {
				t.Fatal("거부되어야 하는데 성공했다")
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("errors.Is(%v, ErrGRPCUnsupported) = false", err)
			}
		})
	}
}

func TestNewRejectsBadEndpoint(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, endpoint string }{
		{"빈 endpoint", ""},
		{"스킴 없음", "collector.example.com"},
		{"http(s) 가 아님", "grpc://collector.example.com"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := New(Options{
				Manifest: testManifest(tc.endpoint, blockAll()),
				Tokens:   StaticToken("t"),
				Logger:   log.New(&syncBuffer{}, "", 0),
			})
			if err == nil {
				t.Fatal("잘못된 endpoint 가 통과했다")
			}
			if strings.Contains(err.Error(), tc.endpoint) && tc.endpoint != "" {
				t.Fatalf("오류 메시지에 endpoint 가 그대로 들어 있다: %v", err)
			}
		})
	}
}

func TestNewRequiresLoggerAndTokens(t *testing.T) {
	t.Parallel()
	manifest := testManifest("https://collector.example.com", blockAll())
	if _, err := New(Options{Manifest: manifest, Tokens: StaticToken("t")}); err == nil {
		t.Fatal("logger 없이 통과했다")
	}
	if _, err := New(Options{Manifest: manifest, Logger: log.New(&syncBuffer{}, "", 0)}); err == nil {
		t.Fatal("token 공급자 없이 통과했다")
	}
}

// --- 프라이버시 집행 (ADR 0003) --------------------------------------------

// 이 테스트가 ADR 0003 의 집행 증거다. 원문이 들어간 페이로드를 전달하면 상위가 받는
// 본문에 원문이 없어야 한다. protojson·protobuf 양쪽으로 돌린다.
func TestForwardScrubsContentBeforeUpstream(t *testing.T) {
	t.Parallel()
	encodings := []struct {
		name string
		enc  otlpdecode.Encoding
	}{
		{"protobuf", otlpdecode.EncodingProtobuf},
		{"protojson", otlpdecode.EncodingJSON},
	}
	for _, e := range encodings {
		t.Run(e.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, harnessOpts{privacy: blockAll(), start: true})

			in := encodePayload(t, logsFixture(), e.enc)
			for _, sentinel := range []string{secretPrompt, secretPromptAt, secretPath, secretEmail} {
				if !bytes.Contains(in, []byte(sentinel)) {
					t.Fatalf("픽스처에 표식 %q 가 없다 — 테스트가 아무것도 증명하지 못한다", sentinel)
				}
			}

			if !h.fwd.Enqueue(otlpdecode.PayloadLogs, e.enc, in) {
				t.Fatal("Enqueue 가 거부됐다")
			}
			if err := h.shutdown(t, 5*time.Second); err != nil {
				t.Fatalf("Shutdown: %v", err)
			}

			reqs := h.up.requests()
			if len(reqs) != 1 {
				t.Fatalf("상위 요청 수 = %d, want 1", len(reqs))
			}
			out := reqs[0].body
			for _, sentinel := range []string{secretPrompt, secretPromptAt, secretPath, secretEmail} {
				if bytes.Contains(out, []byte(sentinel)) {
					t.Errorf("상위로 나간 본문에 %q 가 남아 있다 (ADR 0003 위반)", sentinel)
				}
			}
			if !bytes.Contains(out, []byte(keptModel)) {
				t.Error("금지되지 않은 속성(model)까지 사라졌다 — denylist 가 allowlist 처럼 동작한다")
			}

			stats := h.fwd.Stats()
			if stats.Sent != 1 {
				t.Errorf("Sent = %d, want 1", stats.Sent)
			}
			if stats.AttributesRemoved == 0 || stats.BodiesCleared == 0 {
				t.Errorf("제거 통계가 비었다: %+v", stats)
			}
		})
	}
}

// manifest 가 항목을 허용으로 바꾸면 그 항목은 자동으로 통과해야 한다.
// 포워더에 제거 규칙이 하드코딩돼 있지 않다는 증거다.
func TestManifestPrivacyAllowsPassthrough(t *testing.T) {
	t.Parallel()
	privacy := contract.Privacy{CollectUserPrompts: true, CollectToolDetails: true}
	h := newHarness(t, harnessOpts{privacy: privacy, start: true})

	in := encodePayload(t, logsFixture(), otlpdecode.EncodingProtobuf)
	if !h.fwd.Enqueue(otlpdecode.PayloadLogs, otlpdecode.EncodingProtobuf, in) {
		t.Fatal("Enqueue 가 거부됐다")
	}
	if err := h.shutdown(t, 5*time.Second); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	out := h.up.requests()[0].body
	for _, allowed := range []string{secretPrompt, secretPromptAt, secretPath} {
		if !bytes.Contains(out, []byte(allowed)) {
			t.Errorf("manifest 가 허용한 %q 가 지워졌다", allowed)
		}
	}
	// 허용하지 않은 항목은 그대로 막혀 있어야 한다.
	if bytes.Contains(out, []byte(secretEmail)) {
		t.Error("collect_user_email=false 인데 이메일이 나갔다")
	}
}

// Scrub 이 실패한 페이로드는 절대 상위로 나가면 안 된다.
func TestScrubFailureDropsPayload(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOpts{privacy: blockAll(), start: true})

	garbage := []byte("이건 OTLP 페이로드가 아니다")
	if !h.fwd.Enqueue(otlpdecode.PayloadLogs, otlpdecode.EncodingProtobuf, garbage) {
		t.Fatal("Enqueue 가 거부됐다")
	}
	if err := h.shutdown(t, 5*time.Second); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	if n := h.up.count(); n != 0 {
		t.Fatalf("정리에 실패한 페이로드가 %d 건 상위로 나갔다", n)
	}
	if got := h.fwd.Stats().DroppedScrub; got != 1 {
		t.Fatalf("DroppedScrub = %d, want 1", got)
	}
}

// --- 인코딩·경로 보존 -------------------------------------------------------

func TestEncodingAndPathPreserved(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		kind        otlpdecode.PayloadKind
		msg         proto.Message
		enc         otlpdecode.Encoding
		wantPath    string
		wantCType   string
		checkParses func(t *testing.T, body []byte)
	}{
		{
			name: "logs protobuf", kind: otlpdecode.PayloadLogs, msg: logsFixture(),
			enc: otlpdecode.EncodingProtobuf, wantPath: "/v1/logs", wantCType: "application/x-protobuf",
			checkParses: func(t *testing.T, body []byte) {
				var req logscolpb.ExportLogsServiceRequest
				if err := proto.Unmarshal(body, &req); err != nil {
					t.Fatalf("상위가 받은 본문이 protobuf 가 아니다: %v", err)
				}
				if len(req.GetResourceLogs()) == 0 {
					t.Fatal("재인코딩 결과가 비었다")
				}
			},
		},
		{
			name: "logs protojson", kind: otlpdecode.PayloadLogs, msg: logsFixture(),
			enc: otlpdecode.EncodingJSON, wantPath: "/v1/logs", wantCType: "application/json",
			checkParses: func(t *testing.T, body []byte) {
				var req logscolpb.ExportLogsServiceRequest
				if err := protojson.Unmarshal(body, &req); err != nil {
					t.Fatalf("상위가 받은 본문이 protojson 이 아니다: %v", err)
				}
			},
		},
		{
			name: "metrics protobuf", kind: otlpdecode.PayloadMetrics, msg: metricsFixture(),
			enc: otlpdecode.EncodingProtobuf, wantPath: "/v1/metrics", wantCType: "application/x-protobuf",
		},
		{
			name: "traces protojson", kind: otlpdecode.PayloadTraces, msg: tracesFixture(),
			enc: otlpdecode.EncodingJSON, wantPath: "/v1/traces", wantCType: "application/json",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, harnessOpts{privacy: blockAll(), start: true})
			in := encodePayload(t, tc.msg, tc.enc)
			if !h.fwd.Enqueue(tc.kind, tc.enc, in) {
				t.Fatal("Enqueue 가 거부됐다")
			}
			if err := h.shutdown(t, 5*time.Second); err != nil {
				t.Fatalf("Shutdown: %v", err)
			}

			reqs := h.up.requests()
			if len(reqs) != 1 {
				t.Fatalf("상위 요청 수 = %d, want 1", len(reqs))
			}
			if reqs[0].path != tc.wantPath {
				t.Errorf("path = %q, want %q", reqs[0].path, tc.wantPath)
			}
			if reqs[0].contentType != tc.wantCType {
				t.Errorf("Content-Type = %q, want %q", reqs[0].contentType, tc.wantCType)
			}
			if tc.checkParses != nil {
				tc.checkParses(t, reqs[0].body)
			}
		})
	}
}

// endpoint 뒤에 슬래시가 붙어 있어도 //v1/logs 가 되면 안 된다.
func TestEndpointTrailingSlash(t *testing.T) {
	t.Parallel()
	up := newUpstream(t, nil)
	fwd, err := New(Options{
		Manifest: testManifest(up.srv.URL+"/", blockAll()),
		Tokens:   StaticToken("t"),
		Logger:   log.New(&syncBuffer{}, "", 0),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	fwd.Start()
	fwd.Enqueue(otlpdecode.PayloadLogs, otlpdecode.EncodingProtobuf, encodePayload(t, logsFixture(), otlpdecode.EncodingProtobuf))
	if err := fwd.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if got := up.requests()[0].path; got != "/v1/logs" {
		t.Fatalf("path = %q, want /v1/logs", got)
	}
}

// --- 재시도 정책 -------------------------------------------------------------

func TestRetryPolicy(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		status       func(n int) int
		maxAttempts  int
		wantRequests int
		wantStats    func(s Stats) bool
		statsDesc    string
	}{
		{
			name:         "5xx 는 예산까지 재시도",
			status:       func(int) int { return http.StatusInternalServerError },
			maxAttempts:  3,
			wantRequests: 3,
			wantStats:    func(s Stats) bool { return s.Failed == 1 && s.Sent == 0 && s.Retries == 2 },
			statsDesc:    "Failed=1 Sent=0 Retries=2",
		},
		{
			name: "5xx 뒤 성공하면 거기서 멈춘다",
			status: func(n int) int {
				if n == 1 {
					return http.StatusServiceUnavailable
				}
				return http.StatusOK
			},
			maxAttempts:  3,
			wantRequests: 2,
			wantStats:    func(s Stats) bool { return s.Sent == 1 && s.Failed == 0 },
			statsDesc:    "Sent=1 Failed=0",
		},
		{
			name:         "400 은 즉시 폐기",
			status:       func(int) int { return http.StatusBadRequest },
			maxAttempts:  3,
			wantRequests: 1,
			wantStats:    func(s Stats) bool { return s.Discarded == 1 && s.Failed == 0 && s.Retries == 0 },
			statsDesc:    "Discarded=1 Failed=0 Retries=0",
		},
		{
			name:         "404 도 즉시 폐기",
			status:       func(int) int { return http.StatusNotFound },
			maxAttempts:  3,
			wantRequests: 1,
			wantStats:    func(s Stats) bool { return s.Discarded == 1 },
			statsDesc:    "Discarded=1",
		},
		{
			name:         "413 도 즉시 폐기 — 같은 본문은 계속 크다",
			status:       func(int) int { return http.StatusRequestEntityTooLarge },
			maxAttempts:  3,
			wantRequests: 1,
			wantStats:    func(s Stats) bool { return s.Discarded == 1 },
			statsDesc:    "Discarded=1",
		},
		{
			name:         "429 는 재시도한다",
			status:       func(int) int { return http.StatusTooManyRequests },
			maxAttempts:  2,
			wantRequests: 2,
			wantStats:    func(s Stats) bool { return s.Failed == 1 && s.Discarded == 0 },
			statsDesc:    "Failed=1 Discarded=0",
		},
		{
			name:         "maxAttempts=1 이면 재시도하지 않는다",
			status:       func(int) int { return http.StatusInternalServerError },
			maxAttempts:  1,
			wantRequests: 1,
			wantStats:    func(s Stats) bool { return s.Failed == 1 && s.Retries == 0 },
			statsDesc:    "Failed=1 Retries=0",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, harnessOpts{
				privacy:     blockAll(),
				status:      tc.status,
				maxAttempts: tc.maxAttempts,
				start:       true,
			})
			in := encodePayload(t, logsFixture(), otlpdecode.EncodingProtobuf)
			if !h.fwd.Enqueue(otlpdecode.PayloadLogs, otlpdecode.EncodingProtobuf, in) {
				t.Fatal("Enqueue 가 거부됐다")
			}
			if err := h.shutdown(t, 5*time.Second); err != nil {
				t.Fatalf("Shutdown: %v", err)
			}
			if got := h.up.count(); got != tc.wantRequests {
				t.Errorf("상위 요청 수 = %d, want %d", got, tc.wantRequests)
			}
			if stats := h.fwd.Stats(); !tc.wantStats(stats) {
				t.Errorf("stats = %+v, want %s", stats, tc.statsDesc)
			}
		})
	}
}

// 전송 오류(연결 거부)도 재시도 대상이다.
func TestTransportErrorRetries(t *testing.T) {
	t.Parallel()
	// 서버를 띄웠다가 바로 닫아 확실히 죽은 주소를 만든다.
	up := newUpstream(t, nil)
	dead := up.srv.URL
	up.srv.Close()

	buf := &syncBuffer{}
	fwd, err := New(Options{
		Manifest:    testManifest(dead, blockAll()),
		Tokens:      StaticToken("t"),
		Logger:      log.New(buf, "", 0),
		MaxAttempts: 2,
		BaseBackoff: time.Millisecond,
		MaxBackoff:  2 * time.Millisecond,
		Timeout:     time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	fwd.Start()
	fwd.Enqueue(otlpdecode.PayloadLogs, otlpdecode.EncodingProtobuf, encodePayload(t, logsFixture(), otlpdecode.EncodingProtobuf))
	if err := fwd.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	stats := fwd.Stats()
	if stats.Failed != 1 || stats.Retries != 1 {
		t.Fatalf("stats = %+v, want Failed=1 Retries=1", stats)
	}
}

// 401 은 토큰 만료로 보고 갱신 후 한 번만 다시 시도한다.
func TestAuthRefreshOn401(t *testing.T) {
	t.Parallel()
	tokens := &fakeTokens{tokens: []string{"stale-token", "fresh-token"}}
	h := newHarness(t, harnessOpts{
		privacy: blockAll(),
		tokens:  tokens,
		status: func(n int) int {
			if n == 1 {
				return http.StatusUnauthorized
			}
			return http.StatusOK
		},
		maxAttempts: 3,
		start:       true,
	})
	in := encodePayload(t, logsFixture(), otlpdecode.EncodingProtobuf)
	h.fwd.Enqueue(otlpdecode.PayloadLogs, otlpdecode.EncodingProtobuf, in)
	if err := h.shutdown(t, 5*time.Second); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	reqs := h.up.requests()
	if len(reqs) != 2 {
		t.Fatalf("상위 요청 수 = %d, want 2", len(reqs))
	}
	if reqs[0].auth != "Bearer stale-token" || reqs[1].auth != "Bearer fresh-token" {
		t.Fatalf("갱신된 토큰으로 재시도하지 않았다: %q → %q", reqs[0].auth, reqs[1].auth)
	}
	if inv := tokens.invalidations(); len(inv) != 1 || inv[0] != "stale-token" {
		t.Fatalf("Invalidate 호출 = %v, want [stale-token]", inv)
	}
	if got := h.fwd.Stats().Sent; got != 1 {
		t.Fatalf("Sent = %d, want 1", got)
	}
}

// 갱신 후에도 401 이면 폐기한다 — 무한 갱신 루프를 만들지 않는다.
func TestAuthGivesUpAfterOneRefresh(t *testing.T) {
	t.Parallel()
	tokens := &fakeTokens{tokens: []string{"t1", "t2", "t3"}}
	h := newHarness(t, harnessOpts{
		privacy:     blockAll(),
		tokens:      tokens,
		status:      func(int) int { return http.StatusUnauthorized },
		maxAttempts: 5,
		start:       true,
	})
	h.fwd.Enqueue(otlpdecode.PayloadLogs, otlpdecode.EncodingProtobuf, encodePayload(t, logsFixture(), otlpdecode.EncodingProtobuf))
	if err := h.shutdown(t, 5*time.Second); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if got := h.up.count(); got != 2 {
		t.Fatalf("상위 요청 수 = %d, want 2 (최초 + 갱신 후 1회)", got)
	}
	if got := h.fwd.Stats().Discarded; got != 1 {
		t.Fatalf("Discarded = %d, want 1", got)
	}
}

// 토큰을 못 얻으면 전송 자체를 하지 않는다.
func TestTokenErrorSkipsUpstream(t *testing.T) {
	t.Parallel()
	tokens := &fakeTokens{tokens: []string{""}, err: errors.New("키링 잠김")}
	h := newHarness(t, harnessOpts{privacy: blockAll(), tokens: tokens, maxAttempts: 2, start: true})
	h.fwd.Enqueue(otlpdecode.PayloadLogs, otlpdecode.EncodingProtobuf, encodePayload(t, logsFixture(), otlpdecode.EncodingProtobuf))
	if err := h.shutdown(t, 5*time.Second); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if got := h.up.count(); got != 0 {
		t.Fatalf("토큰 없이 %d 건을 보냈다", got)
	}
	stats := h.fwd.Stats()
	if stats.TokenErrors != 2 || stats.Failed != 1 {
		t.Fatalf("stats = %+v, want TokenErrors=2 Failed=1", stats)
	}
}

// 상위가 Retry-After 로 지시하면 그 값을 따르되 MaxBackoff 로 자른다.
func TestRetryAfterHonoredButClamped(t *testing.T) {
	t.Parallel()
	up := newUpstream(t, func(int) int { return http.StatusTooManyRequests })
	up.setHeader("Retry-After", "3600") // 한 시간. 그대로 따르면 워커가 잠긴다.

	fwd, err := New(Options{
		Manifest:    testManifest(up.srv.URL, blockAll()),
		Tokens:      StaticToken("t"),
		Logger:      log.New(&syncBuffer{}, "", 0),
		MaxAttempts: 2,
		BaseBackoff: time.Millisecond,
		MaxBackoff:  20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	fwd.Start()
	fwd.Enqueue(otlpdecode.PayloadLogs, otlpdecode.EncodingProtobuf, encodePayload(t, logsFixture(), otlpdecode.EncodingProtobuf))

	start := time.Now()
	if err := fwd.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Retry-After 가 잘리지 않아 %s 걸렸다", elapsed)
	}
	if got := up.count(); got != 2 {
		t.Fatalf("상위 요청 수 = %d, want 2", got)
	}
}

// --- 큐 포화 (§5.4) ---------------------------------------------------------

// 워커를 띄우지 않아 큐가 절대 비지 않는 상태를 만든다. 여기서 Enqueue 가 블로킹하면
// 감시자가 잡는다 — 블로킹은 곧 벤더 exporter 지연이다.
func TestEnqueueNonBlockingWhenQueueFull(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOpts{privacy: blockAll(), queueSize: 2, start: false})
	in := encodePayload(t, logsFixture(), otlpdecode.EncodingProtobuf)

	results := make(chan []bool, 1)
	go func() {
		var got []bool
		for i := 0; i < 5; i++ {
			got = append(got, h.fwd.Enqueue(otlpdecode.PayloadLogs, otlpdecode.EncodingProtobuf, in))
		}
		results <- got
	}()

	select {
	case got := <-results:
		want := []bool{true, true, false, false, false}
		if len(got) != len(want) {
			t.Fatalf("결과 수 = %d", len(got))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("Enqueue 결과 = %v, want %v", got, want)
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Enqueue 가 블로킹했다 — §5.4 위반")
	}

	if got := h.fwd.Stats().DroppedQueueFull; got != 3 {
		t.Fatalf("DroppedQueueFull = %d, want 3", got)
	}
	if got := h.fwd.Stats().Enqueued; got != 2 {
		t.Fatalf("Enqueued = %d, want 2", got)
	}
	_ = h.shutdown(t, time.Second)
}

// 워커가 상위 응답에 붙잡혀 있어도 Enqueue 는 즉시 돌아온다.
func TestEnqueueNonBlockingWhileWorkerStuck(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOpts{privacy: blockAll(), queueSize: 1, maxAttempts: 1, start: true})
	h.up.block()

	in := encodePayload(t, logsFixture(), otlpdecode.EncodingProtobuf)
	done := make(chan bool, 1)
	go func() {
		dropped := false
		for i := 0; i < 50; i++ {
			if !h.fwd.Enqueue(otlpdecode.PayloadLogs, otlpdecode.EncodingProtobuf, in) {
				dropped = true
			}
		}
		done <- dropped
	}()

	select {
	case dropped := <-done:
		if !dropped {
			t.Fatal("큐가 1 인데 50 건이 전부 들어갔다 — 큐가 유계가 아니다")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("워커가 막힌 동안 Enqueue 가 블로킹했다 — §5.4 위반")
	}
	h.up.release()
	_ = h.shutdown(t, 2*time.Second)
}

// 바이트 상한은 항목 수와 별개로 큐를 묶는다.
func TestQueueByteLimit(t *testing.T) {
	t.Parallel()
	up := newUpstream(t, nil)
	fwd, err := New(Options{
		Manifest:      testManifest(up.srv.URL, blockAll()),
		Tokens:        StaticToken("t"),
		Logger:        log.New(&syncBuffer{}, "", 0),
		QueueSize:     100,
		MaxQueueBytes: 64,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	big := bytes.Repeat([]byte("x"), 100)
	if fwd.Enqueue(otlpdecode.PayloadLogs, otlpdecode.EncodingProtobuf, big) {
		t.Fatal("바이트 상한을 넘는 페이로드가 큐에 들어갔다")
	}
	if got := fwd.Stats().DroppedQueueFull; got != 1 {
		t.Fatalf("DroppedQueueFull = %d, want 1", got)
	}
}

func TestEnqueueRejectsEmptyBody(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOpts{privacy: blockAll()})
	if h.fwd.Enqueue(otlpdecode.PayloadLogs, otlpdecode.EncodingProtobuf, nil) {
		t.Fatal("빈 본문이 큐에 들어갔다")
	}
}

// 회사 manifest 가 끈 시그널은 큐에 들어가지도 않는다 (PROJ-45).
//
// 로컬 배선은 시그널 셋을 전부 켜 놓고 받으므로(installer.localProfile), 회사가 받기로 하지
// 않은 시그널을 여기서 멈추지 않으면 재배선만으로 상위로 나가는 데이터가 늘어난다 —
// installer/local.go 의 불변식 1 위반이다. 이 테스트가 그 불변식의 forward 쪽 절반이다.
func TestEnqueue는꺼진시그널을버린다(t *testing.T) {
	t.Parallel()

	kinds := []struct {
		name string
		kind otlpdecode.PayloadKind
	}{
		{"metrics", otlpdecode.PayloadMetrics},
		{"logs", otlpdecode.PayloadLogs},
		{"traces", otlpdecode.PayloadTraces},
	}

	tests := []struct {
		name    string
		signals contract.Signals
	}{
		{"traces 만 끔", contract.Signals{Logs: true, Metrics: true}},
		{"traces 만 켬", contract.Signals{Traces: true}},
		{"전부 끔", contract.Signals{}},
		{"전부 켬", contract.Signals{Logs: true, Metrics: true, Traces: true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			up := newUpstream(t, nil)
			fwd, err := New(Options{
				Manifest: testManifestSignals(up.srv.URL, blockAll(), tt.signals),
				Tokens:   StaticToken("test-token"),
				Logger:   log.New(&syncBuffer{}, "", 0),
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			enabled := map[otlpdecode.PayloadKind]bool{
				otlpdecode.PayloadMetrics: tt.signals.Metrics,
				otlpdecode.PayloadLogs:    tt.signals.Logs,
				otlpdecode.PayloadTraces:  tt.signals.Traces,
			}

			var wantDropped, wantQueued int64
			for _, k := range kinds {
				body := []byte("payload-" + k.name)
				if got := fwd.Enqueue(k.kind, otlpdecode.EncodingProtobuf, body); got != enabled[k.kind] {
					t.Errorf("Enqueue(%s) = %v, want %v (signals=%+v)", k.name, got, enabled[k.kind], tt.signals)
				}
				if enabled[k.kind] {
					wantQueued++
				} else {
					wantDropped++
				}
			}

			stats := fwd.Stats()
			if stats.DroppedSignalDisabled != wantDropped {
				t.Errorf("DroppedSignalDisabled = %d, want %d", stats.DroppedSignalDisabled, wantDropped)
			}
			if stats.Enqueued != wantQueued {
				t.Errorf("Enqueued = %d, want %d", stats.Enqueued, wantQueued)
			}
			// 꺼진 시그널은 큐 예산도 먹지 않아야 한다. 그것이 deliver 가 아니라 Enqueue 에서
			// 막는 이유다 — 상위가 받지도 않을 페이로드가 실제로 보낼 배치를 밀어내면 안 된다.
			if stats.DroppedQueueFull != 0 {
				t.Errorf("DroppedQueueFull = %d, want 0", stats.DroppedQueueFull)
			}
		})
	}
}

// 꺼진 시그널은 상위로 요청 자체가 나가지 않는다. Enqueue 반환값만 보면 워커가 큐를 비우며
// 뒤늦게 보내는 경우를 놓친다.
func TestForward는꺼진시그널을상위로보내지않는다(t *testing.T) {
	t.Parallel()
	up := newUpstream(t, nil)
	fwd, err := New(Options{
		Manifest: testManifestSignals(up.srv.URL, blockAll(),
			contract.Signals{Logs: true, Metrics: false, Traces: false}),
		Tokens:      StaticToken("test-token"),
		Logger:      log.New(&syncBuffer{}, "", 0),
		BaseBackoff: time.Millisecond,
		MaxBackoff:  2 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	fwd.Start()

	fwd.Enqueue(otlpdecode.PayloadLogs, otlpdecode.EncodingProtobuf,
		encodePayload(t, logsFixture(), otlpdecode.EncodingProtobuf))
	fwd.Enqueue(otlpdecode.PayloadMetrics, otlpdecode.EncodingProtobuf,
		encodePayload(t, metricsFixture(), otlpdecode.EncodingProtobuf))
	fwd.Enqueue(otlpdecode.PayloadTraces, otlpdecode.EncodingProtobuf,
		encodePayload(t, tracesFixture(), otlpdecode.EncodingProtobuf))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := fwd.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	reqs := up.requests()
	if len(reqs) != 1 {
		t.Fatalf("상위가 받은 요청 %d 건, want 1 (logs 만)", len(reqs))
	}
	if reqs[0].path != otlpdecode.PayloadLogs.Path() {
		t.Fatalf("상위가 받은 경로 = %q, want %q", reqs[0].path, otlpdecode.PayloadLogs.Path())
	}
}

// Enqueue 는 본문을 복사한다. 수신기가 버퍼를 재사용해도 큐에 든 페이로드가 망가지면 안 된다.
func TestEnqueueCopiesBody(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOpts{privacy: blockAll(), start: false})
	in := encodePayload(t, logsFixture(), otlpdecode.EncodingProtobuf)
	buf := append([]byte(nil), in...)
	if !h.fwd.Enqueue(otlpdecode.PayloadLogs, otlpdecode.EncodingProtobuf, buf) {
		t.Fatal("Enqueue 가 거부됐다")
	}
	for i := range buf {
		buf[i] = 0 // 호출부가 버퍼를 재사용한 상황
	}
	h.fwd.Start()
	if err := h.shutdown(t, 5*time.Second); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if got := h.fwd.Stats().Sent; got != 1 {
		t.Fatalf("Sent = %d, want 1 — 복사하지 않아 페이로드가 깨졌다", got)
	}
}

// --- 종료 -------------------------------------------------------------------

// 정상 종료는 큐를 비우고 끝난다.
func TestShutdownDrainsQueue(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOpts{privacy: blockAll(), queueSize: 32, start: true})
	in := encodePayload(t, logsFixture(), otlpdecode.EncodingProtobuf)
	for i := 0; i < 10; i++ {
		if !h.fwd.Enqueue(otlpdecode.PayloadLogs, otlpdecode.EncodingProtobuf, in) {
			t.Fatalf("%d 번째 Enqueue 가 거부됐다", i)
		}
	}
	if err := h.shutdown(t, 10*time.Second); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if got := h.up.count(); got != 10 {
		t.Fatalf("상위가 받은 요청 수 = %d, want 10", got)
	}
	if got := h.fwd.Stats().Sent; got != 10 {
		t.Fatalf("Sent = %d, want 10", got)
	}
}

// 상위가 응답하지 않아도 종료는 제한 시간 안에 끝난다. 데몬 종료가 늘어지면 안 된다.
// shutdownQueueSize 는 아래 테스트의 큐 크기다. 워커가 동시에 집어갈 수 있는 수보다
// 충분히 커야 종료 시점에 큐에 남은 항목이 보장된다.
const shutdownQueueSize = 32

func TestShutdownBoundedWhenUpstreamHangs(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOpts{privacy: blockAll(), queueSize: shutdownQueueSize, maxAttempts: 1, start: true})
	h.up.block()

	in := encodePayload(t, logsFixture(), otlpdecode.EncodingProtobuf)
	// 큐를 가득 채운다. 5건만 넣으면 워커가 그것을 전부 집어가 채널이 비고, 그러면
	// discardQueued 가 0 을 세어 DroppedShutdown 단언이 간헐 실패한다 — 워커는 막힌
	// 상위에 물려 있을 뿐 큐에는 아무것도 남지 않기 때문이다. 큐 크기만큼 넣으면
	// 워커가 몇 개를 집어가든 남는 것이 보장된다.
	for i := 0; i < shutdownQueueSize; i++ {
		h.fwd.Enqueue(otlpdecode.PayloadLogs, otlpdecode.EncodingProtobuf, in)
	}
	waitFor(t, "워커가 첫 요청을 상위로 보냄", func() bool { return h.up.count() > 0 })

	start := time.Now()
	err := h.shutdown(t, 100*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("제한 시간을 넘겼는데 nil 을 반환했다")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded 를 감싼 오류", err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("종료에 %s 걸렸다 — 제한 시간이 지켜지지 않는다", elapsed)
	}
	if got := h.fwd.Stats().DroppedShutdown; got == 0 {
		t.Fatal("버린 항목이 집계되지 않았다")
	}
}

// 종료 뒤 Enqueue 는 조용히 거부한다. 닫힌 채널에 보내 패닉이 나면 안 된다.
func TestEnqueueAfterShutdown(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOpts{privacy: blockAll(), start: true})
	if err := h.shutdown(t, 5*time.Second); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	in := encodePayload(t, logsFixture(), otlpdecode.EncodingProtobuf)
	if h.fwd.Enqueue(otlpdecode.PayloadLogs, otlpdecode.EncodingProtobuf, in) {
		t.Fatal("종료 후 Enqueue 가 성공했다")
	}
	// 두 번째 Shutdown 도 안전해야 한다.
	if err := h.fwd.Shutdown(context.Background()); err != nil {
		t.Fatalf("두 번째 Shutdown: %v", err)
	}
}

// Start 를 부르지 않아도 Shutdown 은 걸리지 않는다.
func TestShutdownWithoutStart(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOpts{privacy: blockAll(), start: false})
	if err := h.shutdown(t, 2*time.Second); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

// 동시 Enqueue 는 -race 로 검증한다.
func TestConcurrentEnqueue(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOpts{privacy: blockAll(), queueSize: 8, start: true})
	in := encodePayload(t, logsFixture(), otlpdecode.EncodingProtobuf)

	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 20; j++ {
				h.fwd.Enqueue(otlpdecode.PayloadLogs, otlpdecode.EncodingProtobuf, in)
			}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
	if err := h.shutdown(t, 10*time.Second); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	stats := h.fwd.Stats()
	if stats.Enqueued+stats.DroppedQueueFull != 160 {
		t.Fatalf("Enqueued(%d) + DroppedQueueFull(%d) != 160", stats.Enqueued, stats.DroppedQueueFull)
	}
}
