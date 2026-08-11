// Package forward 는 로컬 수신기가 받은 OTLP 페이로드를 회사 Collector 로 전달한다.
//
// # 왜 원본 바이트를 그대로 넘기지 않는가
//
// 로컬 파이프라인은 OTEL_LOG_USER_PROMPTS·OTEL_LOG_TOOL_DETAILS 를 강제로 켠다 (ADR 0003).
// 그 순간 회사가 수집하지 않기로 한 데이터가 로컬을 통과해 상위로 흘러갈 수 있게 된다.
// 그래서 보내기 직전에 회사 manifest 의 Privacy 기준으로 otlpdecode.Scrub 을 태우고
// **받은 인코딩 그대로** 재인코딩한다. 결과적으로 이 패키지가 프라이버시 집행 지점이다.
//
// 무엇을 지울지는 전부 otlpdecode.PolicyFromPrivacy 에서 온다. 이 패키지에는 제거 규칙이
// 하나도 하드코딩돼 있지 않다 — manifest 가 항목을 허용으로 바꾸면 그 항목은 자동으로 통과한다.
//
// # 실패는 텔레메트리 손실로 끝나야 한다
//
// 회사 Collector 장애가 개발 도구를 느리게 만들면 안 된다 (docs/installation-architecture.md §5.4).
// 그래서 전송은 비동기이고, 큐가 가득 차면 블로킹 대신 버리고, 재시도는 횟수와 시간이 모두 유계이며,
// 종료도 제한 시간 안에 끝난다. 수신기는 전달 결과를 절대 기다리지 않는다.
//
// # 토큰은 로그에 남지 않는다
//
// telemetry token 은 메모리에만 있고 (TokenSource), 어떤 로그·오류 메시지에도 들어가지 않는다.
// net/http 가 실패를 *url.Error 로 감싸며 요청 URL 을 통째로 담는 것까지 고려해 sanitizeErr 로
// URL 을 떼어낸다 — endpoint 에 userinfo 가 있으면 그것이 곧 유출이다.
package forward

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/your-org/pulsemetry/internal/contract"
	"github.com/your-org/pulsemetry/internal/otlpdecode"
)

// 기본값. 전부 §5.4 의 "짧은 timeout · 제한된 retry · 유계 큐"를 그대로 옮긴 것이다.
const (
	// DefaultQueueSize 는 전송 대기 큐의 항목 수 상한이다.
	DefaultQueueSize = 64
	// DefaultMaxQueueBytes 는 큐가 붙들 수 있는 본문 바이트 총량이다. 항목 수만으로는
	// 부족하다 — 수신기가 페이로드당 4 MiB 까지 받으므로 64 개면 256 MiB 가 된다.
	DefaultMaxQueueBytes = 32 << 20
	// DefaultMaxAttempts 는 한 페이로드의 총 시도 횟수다 (최초 시도 포함).
	DefaultMaxAttempts = 3
	// DefaultBaseBackoff 는 첫 재시도 대기의 기준값이다.
	DefaultBaseBackoff = 500 * time.Millisecond
	// DefaultMaxBackoff 는 지수 백오프와 Retry-After 양쪽의 상한이다.
	DefaultMaxBackoff = 15 * time.Second
	// DefaultTimeout 은 한 번의 상위 요청에 허용하는 시간이다.
	DefaultTimeout = 10 * time.Second

	// maxDropLogGap 은 큐 포화 로그의 최소 간격이다. 포화 상태에서 매 요청마다 찍으면
	// 로그가 통째로 잠식된다.
	maxDropLogGap = time.Minute
	// maxUpstreamBodyRead 는 상위 응답 본문을 연결 재사용을 위해 읽어 버릴 상한이다.
	// 내용은 어디에도 쓰지 않는다.
	maxUpstreamBodyRead = 4 << 10
)

