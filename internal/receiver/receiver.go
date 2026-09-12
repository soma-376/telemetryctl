// Package receiver 는 데몬이 띄우는 loopback OTLP/HTTP 수신기다. Claude Code·Codex 가
// 회사 Collector 대신 여기로 시그널을 보내고, 데몬이 로컬 집계 후 상위로 전달한다
// (ADR 0001 인라인 프록시).
//
//	POST /v1/metrics · /v1/logs · /v1/traces
//	GET  /healthz    (인증 없음, status 명령이 사용)
//	GET  /v1/tray · POST /v1/tray/refresh (인증된 로컬 GUI API)
//	그 외             404
//
// # 이 패키지가 지키는 상한선은 §5.4 다
//
// docs/installation-architecture.md §5.4 「Collector 장애가 개발을 방해하면 안 됨」이
// 이 설계 전체의 제약이다. 개발 도구의 exporter 는 우리 응답을 기다린다 — 우리가 느려지면
// Claude Code 가 느려지고, 우리가 재시도를 유도하면 Claude Code 의 종료가 늦어진다.
// 그래서 핸들러는 **인증 → 본문 읽기 → 프레이밍 확인 → 유계 채널 non-blocking 전송 → 응답**
// 까지만 하고, 디코드·조립은 워커에게 넘긴다. 큐가 가득 차면 `429` 가 아니라
// `200` + PartialSuccess 로 답한다. 텔레메트리 손실은 허용, 개발 도구 지연은 불허다.
//
// # 파이프라인 결합을 여기서 끊는다
//
// 워커가 디코드한 결과를 어디로 보낼지는 9단계 배선의 몫이다. 이 패키지는 Sink 인터페이스만
// 정의하고 store·session·rollup·forward 중 어느 것도 import 하지 않는다. 그렇게 하지 않으면
// "OTLP 를 받는다" 는 작은 관심사가 파이프라인 전체에 묶여 따로 테스트할 수 없게 된다.
// 같은 이유로 protobuf 타입도 보지 않는다 — 그 격리는 otlpdecode 가 맡는다.
package receiver

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/your-org/pulsemetry/internal/otlpdecode"
)

const (
	// DefaultPort 는 OTLP/HTTP 기본 포트다. 사용 중이면 임의 포트로 폴백한다 (listen.go).
	DefaultPort = 4318

	// HealthPath 는 인증 없이 열려 있는 유일한 경로다. status 명령과 GUI 가
	// runtime.json 의 pid 판정을 확정하는 데 쓴다 (runtimeinfo.Load 주석).
	HealthPath = "/healthz"

	// LocalAPIPathPrefix 는 GUI 가 부르는 로컬 API 경로의 접두다. 개별 경로는
	// internal/localapi 가 정하고, 여기서는 인증만 태워 그대로 넘긴다 (ServeHTTP).
	LocalAPIPathPrefix = "/v1/tray"

	// DefaultMaxBodyBytes 는 요청 본문 상한이다 (계획서 「수신기 설계」의 4 MiB).
	// gzip 은 **압축 해제 후** 크기에 이 값을 건다 (body.go).
	DefaultMaxBodyBytes int64 = 4 << 20

	// DefaultQueueSize 는 워커 앞 유계 채널의 길이다. 4 MiB 배치가 이만큼 쌓이면
	// 이론상 1 GiB 지만, 실제 Claude Code 배치는 수십 KB 라 수 MB 수준에서 논다.
	// 이 값을 키우면 포화 시점이 늦춰지는 대신 종료 시 버려질 데이터가 늘어난다.
	DefaultQueueSize = 256

	// DefaultWorkers 는 디코드 워커 수다 (계획서 「수신기 설계」의 "워커 2개").
	DefaultWorkers = 2

	// defaultReadHeaderTimeout 은 헤더만 보내고 멈춘 연결을 정리한다. loopback 전용이라
	// 위협 모델이 크지 않지만, 리스너를 무한정 붙잡는 클라이언트를 막아 둔다.
	defaultReadHeaderTimeout = 10 * time.Second
)