// ErrGRPCUnsupported 는 manifest 가 grpc 프로토콜일 때 New 가 돌려주는 오류다.
//
// gRPC 상위 전달은 이 티켓 범위 밖이다. local enable 이 미리 거부하지만 여기서도 막는다 —
// 나중에 manifest 가 grpc 로 바뀌었을 때 조용히 HTTP 로 보내 성공한 척하면 안 된다.
var ErrGRPCUnsupported = errors.New("forward: grpc 상위 전달은 아직 지원하지 않음 (local enable 이 거부해야 함)")

// Options 는 포워더 구성이다. 상위 endpoint·프로토콜·프라이버시 정책은 전부 Manifest 에서 온다.
type Options struct {
	// Manifest 는 **회사 manifest 원본**이다. local enable 이 만든 로컬 사본이 아니다 —
	// 제거 기준은 회사가 수집하기로 한 범위여야 한다 (ADR 0003).
	Manifest contract.Manifest
	// Tokens 는 Authorization 헤더에 쓸 telemetry token 공급자다. 필수.
	Tokens TokenSource
	// Logger 는 필수다. 여기에 토큰이 새지 않는 것이 이 패키지의 핵심 불변식이다.
	Logger *log.Logger

	// Client 는 상위 전송에 쓸 HTTP 클라이언트다. 비우면 Timeout 이 걸린 기본값을 만든다.
	Client *http.Client
	// Timeout 은 요청 한 번의 상한이다. 0 이면 manifest 의 otlp.timeout_ms, 그것도 0 이면 DefaultTimeout.
	Timeout time.Duration

	// QueueSize·MaxQueueBytes 는 큐의 두 축이다. 0 이면 기본값.
	QueueSize     int
	MaxQueueBytes int64

	// MaxAttempts·BaseBackoff·MaxBackoff 는 재시도 예산이다. 0 이면 기본값.
	MaxAttempts int
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
}

// Stats 는 전달 결과 집계다. 값은 담지 않는다 — 개수만이다.
type Stats struct {
	// Enqueued 는 큐에 들어간 페이로드 수다.
	Enqueued int64
	// Sent 는 상위가 2xx 로 받은 페이로드 수다.
	Sent int64
	// DroppedQueueFull 은 큐 포화(항목 수 또는 바이트)로 버린 수다.
	DroppedQueueFull int64
	// DroppedScrub 은 Scrub 실패로 버린 수다. **정리에 실패한 페이로드는 절대 보내지 않는다.**
	DroppedScrub int64
	// DroppedShutdown 은 종료 제한 시간 안에 비우지 못해 버린 수다.
	DroppedShutdown int64
	// Discarded 는 4xx 로 즉시 폐기한 수다 (재시도해도 결과가 같다).
	Discarded int64
	// Failed 는 재시도 예산을 다 쓰고도 실패한 수다.
	Failed int64
	// Retries 는 재시도 대기에 들어간 횟수다.
	Retries int64
	// TokenErrors 는 telemetry token 확보에 실패한 횟수다.
	TokenErrors int64
	// AttributesRemoved·BodiesCleared 는 Scrub 이 실제로 지운 양이다.
	// 정책이 동작 중인지 확인하는 유일한 관측 지점이다.
	AttributesRemoved int64
	BodiesCleared     int64
}

type job struct {
	kind otlpdecode.PayloadKind
	enc  otlpdecode.Encoding
	body []byte
}

// Forwarder 는 상위 Collector 전달기다. New → Start → Enqueue… → Shutdown 순으로 쓴다.
type Forwarder struct {
	endpoint string
	policy   otlpdecode.ScrubPolicy
	tokens   TokenSource
	logger   *log.Logger
	client   *http.Client
	timeout  time.Duration

	maxAttempts int
	baseBackoff time.Duration
	maxBackoff  time.Duration

	queue         chan job
	maxQueueBytes int64
	queuedBytes   atomic.Int64

	// closeMu 는 Enqueue 와 close(queue) 의 경합만 막는다. Enqueue 는 RLock 만 잡고
	// 채널 전송은 non-blocking 이므로 여기서 대기하는 일이 없다.
	closeMu sync.RWMutex
	closed  bool

	stopCtx    context.Context
	stopCancel context.CancelFunc
	wg         sync.WaitGroup
	startOnce  sync.Once
	closeOnce  sync.Once

	statsMu     sync.Mutex
	stats       Stats
	lastDropLog time.Time
}

// New 는 포워더를 만든다. manifest 가 grpc 면 ErrGRPCUnsupported 로 거부한다.
func New(opts Options) (*Forwarder, error) {
	if opts.Logger == nil {
		return nil, errors.New("forward: logger 는 필수")
	}
	if opts.Tokens == nil {
		return nil, errors.New("forward: token 공급자는 필수")
	}
	if err := checkProtocol(opts.Manifest.OTLP.Protocol); err != nil {
		return nil, err
	}
	endpoint, err := normalizeEndpoint(opts.Manifest.OTLP.Endpoint)
	if err != nil {
		return nil, err
	}

	timeout := opts.Timeout
	if timeout <= 0 && opts.Manifest.OTLP.TimeoutMS > 0 {
		timeout = time.Duration(opts.Manifest.OTLP.TimeoutMS) * time.Millisecond
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}

	f := &Forwarder{
		endpoint:      endpoint,
		policy:        otlpdecode.PolicyFromPrivacy(opts.Manifest.Privacy),
		tokens:        opts.Tokens,
		logger:        opts.Logger,
		client:        client,
		timeout:       timeout,
		maxAttempts:   orDefaultInt(opts.MaxAttempts, DefaultMaxAttempts),
		baseBackoff:   orDefaultDuration(opts.BaseBackoff, DefaultBaseBackoff),
		maxBackoff:    orDefaultDuration(opts.MaxBackoff, DefaultMaxBackoff),
		queue:         make(chan job, orDefaultInt(opts.QueueSize, DefaultQueueSize)),
		maxQueueBytes: orDefaultInt64(opts.MaxQueueBytes, DefaultMaxQueueBytes),
	}
	f.stopCtx, f.stopCancel = context.WithCancel(context.Background())
	return f, nil
}

// checkProtocol 은 manifest 프로토콜이 이 포워더가 다룰 수 있는 것인지 본다.
//
// 주의: 프로토콜은 **우리가 내보낼 인코딩을 정하지 않는다.** 나가는 인코딩은 받은 인코딩을
// 그대로 따른다 (Requirement A). 여기서 프로토콜을 보는 이유는 grpc 를 거부하기 위해서다.
func checkProtocol(protocol string) error {
	switch protocol {
	case "http/protobuf", "http/json":
		return nil
	case "grpc":
		return ErrGRPCUnsupported
	default:
		return fmt.Errorf("forward: 지원하지 않는 otlp.protocol %q", protocol)
	}
}

// normalizeEndpoint 는 base endpoint 를 검증하고 뒤 슬래시를 떼어낸다.
//
// 오류 메시지에 endpoint 를 넣지 않는다. userinfo 가 들어 있으면 그것이 곧 자격증명 유출이다.
func normalizeEndpoint(endpoint string) (string, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		return "", errors.New("forward: otlp.endpoint 를 URL 로 파싱할 수 없음")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("forward: otlp.endpoint 는 http 또는 https 여야 함")
	}
	return strings.TrimRight(endpoint, "/"), nil
}