// Batch 는 워커가 디코드를 마친 페이로드 하나다. 9단계 배선이 Sink 로 받아
// Result 는 session·rollup·store 로, Body 는 forward 로 보낸다.
type Batch struct {
	// Kind 는 어느 엔드포인트로 들어왔는지다. /v1/traces 는 Result 가 비어 있고
	// Body 만 의미가 있다 — 스팬은 events 스키마에 담을 자리가 없다 (otlpdecode.Decode).
	Kind otlpdecode.PayloadKind
	// Encoding 은 요청 Content-Type 에서 읽은 직렬화 형식이다. 상위 전달 시 이 인코딩을
	// 보존해야 한다 — Content-Type 과 본문이 어긋나면 진단 불가능한 조용한 실패가 난다.
	Encoding otlpdecode.Encoding
	// Body 는 압축을 푼 원본 바이트다. forward 가 otlpdecode.Scrub 으로 원문·tool details 를
	// 지운 뒤 상위 Collector 로 보내는 입력이다 (ADR 0003).
	Body []byte
	// ReceivedAt 은 핸들러가 본문을 다 읽은 시각이다.
	ReceivedAt time.Time
	// Result 는 정규화 결과다. Kind 가 PayloadTraces 면 제로값이다.
	Result otlpdecode.Result
}

// Sink 는 디코드된 배치를 받는 곳이다. 9단계가 store·session·forward 배선을 여기에 꽂는다.
//
// 구현은 **블로킹해도 된다.** 워커가 막히면 큐가 차고, 큐가 차면 핸들러가 200 + PartialSuccess
// 로 흘려보낸다 — 역압이 개발 도구까지 번지지 않고 이 경계에서 멈추도록 설계돼 있다.
// 다만 error 를 돌려주면 그 배치는 그대로 버려진다. 재시도는 Sink 안에서 결정할 일이다.
type Sink interface {
	Consume(ctx context.Context, b Batch) error
}

// SinkFunc 는 함수를 Sink 로 쓰기 위한 어댑터다.
type SinkFunc func(ctx context.Context, b Batch) error

func (f SinkFunc) Consume(ctx context.Context, b Batch) error { return f(ctx, b) }

// Options 는 수신기 구성이다.
type Options struct {
	// Port 는 바인딩할 포트다. 0 이하면 DefaultPort 를 쓴다. Server 에서만 의미가 있다.
	Port int
	// FixedPort 는 --listen 을 명시했다는 뜻이다. true 면 Port 를 잡지 못할 때
	// 임의 포트로 폴백하지 않고 하드 실패한다 (계획서 「리스크」).
	FixedPort bool

	// Token 은 loopback ingest bearer 토큰이다. 비어 있으면 New 가 거부한다 —
	// 인증 없는 수신기를 실수로 띄우는 경로를 만들지 않는다. EnsureToken 이 만들어 준다.
	Token string
	// LocalAPI 는 인증을 통과한 LocalAPIPathPrefix 요청 전부를 받는다 (internal/localapi).
	// 메서드·경로 판정은 그쪽 몫이다. nil이면 로컬 API를 열지 않는다.
	LocalAPI http.Handler

	// Decode 는 워커가 otlpdecode 에 넘길 옵션이다. InstallationID 가 비어 있으면
	// 모든 이벤트가 검증에서 거부되므로 New 가 미리 막는다.
	Decode otlpdecode.Options

	// Sink 는 필수다.
	Sink Sink

	QueueSize    int
	Workers      int
	MaxBodyBytes int64

	// Logger 는 nil 이면 로그를 버린다. 토큰은 어떤 경로로도 여기 흘러가지 않는다.
	Logger *log.Logger
	// Now 는 테스트용 시계다. nil 이면 time.Now.
	Now func() time.Time
}