// Start 는 전송 워커를 띄운다. 여러 번 불러도 한 번만 뜬다.
//
// 워커는 하나다. 순서를 보존하고, 상위가 죽었을 때 동시 재시도로 몰려가지 않게 한다.
// 워커가 막히면 큐가 차고, 큐가 차면 버린다 — 그것이 설계된 동작이다.
//
// 수명은 Shutdown 만으로 제어한다. ctx 를 받지 않는 이유는, 데몬이 종료 ctx 를 먼저 취소한 뒤
// Shutdown 을 부르면 큐를 비울 기회가 사라지는 함정을 만들지 않기 위해서다.
func (f *Forwarder) Start() {
	f.startOnce.Do(func() {
		f.wg.Add(1)
		go func() {
			defer f.wg.Done()
			f.loop()
		}()
	})
}

func (f *Forwarder) loop() {
	for {
		select {
		case <-f.stopCtx.Done():
			return
		case j, ok := <-f.queue:
			if !ok {
				return
			}
			f.queuedBytes.Add(-int64(len(j.body)))
			f.deliver(j)
		}
	}
}

// Enqueue 는 전달을 예약한다. 큐가 가득 차면 **버리고 false 를 반환한다.**
//
// 절대 블로킹하지 않는다. 여기서 기다리면 수신기 핸들러가 밀리고 결국 벤더 exporter 가
// 기다리게 된다 — 정확히 §5.4 가 금지하는 구조다. 텔레메트리 손실은 허용, 개발 도구 지연은 불허.
//
// body 는 복사한다. 호출부(수신기)가 버퍼를 재사용해도 큐에 든 페이로드가 망가지지 않아야 한다.
func (f *Forwarder) Enqueue(kind otlpdecode.PayloadKind, enc otlpdecode.Encoding, body []byte) bool {
	if len(body) == 0 {
		return false
	}

	f.closeMu.RLock()
	defer f.closeMu.RUnlock()
	if f.closed {
		return false
	}

	size := int64(len(body))
	if f.queuedBytes.Load()+size > f.maxQueueBytes {
		f.dropQueueFull()
		return false
	}

	j := job{kind: kind, enc: enc, body: bytes.Clone(body)}
	select {
	case f.queue <- j:
		f.queuedBytes.Add(size)
		f.bump(func(s *Stats) { s.Enqueued++ })
		return true
	default:
		f.dropQueueFull()
		return false
	}
}

func (f *Forwarder) dropQueueFull() {
	f.statsMu.Lock()
	f.stats.DroppedQueueFull++
	total := f.stats.DroppedQueueFull
	shouldLog := time.Since(f.lastDropLog) >= maxDropLogGap
	if shouldLog {
		f.lastDropLog = time.Now()
	}
	f.statsMu.Unlock()

	if shouldLog {
		f.logger.Printf("forward: 상위 전달 큐 포화 — 텔레메트리를 버린다 (누적 %d 건)", total)
	}
}

// Shutdown 은 큐를 닫고 남은 항목을 비울 기회를 준다.
//
// ctx 가 먼저 끝나면 진행 중인 요청과 백오프 대기를 끊고 남은 것을 버린 뒤 오류와 함께
// 돌아온다. 데몬 종료가 상위 Collector 의 응답 속도에 묶이면 안 된다 (§5.4).
// 여러 번 불러도 안전하다.
func (f *Forwarder) Shutdown(ctx context.Context) error {
	f.closeMu.Lock()
	f.closeOnce.Do(func() {
		f.closed = true
		close(f.queue)
	})
	f.closeMu.Unlock()

	// Start 를 부른 적이 없으면 기다릴 워커가 없다. 남은 것을 세고 끝낸다.
	drained := make(chan struct{})
	go func() {
		f.wg.Wait()
		close(drained)
	}()

	select {
	case <-drained:
		f.stopCancel()
		// Start 를 부른 적이 없으면 큐에 남은 것을 아무도 읽지 않았다. 여기서 정리한다.
		f.discardQueued()
		return nil
	case <-ctx.Done():
	}

	// 제한 시간 초과 — 하드 스톱. 진행 중 요청의 ctx 가 stopCtx 파생이라 즉시 끊긴다.
	f.stopCancel()
	<-drained

	remaining := f.discardQueued()
	if remaining > 0 {
		f.logger.Printf("forward: 종료 제한 시간 초과 — 남은 %d 건을 버린다", remaining)
	}
	return fmt.Errorf("forward: 종료 시간 안에 큐를 비우지 못함 (%d 건 폐기): %w", remaining, ctx.Err())
}

// discardQueued 는 닫힌 큐에 남은 항목을 비우고 그 수를 돌려준다.
func (f *Forwarder) discardQueued() int64 {
	var n int64
	for j := range f.queue {
		f.queuedBytes.Add(-int64(len(j.body)))
		n++
	}
	if n > 0 {
		f.bump(func(s *Stats) { s.DroppedShutdown += n })
	}
	return n
}

// Stats 는 현재까지의 집계 스냅샷이다.
func (f *Forwarder) Stats() Stats {
	f.statsMu.Lock()
	defer f.statsMu.Unlock()
	return f.stats
}

func (f *Forwarder) bump(fn func(*Stats)) {
	f.statsMu.Lock()
	fn(&f.stats)
	f.statsMu.Unlock()
}

// deliver 는 페이로드 하나를 정리해 상위로 보낸다.
//
// 패닉을 삼키는 이유: 배치 하나가 워커를 죽이면 그 뒤 텔레메트리가 전부 사라지고,
// 최악의 경우 데몬이 내려간다. 페이로드 하나의 손실로 막는다.
func (f *Forwarder) deliver(j job) {
	defer func() {
		if r := recover(); r != nil {
			f.bump(func(s *Stats) { s.Failed++ })
			f.logger.Printf("forward: %s 전달 중 패닉 — 이 배치만 버린다: %v", j.kind, r)
		}
	}()

	// 프라이버시 집행 지점. 이 호출이 빠지면 회사가 수집하지 않기로 한 원문·tool details 가
	// 그대로 흘러간다 (ADR 0003). 인코딩은 Scrub 이 입력과 동일하게 유지한다.
	body, scrubbed, err := otlpdecode.Scrub(j.kind, j.body, j.enc, f.policy)
	if err != nil {
		// 정리에 실패했다면 보내지 않는다. 정리되지 않은 본문을 흘려보내느니 버린다.
		f.bump(func(s *Stats) { s.DroppedScrub++ })
		f.logger.Printf("forward: %s 페이로드 정리 실패 — 전달하지 않고 버린다: %v", j.kind, err)
		return
	}
	f.bump(func(s *Stats) {
		s.AttributesRemoved += int64(scrubbed.AttributesRemoved)
		s.BodiesCleared += int64(scrubbed.BodiesCleared)
	})

	f.send(j.kind, j.enc, body)
}