func (o Options) normalized() Options {
	if o.QueueSize <= 0 {
		o.QueueSize = DefaultQueueSize
	}
	if o.Workers <= 0 {
		o.Workers = DefaultWorkers
	}
	if o.MaxBodyBytes <= 0 {
		o.MaxBodyBytes = DefaultMaxBodyBytes
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return o
}

func (o Options) validate() error {
	if o.Token == "" {
		return errors.New("receiver: ingest 토큰이 비어 있음")
	}
	if o.Sink == nil {
		return errors.New("receiver: Sink 는 필수")
	}
	if o.Decode.InstallationID == "" {
		return errors.New("receiver: Decode.InstallationID 는 필수 (비면 모든 이벤트가 폐기됨)")
	}
	return nil
}

// counters 는 원자적으로 갱신되는 누적 카운터다. Stats 로 스냅샷을 뜬다.
type counters struct {
	accepted     atomic.Int64
	dropped      atomic.Int64
	decoded      atomic.Int64
	decodeErrors atomic.Int64
	sinkErrors   atomic.Int64
	rejected     atomic.Int64
	unauthorized atomic.Int64
	tooLarge     atomic.Int64
	badRequest   atomic.Int64
	unsupported  atomic.Int64
}

// Stats 는 수신기 카운터 스냅샷이다. status 명령과 /healthz 가 그대로 쓴다.
// 비밀이 될 수 있는 값은 없다.
type Stats struct {
	Accepted     int64 `json:"accepted"`
	Dropped      int64 `json:"dropped"`
	Decoded      int64 `json:"decoded"`
	DecodeErrors int64 `json:"decode_errors"`
	SinkErrors   int64 `json:"sink_errors"`
	Rejected     int64 `json:"rejected"`
	Unauthorized int64 `json:"unauthorized"`
	TooLarge     int64 `json:"too_large"`
	BadRequest   int64 `json:"bad_request"`
	Unsupported  int64 `json:"unsupported_media"`
}

// queueItem 은 핸들러가 워커에게 넘기는 것이다. 디코드 전 상태라 아직 event 가 아니다.
type queueItem struct {
	kind     otlpdecode.PayloadKind
	enc      otlpdecode.Encoding
	body     []byte
	received time.Time
}

// Receiver 는 인증·라우팅·크기 제한·백프레셔를 담당하는 http.Handler 다.
//
// 리스너와 분리돼 있다. 라우팅과 인증은 httptest 로 전부 검증할 수 있고, 두 리스너 바인딩과
// 포트 폴백은 Server 쪽에서 따로 검증한다.
type Receiver struct {
	opt   Options
	token string

	queue chan queueItem
	wg    sync.WaitGroup

	ctx    context.Context
	cancel context.CancelFunc

	// mu 는 queue 를 닫는 순간과 핸들러의 전송이 겹치지 않게 한다. 닫힌 채널에 보내면
	// select 안이라도 panic 이므로 플래그 확인이 필요하다. 핸들러 쪽은 RLock 이라
	// 동시 요청끼리는 경합하지 않는다.
	mu     sync.RWMutex
	closed bool

	// authLog 는 401 로그의 폭주를 막는 상태다. stats.unauthorized 와 달리 뮤텍스를
	// 쓰는 이유는 "마지막으로 찍은 시각" 과 "그 뒤로 생략한 수" 를 원자적으로 함께
	// 갱신해야 하기 때문이다 — 둘을 따로 두면 접힌 건수가 어긋난다.
	authLogMu         sync.Mutex
	authLogLast       time.Time
	authLogSuppressed int64

	stats counters
}

// New 는 수신기를 만들고 워커를 띄운다. 반환된 Receiver 는 반드시 Close 해야 한다.
func New(opt Options) (*Receiver, error) {
	if err := opt.validate(); err != nil {
		return nil, err
	}
	opt = opt.normalized()
	ctx, cancel := context.WithCancel(context.Background())
	rc := &Receiver{
		opt:    opt,
		token:  opt.Token,
		queue:  make(chan queueItem, opt.QueueSize),
		ctx:    ctx,
		cancel: cancel,
	}
	rc.wg.Add(opt.Workers)
	for range opt.Workers {
		go rc.work()
	}
	return rc, nil
}

// Close 는 큐를 닫고 워커가 남은 배치를 끝낼 때까지 기다린다.
// 여러 번 불러도 안전하다.
func (rc *Receiver) Close() error {
	rc.mu.Lock()
	if rc.closed {
		rc.mu.Unlock()
		return nil
	}
	rc.closed = true
	close(rc.queue)
	rc.mu.Unlock()

	rc.wg.Wait()
	rc.cancel()
	return nil
}

// Stats 는 카운터 스냅샷이다.
func (rc *Receiver) Stats() Stats {
	return Stats{
		Accepted:     rc.stats.accepted.Load(),
		Dropped:      rc.stats.dropped.Load(),
		Decoded:      rc.stats.decoded.Load(),
		DecodeErrors: rc.stats.decodeErrors.Load(),
		SinkErrors:   rc.stats.sinkErrors.Load(),
		Rejected:     rc.stats.rejected.Load(),
		Unauthorized: rc.stats.unauthorized.Load(),
		TooLarge:     rc.stats.tooLarge.Load(),
		BadRequest:   rc.stats.badRequest.Load(),
		Unsupported:  rc.stats.unsupported.Load(),
	}
}

// QueueDepth 는 현재 큐에 쌓인 배치 수다.
func (rc *Receiver) QueueDepth() int { return len(rc.queue) }

// QueueCapacity 는 큐 길이다.
func (rc *Receiver) QueueCapacity() int { return cap(rc.queue) }

// authLogInterval 은 같은 인증 실패를 다시 찍기까지 기다리는 시간이다.
//
// 벤더 exporter 는 metrics 60초·logs 5초 주기로 재시도하므로, 설정이 어긋난 동안에는
// 401 이 끝없이 들어온다. 매 건 찍으면 로그가 그것만으로 채워져 정작 다른 문제를 덮는다.
const authLogInterval = time.Minute

// logUnauthorized 는 401 을 사유와 함께 남기되 분당 한 줄로 접는다.
//
// # 왜 로그가 필요한가
//
// 이 경로는 원래 카운터만 올리고 조용했다. 그 결과 벤더 설정에 X-Pulsemetry-Local 이
// 빠져 모든 배치가 401 로 사라지는 동안 에러 로그가 한 줄도 생기지 않았고, 증상은
// `telemetryctl status` 의 인증실패 카운터를 직접 읽기 전까지 진단할 수 없었다.
// 텔레메트리 손실은 허용하지만(§5.4), 손실이 **보이지 않는 것**은 허용하지 않는다.
//
// # 왜 첫 건은 무조건 찍는가
//
// 접는 규칙만 있으면 데몬을 띄운 직후의 한 건이 창 안에 묻혀 아무것도 안 보일 수 있다.
// 그 한 줄이 대개 유일하게 필요한 줄이다.
func (rc *Receiver) logUnauthorized(reason string, total int64) {
	rc.authLogMu.Lock()
	now := rc.opt.Now()
	first := rc.authLogLast.IsZero()
	if !first && now.Sub(rc.authLogLast) < authLogInterval {
		rc.authLogSuppressed++
		rc.authLogMu.Unlock()
		return
	}
	suppressed := rc.authLogSuppressed
	rc.authLogSuppressed = 0
	rc.authLogLast = now
	rc.authLogMu.Unlock()

	// 제시된 토큰·헤더 값·요청 경로는 담지 않는다. 사유와 개수만으로 무엇을 고쳐야
	// 하는지 알 수 있고, 그 이상은 비밀을 로그로 옮기는 일이다.
	rc.logf("인증 실패: %s (누계 %d, 최근 %d건 생략) — 벤더 설정이 로컬 수신기와 어긋났다면 "+
		"`telemetryctl local enable` 로 재병합하세요", reason, total, suppressed)
}

func (rc *Receiver) logf(format string, args ...any) {
	if rc.opt.Logger == nil {
		return
	}
	rc.opt.Logger.Printf(format, args...)
}

// ServeHTTP 는 계획서가 못박은 라우팅을 그대로 구현한다.
//
// **CORS 헤더는 어떤 응답에도 붙지 않는다.** 이 수신기를 브라우저에서 호출할 이유가 없고,
// Access-Control-Allow-Origin 을 한 번이라도 내보내면 사용자가 방문한 아무 웹페이지가
// 로컬 데몬에 요청을 던질 수 있게 된다. OPTIONS 를 405 로 끊는 것도 같은 이유다 —
// preflight 가 성공하지 못하면 브라우저는 본 요청을 보내지 않는다.
func (rc *Receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		// 경로를 보지 않고 먼저 끊는다. 존재 여부를 알려 줄 이유가 없다.
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if r.URL.Path == HealthPath {
		rc.serveHealth(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, LocalAPIPathPrefix) && rc.opt.LocalAPI != nil {
		if ok, reason := rc.authorize(r); !ok {
			total := rc.stats.unauthorized.Add(1)
			rc.logUnauthorized(reason, total)
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		rc.opt.LocalAPI.ServeHTTP(w, r)
		return
	}

	kind, ok := otlpdecode.PayloadKindFromPath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	rc.serveIngest(w, r, kind)
}

// serveHealth 는 인증 없이 열려 있다. runtime.json 의 pid 판정이 pid 재사용 때문에
// 확정적이지 않아서, status 명령이 "정말 우리 데몬인가" 를 여기서 확인한다.
// 응답에는 좌표와 카운터만 담는다 — 토큰·경로·프롬프트는 없다.
func (rc *Receiver) serveHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeHealth(w, healthBody{
		Status:        "ok",
		Service:       "pulsemetry-receiver",
		QueueDepth:    rc.QueueDepth(),
		QueueCapacity: rc.QueueCapacity(),
		Stats:         rc.Stats(),
	})
}

func (rc *Receiver) serveIngest(w http.ResponseWriter, r *http.Request, kind otlpdecode.PayloadKind) {
	if ok, reason := rc.authorize(r); !ok {
		total := rc.stats.unauthorized.Add(1)
		// 응답은 어느 검사에서 걸렸는지 알려 주지 않는다. 헤더 하나씩 맞춰 보는 탐색을
		// 돕지 않는다. 사유는 이 기계의 주인이 보는 데몬 로그로만 나간다.
		rc.logUnauthorized(reason, total)
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	enc, ok := otlpdecode.EncodingFromContentType(r.Header.Get("Content-Type"))
	if !ok {
		rc.stats.unsupported.Add(1)
		writeError(w, http.StatusUnsupportedMediaType, "unsupported content type")
		return
	}

	body, err := rc.readBody(w, r)
	if err != nil {
		switch {
		case errors.Is(err, errTooLarge):
			rc.stats.tooLarge.Add(1)
			writeError(w, http.StatusRequestEntityTooLarge, "payload too large")
		case errors.Is(err, errUnsupportedEncoding):
			rc.stats.unsupported.Add(1)
			writeError(w, http.StatusUnsupportedMediaType, "unsupported content encoding")
		default:
			rc.stats.badRequest.Add(1)
			writeError(w, http.StatusBadRequest, "malformed request body")
		}
		return
	}

	// 빈 본문은 유효한 빈 메시지다. 큐를 쓸 이유가 없으니 바로 성공으로 답한다.
	if len(body) == 0 {
		writeSuccess(w, enc)
		return
	}

	// 완전한 디코드는 워커가 한다. 여기서는 wire format 프레이밍만 O(n)·무할당으로 훑어
	// 명백한 쓰레기를 400 으로 돌려준다. 재시도해도 절대 성공하지 않을 요청을 200 으로
	// 받아 두면 exporter 는 성공했다고 믿고 우리는 워커 로그에만 흔적을 남긴다 —
	// 진단이 불가능해진다. 이 검사는 큐에 넣기 전에 끝나므로 §5.4 의 지연 상한을 건드리지 않는다.
	if !validPayloadFraming(body, enc) {
		rc.stats.badRequest.Add(1)
		writeError(w, http.StatusBadRequest, "malformed payload")
		return
	}

	item := queueItem{kind: kind, enc: enc, body: body, received: rc.opt.Now()}
	if !rc.enqueue(item) {
		rc.stats.dropped.Add(1)
		// 429 가 아니라 200 + PartialSuccess 다 (§5.4, ADR 0001).
		// 429 는 exporter 재시도를 유발하고, 재시도는 Claude Code 종료를 지연시킨다.
		// rejected 수를 0 으로 두는 것은 OTLP 스펙이 정의한 "경고" 형태다 —
		// 디코드 전이라 배치 안에 데이터포인트가 몇 개인지 알 수 없기 때문이다.
		writePartialSuccess(w, kind, enc, 0, queueFullMessage)
		return
	}
	rc.stats.accepted.Add(1)
	writeSuccess(w, enc)
}

const queueFullMessage = "local receiver queue is full; batch dropped"

// enqueue 는 유계 채널에 non-blocking 으로 넣는다. 여기서 절대 블로킹하지 않는 것이
// §5.4 의 핵심이다 — 이 함수가 기다리기 시작하는 순간 개발 도구가 기다린다.
func (rc *Receiver) enqueue(item queueItem) bool {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	if rc.closed {
		return false
	}
	select {
	case rc.queue <- item:
		return true
	default:
		return false
	}
}

func (rc *Receiver) work() {
	defer rc.wg.Done()
	for item := range rc.queue {
		rc.consume(item)
	}
}

// consume 은 디코드하고 Sink 로 넘긴다. 여기서 나는 오류는 전부 카운터와 로그로만 남는다 —
// 이미 200 으로 응답한 뒤라 클라이언트에게 알릴 방법이 없고, 알릴 필요도 없다.
func (rc *Receiver) consume(item queueItem) {
	res, err := otlpdecode.Decode(item.kind, item.body, item.enc, rc.opt.Decode)
	if err != nil {
		rc.stats.decodeErrors.Add(1)
		rc.logf("수신 배치 디코드 실패: kind=%s enc=%s bytes=%d: %v",
			item.kind, item.enc, len(item.body), err)
		return
	}
	rc.stats.decoded.Add(1)
	if n := res.Rejected.Total(); n > 0 {
		rc.stats.rejected.Add(int64(n))
	}

	batch := Batch{
		Kind:       item.kind,
		Encoding:   item.enc,
		Body:       item.body,
		ReceivedAt: item.received,
		Result:     res,
	}
	if err := rc.opt.Sink.Consume(rc.ctx, batch); err != nil {
		rc.stats.sinkErrors.Add(1)
		rc.logf("수신 배치 처리 실패: kind=%s events=%d: %v", item.kind, len(res.Events), err)
	}
}