func (f *Forwarder) send(kind otlpdecode.PayloadKind, enc otlpdecode.Encoding, body []byte) {
	target := f.endpoint + kind.Path()
	contentType := enc.ContentType() // 입력 인코딩 보존 — 상위는 이 헤더를 믿는다.
	authRefreshed := false

	for attempt := 1; attempt <= f.maxAttempts; attempt++ {
		token, err := f.tokens.Token(f.stopCtx)
		if err != nil {
			f.bump(func(s *Stats) { s.TokenErrors++ })
			f.logger.Printf("forward: %s telemetry token 확보 실패 (%d/%d): %v", kind, attempt, f.maxAttempts, err)
			if attempt == f.maxAttempts || !f.waitBackoff(attempt, 0) {
				break
			}
			continue
		}

		status, retryAfter, err := f.post(target, contentType, body, token)
		switch classify(status, err) {
		case dispositionDone:
			f.bump(func(s *Stats) { s.Sent++ })
			return

		case dispositionAuth:
			// 토큰 만료로 본다. 캐시를 버리고 새 토큰으로 한 번만 다시 시도한다.
			f.tokens.Invalidate(token)
			if authRefreshed {
				f.bump(func(s *Stats) { s.Discarded++ })
				f.logger.Printf("forward: %s 상위 인증 거부 (HTTP %d) — 토큰 갱신 후에도 거부되어 폐기", kind, status)
				return
			}
			authRefreshed = true
			f.logger.Printf("forward: %s 상위 인증 거부 (HTTP %d) — 토큰 갱신 후 재시도", kind, status)
			continue

		case dispositionDiscard:
			// 요청 자체가 잘못됐다. 같은 본문을 다시 보내면 같은 답이 온다.
			f.bump(func(s *Stats) { s.Discarded++ })
			f.logger.Printf("forward: %s 상위 거부 (HTTP %d) — 재시도하지 않고 폐기", kind, status)
			return

		default: // dispositionRetry — 5xx·429·전송 오류. 예산이 남았을 때만 다시 시도한다.
			f.logRetryable(kind, attempt, status, err)
			if attempt < f.maxAttempts {
				f.bump(func(s *Stats) { s.Retries++ })
				if !f.waitBackoff(attempt, retryAfter) {
					return
				}
			}
		}
	}

	f.bump(func(s *Stats) { s.Failed++ })
	f.logger.Printf("forward: %s 전달 실패 — %d 회 시도 후 포기", kind, f.maxAttempts)
}

func (f *Forwarder) logRetryable(kind otlpdecode.PayloadKind, attempt, status int, err error) {
	if err != nil {
		f.logger.Printf("forward: %s 전송 오류 (%d/%d): %v", kind, attempt, f.maxAttempts, err)
		return
	}
	f.logger.Printf("forward: %s 상위 오류 (HTTP %d, %d/%d)", kind, status, attempt, f.maxAttempts)
}

// post 는 한 번 보낸다. 반환값은 상태 코드·Retry-After·전송 오류뿐이다.
// 응답 본문도 토큰도 호출부로 나가지 않는다.
func (f *Forwarder) post(target, contentType string, body []byte, token string) (int, time.Duration, error) {
	ctx, cancel := context.WithTimeout(f.stopCtx, f.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return 0, 0, sanitizeErr(err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := f.client.Do(req)
	if err != nil {
		return 0, 0, sanitizeErr(err)
	}
	defer resp.Body.Close()
	// 연결을 재사용할 수 있게 본문을 읽어 버린다. 내용은 어디에도 쓰지 않는다 —
	// 상위 응답에 무엇이 들어 있든 로그로 옮기지 않는다.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxUpstreamBodyRead))

	return resp.StatusCode, parseRetryAfter(resp.Header.Get("Retry-After"), time.Now(), f.maxBackoff), nil
}

// waitBackoff 는 재시도 전 대기다. 종료가 시작되면 false 를 돌려주고 즉시 포기한다.
func (f *Forwarder) waitBackoff(attempt int, retryAfter time.Duration) bool {
	delay := backoff(attempt, f.baseBackoff, f.maxBackoff)
	if retryAfter > delay {
		delay = retryAfter
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-f.stopCtx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// sanitizeErr 는 오류에서 요청 URL 을 떼어낸다.
//
// net/http 는 실패를 *url.Error 로 감싸고 그 안에 요청 URL 문자열을 통째로 담는다.
// endpoint 에 userinfo("https://user:secret@…")가 들어 있으면 그 오류를 로그에 찍는 순간
// 자격증명이 그대로 남는다. 원인만 남기고 URL 은 버린다.
func sanitizeErr(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		return urlErr.Err
	}
	return err
}

func orDefaultInt(v, fallback int) int {
	if v > 0 {
		return v
	}
	return fallback
}

func orDefaultInt64(v, fallback int64) int64 {
	if v > 0 {
		return v
	}
	return fallback
}

func orDefaultDuration(v, fallback time.Duration) time.Duration {
	if v > 0 {
		return v
	}
	return fallback
}
