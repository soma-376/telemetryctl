// Package daemon 은 pulsemetry 의 상주 프로세스다.
//
// 9단계에서 이 패키지는 "주기적으로 로그만 찍는 자리표시자" 에서 앞의 일곱 패키지를 하나로
// 잇는 배선으로 바뀌었다. 흐르는 방향은 이렇다.
//
//	receiver.Sink
//	   ├── Batch.Body   → forward.Enqueue        (원본 페이로드, Scrub 은 포워더가)
//	   └── Batch.Result → session + rollup + store
//
// 이 패키지가 직접 하는 일은 세 가지뿐이다. (1) 기동 순서와 종료 순서를 정하고,
// (2) 두 집계기가 같은 순서를 보도록 직렬화 지점을 하나로 만들고(pipeline),
// (3) 세션 마감·롤업·prune·토큰 갱신 틱을 돌린다. 정규화·집계·저장 규칙은 전부
// 앞 단계 패키지가 소유하고 여기에는 복제하지 않는다.
//
// # 벤더 설정은 건드리지 않는다
//
// 포트 폴백이 일어나면 벤더 설정 재병합이 필요하지만, 실제 재병합(MergeClaude·MergeCodex
// 호출)은 12단계 `local enable` 의 몫이다. 여기서는 필요하다는 사실만 로그와
// runtime.json 에 남긴다 — 계획서가 "12 전까지 기존 사용자 설정이 바뀌지 않는다"를
// 명시했다.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/your-org/pulsemetry/internal/autostart"
	"github.com/your-org/pulsemetry/internal/forward"
	"github.com/your-org/pulsemetry/internal/hostenv"
	"github.com/your-org/pulsemetry/internal/installer"
	"github.com/your-org/pulsemetry/internal/otlpdecode"
	"github.com/your-org/pulsemetry/internal/receiver"
	"github.com/your-org/pulsemetry/internal/runtimeinfo"
	"github.com/your-org/pulsemetry/internal/store"
)

// 틱 주기. 각 값의 근거는 아래 주석에 있다.
const (
	// DefaultInterval 은 세션 마감·스냅샷 저장·조립기 정리 주기다.
	//
	// 유휴 임계값이 10분이므로 마감이 늦어질 수 있는 상한이 이 값이다. 30초면 최대
	// 0.5% 오차인데, 화면의 가장 작은 버킷이 1시간이라 눈에 보이지 않는다. 반대로 이
	// 값을 더 줄이면 세션 스냅샷 전체를 다시 쓰는 비용만 늘고 얻는 것이 없다 —
	// 세션 수치는 세션이 마감되기 전에는 어차피 확정값이 아니다.
	DefaultInterval = 30 * time.Second

	// DefaultFlushInterval 은 미저장 이벤트·롤업을 SQLite 로 내리는 주기다.
	//
	// 크래시 시 잃는 양의 상한이 곧 이 값이다. 2초면 Claude Code 의 로그 내보내기
	// 주기(5초)보다 짧아 사실상 배치 하나가 도착할 때마다 한 번 쓰는 셈이 된다.
	DefaultFlushInterval = 2 * time.Second

	// DefaultBatchEvents 는 크기 기준 flush 임계값이다. 시간 축만으로는 부족하다 —
	// 4 MiB 페이로드 하나에 수천 이벤트가 들어올 수 있고, 그것을 2초 동안 메모리에
	// 쌓아 두면 flush 한 번이 지나치게 큰 트랜잭션이 된다.
	DefaultBatchEvents = 512

	// DefaultPruneInterval 은 보존 정책 적용 주기다. 보존 단위가 일(day)이라 시간
	// 단위면 충분하고, 실패해도 다음 틱이 그대로 재시도한다.
	DefaultPruneInterval = time.Hour

	// DefaultTokenInterval 은 상위 전달 토큰 확보 확인 주기다.
	//
	// forward.TokenSource 는 401·403 을 만나야 캐시를 버리므로 이 틱은 평시에 사실상
	// no-op 이다. 그럼에도 도는 이유는 **첫 페이로드가 아니라 지금** 실패를 발견하기
	// 위해서다 — enroll 이 풀렸거나 서버가 죽었으면 텔레메트리가 조용히 큐에서
	// 버려지는데, 그 사실이 로그에 뜨는 시점이 몇 시간 뒤여서는 안 된다.
	DefaultTokenInterval = 15 * time.Minute

	// DefaultShutdownTimeout 은 종료 전체에 허용하는 시간이다. 세 단계(수신기·
	// 파이프라인·포워더)가 이 예산을 1/3 씩 나눠 쓴다. 데몬 종료가 상위 Collector 의
	// 응답 속도에 묶이면 안 된다는 §5.4 원칙이 여기에도 적용된다.
	DefaultShutdownTimeout = 15 * time.Second

	// sessionMemoryTTL 은 마감된 세션을 조립기 메모리에 남겨 두는 기간이다.
	// 유휴 임계값(10분)의 12배 — 늦게 도착한 이벤트가 세션을 되살릴 여유는 주되,
	// 보존 정책이 지운 타임라인을 스냅샷이 되살릴 여지는 남기지 않는 크기다.
	sessionMemoryTTL = 2 * time.Hour

	// storeWriteTimeout·storePruneTimeout 은 SQLite 호출 하나의 상한이다.
	storeWriteTimeout = 5 * time.Second
	storePruneTimeout = 30 * time.Second

	// tokenWarmTimeout 은 토큰 확보 확인 한 번의 상한이다.
	tokenWarmTimeout = 15 * time.Second

	// pipelineQueue 는 소유자 고루틴 앞의 커맨드 버퍼다. 수신기 워커 2개 + 틱들이
	// 잠깐 겹치는 것만 흡수하면 되므로 작게 둔다. 진짜 버퍼는 수신기 큐다.
	pipelineQueue = 8
)

// Options 는 데몬 구성이다. CLI 플래그와 state.Local 이 여기로 모인다.
type Options struct {
	StatePath string
	Logger    *log.Logger

	// DataDir 는 --data-dir 다. 비우면 state.Local.DataDir, 그것도 비면
	// store.DefaultDataDir(~/.pulsemetry).
	DataDir string

	// ListenPort 는 --listen 에서 뽑은 포트다. 0 이면 state.Local.ListenPort,
	// 그것도 0 이면 receiver.DefaultPort.
	ListenPort int
	// FixedPort 는 --listen 을 명시했다는 뜻이다. true 면 포트를 잡지 못할 때
	// 임의 포트로 폴백하지 않고 하드 실패한다.
	FixedPort bool

	// DisableReceiver·DisableForward 는 --no-receiver·--no-forward 다.
	DisableReceiver bool
	DisableForward  bool
	// NoStoreContent 는 --no-store-content 다. state.Local.StoreContent 를 끄는
	// 방향으로만 작동한다 — 플래그로 프라이버시 설정을 켜 주지는 않는다.
	NoStoreContent bool

	// 틱 주기. 0 이면 위의 기본값.
	Interval        time.Duration
	FlushInterval   time.Duration
	PruneInterval   time.Duration
	TokenInterval   time.Duration
	ShutdownTimeout time.Duration
	BatchEvents     int

	// IngestToken 은 loopback ingest 토큰이다. 비우면 receiver.EnsureToken 이
	// 키링에서 읽거나 만든다. 키링을 쓸 수 없는 환경(테스트·헤드리스)의 통로다.
	IngestToken string
	// ForwardTokens 는 상위 전달 토큰 공급자다. 비우면 state.ServerURL 로
	// forward.NewTelemetryTokenSource 를 만든다(운영 경로).
	ForwardTokens forward.TokenSource

	// Ready 는 기동이 끝나 요청을 받을 수 있게 된 순간 한 번 호출된다.
	// runtime.json 에 쓴 것과 같은 값을 준다.
	Ready func(runtimeinfo.Info)
	// Now 는 테스트용 시계다. nil 이면 time.Now.
	Now func() time.Time
}

func (o Options) normalized() (Options, error) {
	if o.Logger == nil {
		return o, errors.New("daemon logger is required")
	}
	if o.StatePath == "" {
		return o, errors.New("daemon state path is required")
	}
	if o.Interval <= 0 {
		o.Interval = DefaultInterval
	}
	if o.FlushInterval <= 0 {
		o.FlushInterval = DefaultFlushInterval
	}
	if o.PruneInterval <= 0 {
		o.PruneInterval = DefaultPruneInterval
	}
	if o.TokenInterval <= 0 {
		o.TokenInterval = DefaultTokenInterval
	}
	if o.ShutdownTimeout <= 0 {
		o.ShutdownTimeout = DefaultShutdownTimeout
	}
	if o.BatchEvents <= 0 {
		o.BatchEvents = DefaultBatchEvents
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return o, nil
}

// Run 은 데몬을 기동하고 ctx 가 끝날 때까지 돌린다. 반환 전에 graceful shutdown 을 마친다.
func Run(ctx context.Context, opts Options) error {
	opts, err := opts.normalized()
	if err != nil {
		return err
	}

	d := &daemon{opts: opts, log: opts.Logger}
	if err := d.start(ctx); err != nil {
		// 부분 기동을 그대로 두면 리스너·DB 핸들·runtime.json 이 남는다.
		d.shutdown()
		return err
	}
	d.loop(ctx)
	d.shutdown()
	return nil
}

type daemon struct {
	opts Options
	log  *log.Logger

	state  *installer.State
	db     *store.DB
	fwd    *forward.Forwarder
	srv    *receiver.Server
	pipe   *pipeline
	tokens forward.TokenSource

	dataDir     string
	runtimePath string

	tokenWarming atomic.Bool
}

// start 는 기동 순서를 정한다. 순서가 의미를 갖는 지점에 주석을 남겼다.
func (d *daemon) start(ctx context.Context) error {
	state, migrated, err := installer.LoadStateMigrated(d.opts.StatePath)
	if err != nil {
		return fmt.Errorf("load installation state: %w", err)
	}
	if state == nil || state.InstallationID == "" {
		return fmt.Errorf("pulsemetry is not enrolled: state file not found at %s", d.opts.StatePath)
	}
	d.state = state
	if migrated {
		// 올림을 디스크에 굳힌다. 실패해도 기동은 계속한다 — 메모리 상 상태로 정상
		// 동작하고 다음 기동이 다시 시도한다. 여기서 죽으면 업그레이드가 곧 장애다.
		if err := installer.SaveState(d.opts.StatePath, state); err != nil {
			d.log.Printf("경고: 상태 스키마 %d 올림을 저장하지 못했다 (동작에는 지장 없음): %v",
				installer.StateSchemaVersion, err)
		} else {
			d.log.Printf("설치 상태를 스키마 %d 로 올렸다: %s", installer.StateSchemaVersion, d.opts.StatePath)
		}
	}

	dataDir, err := d.resolveDataDir()
	if err != nil {
		return err
	}
	d.dataDir = dataDir

	// 원문 보관은 기본 ON, opt-out 이다 (ADR 0003). 플래그는 끄는 방향으로만 작동한다.
	storeContent := state.Local.StoreContent && !d.opts.NoStoreContent

	db, err := store.Open(ctx, store.PathIn(dataDir),
		store.WithContentStorage(storeContent),
	)
	if err != nil {
		return fmt.Errorf("open local store: %w", err)
	}
	d.db = db
	if err := db.SetMeta(ctx, store.MetaInstallationID, state.InstallationID); err != nil {
		d.log.Printf("경고: meta.installation_id 기록 실패: %v", err)
	}
	if err := db.SetMeta(ctx, store.MetaRetentionDays, strconv.Itoa(store.DefaultRetentionDays)); err != nil {
		d.log.Printf("경고: meta.retention_days 기록 실패: %v", err)
	}

	// 포워더를 수신기보다 먼저 띄운다. 첫 페이로드가 도착하는 순간 큐가 이미 살아
	// 있어야 §5.4 의 "수신은 전달 결과를 기다리지 않는다" 가 성립한다.
	if err := d.startForwarder(); err != nil {
		return err
	}

	d.pipe = newPipeline(pipelineConfig{
		DB:           db,
		Forwarder:    d.fwd,
		Logger:       d.log,
		Now:          d.opts.Now,
		BatchEvents:  d.opts.BatchEvents,
		QueueSize:    pipelineQueue,
		WriteTimeout: storeWriteTimeout,
		PruneTimeout: storePruneTimeout,
		SessionTTL:   sessionMemoryTTL,
	})

	if err := d.startReceiver(); err != nil {
		return err
	}

	info := d.runtimeInfo()
	// runtime.json 은 "데몬이 지금 어디서 듣고 있는가" 를 답하는 파일이라 듣는 곳이
	// 없으면 쓸 것이 없다. runtimeinfo.Write 도 listen_port 가 양수임을 요구한다.
	// --no-receiver 는 진단용 플래그이므로 여기서 조용히 건너뛰는 편이, 매번 실패
	// 경고를 내거나 runtimeinfo 의 검증을 느슨하게 푸는 것보다 낫다.
	if d.srv != nil {
		d.runtimePath = runtimeinfo.PathIn(dataDir)
		if err := runtimeinfo.Write(d.runtimePath, info); err != nil {
			// 이 파일이 없으면 status·GUI 가 데몬을 못 찾을 뿐 수집은 정상이다.
			d.log.Printf("경고: runtime.json 기록 실패 (status·GUI 가 데몬을 찾지 못한다): %v", err)
			d.runtimePath = ""
		}
	} else {
		d.log.Printf("--no-receiver: 듣는 주소가 없어 runtime.json 을 쓰지 않는다")
	}

	d.log.Printf("데몬 기동: installation_id=%s db=%s 보존=%d일 원문보관=%t 수신=%t 전달=%t",
		state.InstallationID, db.Path(), store.DefaultRetentionDays, storeContent,
		!d.opts.DisableReceiver, d.fwd != nil)

	if d.opts.Ready != nil {
		d.opts.Ready(info)
	}
	return nil
}

func (d *daemon) resolveDataDir() (string, error) {
	if d.opts.DataDir != "" {
		return d.opts.DataDir, nil
	}
	if d.state.Local.DataDir != "" {
		return d.state.Local.DataDir, nil
	}
	env, err := hostenv.Detect()
	if err != nil {
		return "", fmt.Errorf("데이터 디렉터리 판별 실패: %w", err)
	}
	return store.DefaultDataDir(env), nil
}

// startForwarder 는 상위 Collector 전달기를 띄운다.
func (d *daemon) startForwarder() error {
	if d.opts.DisableForward {
		d.log.Printf("--no-forward: 상위 Collector 전달을 끈다. 수신과 로컬 집계만 한다")
		return nil
	}

	tokens := d.opts.ForwardTokens
	if tokens == nil {
		if d.state.ServerURL == "" {
			return errors.New("상태 파일에 server_url 이 없어 상위 전달 토큰을 받을 수 없다: " +
				"telemetryctl reconnect --server <url> 로 채우거나 --no-forward 로 수신만 하라")
		}
		var err error
		tokens, err = forward.NewTelemetryTokenSource(d.state.ServerURL)
		if err != nil {
			return fmt.Errorf("telemetry token 공급자 생성: %w", err)
		}
	}
	d.tokens = tokens

	fwd, err := forward.New(forward.Options{
		// 회사 manifest **원본**이다. 로컬 사본이 아니다 — 상위로 나갈 때 지울 기준은
		// 회사가 수집하기로 한 범위여야 한다 (ADR 0003).
		Manifest: d.state.Manifest,
		Tokens:   tokens,
		Logger:   d.log,
	})
	if err != nil {
		if errors.Is(err, forward.ErrGRPCUnsupported) {
			// 조용히 수신만 하고 넘어가면, 벤더 설정이 우리를 가리키는 순간 회사
			// Collector 로 가던 스트림이 통째로 끊긴 채 아무도 모르게 된다. 거부하되
			// 의도적으로 수신만 하려는 경로(--no-forward)를 메시지에 적어 준다.
			return fmt.Errorf("상위 전달 프로토콜이 grpc 라 이 버전은 전달할 수 없다 (%w). "+
				"수신과 로컬 집계만 하려면 --no-forward 로 실행하라 — gRPC 전달은 후속 티켓이다", err)
		}
		return fmt.Errorf("포워더 생성: %w", err)
	}
	fwd.Start()
	d.fwd = fwd
	return nil
}

// startReceiver 는 loopback OTLP 수신기를 띄운다.
func (d *daemon) startReceiver() error {
	if d.opts.DisableReceiver {
		d.log.Printf("--no-receiver: 로컬 OTLP 수신기를 띄우지 않는다")
		return nil
	}

	token := d.opts.IngestToken
	if token == "" {
		var err error
		token, err = receiver.EnsureToken()
		if err != nil {
			return fmt.Errorf("loopback ingest 토큰 확보: %w", err)
		}
	}

	requested := d.opts.ListenPort
	if requested <= 0 {
		requested = d.state.Local.ListenPort
	}
	if requested <= 0 {
		requested = receiver.DefaultPort
	}

	srv, err := receiver.Start(receiver.Options{
		Port:      requested,
		FixedPort: d.opts.FixedPort,
		Token:     token,
		Sink:      d.pipe,
		Logger:    d.log,
		Decode:    otlpdecode.Options{InstallationID: d.state.InstallationID},
		Now:       d.opts.Now,
	})
	if err != nil {
		return fmt.Errorf("로컬 수신기 기동: %w", err)
	}
	d.srv = srv

	if srv.Port() != requested {
		// 재병합이 필요하다는 **사실만** 남긴다. 벤더 설정을 실제로 고치는 것은
		// 12단계 `local enable` 의 몫이다 (계획서: 12 전까지 사용자 설정 불변).
		//
		// state.Local.ListenPort 는 덮어쓰지 않는다. 그 값은 "설정된 의도" 이고
		// 벤더 설정에 적힌 주소가 거기서 나왔다. 실제 포트로 덮는 순간 "설정과
		// 현실이 어긋났다"는 신호가 사라져 재병합 판단의 근거가 없어진다.
		d.log.Printf("경고: 요청 포트 %d 를 잡지 못해 %d 로 폴백했다. 벤더 설정은 여전히 %d 를 "+
			"가리키므로 재병합이 필요하다 — telemetryctl local enable 을 다시 실행하라 "+
			"(실제 포트는 runtime.json 의 listen_port 에 있다)",
			requested, srv.Port(), requested)
	}
	if !srv.HasIPv6() {
		d.log.Printf("경고: IPv6 loopback([::1]) 리스너가 없다. localhost 가 ::1 로 풀리는 " +
			"클라이언트는 연결하지 못한다")
	}
	return nil
}

func (d *daemon) runtimeInfo() runtimeinfo.Info {
	info := runtimeinfo.Info{
		PID:          os.Getpid(),
		StartedAt:    d.opts.Now().UTC().Format(time.RFC3339),
		DataDir:      d.dataDir,
		DatabasePath: store.PathIn(d.dataDir),
		Version:      installer.Version,
	}
	if d.srv != nil {
		info.Endpoint = d.srv.Endpoint()
		info.ListenPort = d.srv.Port()
		info.ListenAddrs = d.srv.Addrs()
	}
	return info
}

// loop 는 주기 작업 틱을 돌린다. ctx 가 끝나면 돌아온다.
func (d *daemon) loop(ctx context.Context) {
	flushT := time.NewTicker(d.opts.FlushInterval)
	defer flushT.Stop()
	sessionT := time.NewTicker(d.opts.Interval)
	defer sessionT.Stop()
	pruneT := time.NewTicker(d.opts.PruneInterval)
	defer pruneT.Stop()
	tokenT := time.NewTicker(d.opts.TokenInterval)
	defer tokenT.Stop()

	// 기동 직후 한 번씩 돌린다. 한 시간을 못 살고 재시작되는 환경에서도 보존 정책이
	// 적용되고, 토큰 문제를 첫 페이로드가 아니라 지금 발견한다.
	d.pipe.submit(cmdPrune)
	d.rotateAutostartLogs()
	d.warmToken(ctx)

	for {
		select {
		case <-ctx.Done():
			d.log.Printf("데몬 정지 신호: %v", ctx.Err())
			return
		case <-flushT.C:
			d.pipe.submit(cmdFlush)
		case <-sessionT.C:
			d.pipe.submit(cmdSessions)
		case <-pruneT.C:
			d.pipe.submit(cmdPrune)
			d.rotateAutostartLogs()
		case <-tokenT.C:
			d.warmToken(ctx)
		}
	}
}

// rotateAutostartLogs 는 자동 실행 로그가 상한을 넘으면 회전시킨다 (PROJ-55).
//
// 보존 정책 틱에 얹는 이유는 성격이 같기 때문이다 — 둘 다 "오래되거나 커진 것을 시간
// 단위로 정리한다" 이고, 실패해도 다음 틱이 그대로 재시도한다.
//
// **이 패키지는 플랫폼을 알 필요가 없다.** launchd 만 로그 파일을 남기고 systemd 는
// journald 가 로테이션까지 맡는데, 그 판단은 전부 autostart.RotateLogs 안에 있다
// (LogDir 가 비면 no-op). 실패는 로깅만 한다 — 로그가 커진 것은 수집을 멈출 이유가 아니다.
func (d *daemon) rotateAutostartLogs() {
	env, err := hostenv.Detect()
	if err != nil {
		return
	}
	if err := autostart.RotateLogs(env); err != nil {
		d.log.Printf("경고: 자동 실행 로그 회전 실패: %v", err)
	}
}

// warmToken 은 상위 전달 토큰이 확보 가능한지 확인한다.
//
// 별도 고루틴에서 도는 이유는 재발급이 최대 tokenWarmTimeout 만큼 걸릴 수 있고, 그동안
// 틱 루프가 멈추면 flush 가 그만큼 밀리기 때문이다. 겹쳐 도는 것은 플래그로 막는다.
// 토큰 값은 버린다 — 이 함수에서 확인하는 것은 "받을 수 있는가" 뿐이다.
func (d *daemon) warmToken(ctx context.Context) {
	if d.tokens == nil {
		return
	}
	if !d.tokenWarming.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer d.tokenWarming.Store(false)
		c, cancel := context.WithTimeout(ctx, tokenWarmTimeout)
		defer cancel()
		if _, err := d.tokens.Token(c); err != nil && ctx.Err() == nil {
			d.log.Printf("경고: telemetry token 을 확보하지 못했다 — 상위 전달이 큐에서 버려진다: %v", err)
		}
	}()
}

// shutdown 은 graceful shutdown 이다. 순서가 데이터 손실을 가른다.
//
//  1. 수신기 — 새 유입을 끊고 이미 큐에 든 배치를 워커가 마저 처리하게 한다.
//  2. 파이프라인 — 남은 커맨드를 처리하고 조립 중인 세션·미저장 롤업을 store 에 flush.
//  3. 포워더 — 남은 전달 큐를 비울 기회를 준다.
//  4. DB close
//  5. runtime.json 제거 — 이것이 마지막이어야 status 가 "떠 있는데 파일이 없다"를
//     보지 않는다.
//
// 각 단계는 전체 예산(ShutdownTimeout)을 1/3 씩 나눠 쓰고, 앞 단계가 남긴 시간은
// 뒤로 넘어간다. 어느 단계도 무한정 기다리지 않는다 (§5.4).
func (d *daemon) shutdown() {
	total := d.opts.ShutdownTimeout
	if total <= 0 {
		total = DefaultShutdownTimeout
	}
	overall := time.Now().Add(total)
	stage := total / 3
	d.log.Printf("데몬 종료 시작 (제한 %s)", total)

	if d.srv != nil {
		ctx, cancel := context.WithDeadline(context.Background(), stageDeadline(overall, stage))
		if err := d.srv.Shutdown(ctx); err != nil {
			d.log.Printf("경고: 수신기 종료가 깔끔하지 않았다: %v", err)
		}
		cancel()
	}

	// 파이프라인은 수신기가 완전히 멈춘 뒤에 닫는다. 반대로 하면 종료 직전에 도착한
	// 배치가 Consume 에서 갈 곳을 잃는다.
	flushed := true
	if d.pipe != nil {
		flushed = d.pipe.close(stageDeadline(overall, stage))
		if !flushed {
			d.log.Printf("경고: 제한 시간 안에 파이프라인을 비우지 못했다. 미저장 집계가 남을 수 있다")
		}
	}

	if d.fwd != nil {
		ctx, cancel := context.WithDeadline(context.Background(), stageDeadline(overall, stage))
		if err := d.fwd.Shutdown(ctx); err != nil {
			d.log.Printf("경고: 상위 전달 큐를 다 비우지 못했다: %v", err)
		}
		cancel()
	}

	if d.db != nil {
		// 파이프라인이 제한 시간을 넘겼다면 쓰기가 아직 진행 중일 수 있다. 그 상태에서
		// 닫으면 진행 중인 트랜잭션을 우리 손으로 깨뜨린다. WAL 이라 안 닫고 나가도
		// 다음 기동이 복구하므로, 애매하면 닫지 않는 쪽이 안전하다.
		if flushed {
			if err := d.db.Close(); err != nil {
				d.log.Printf("경고: DB 닫기 실패: %v", err)
			}
		} else {
			d.log.Printf("경고: 파이프라인이 아직 쓰는 중이라 DB 를 닫지 않는다 (WAL 이 복구한다)")
		}
	}

	if d.runtimePath != "" {
		if err := runtimeinfo.Remove(d.runtimePath); err != nil {
			d.log.Printf("경고: runtime.json 제거 실패: %v", err)
		}
	}

	d.logSummary()
}

func (d *daemon) logSummary() {
	if d.pipe != nil {
		s := d.pipe.Stats()
		d.log.Printf("파이프라인 요약: 배치=%d 이벤트=%d 저장=%d 창중복=%d DB중복=%d 원문=%d(버림 %d) "+
			"세션=%d(마감 %d) 롤업행=%d 저장실패=%d prune실패=%d 전달버림=%d UNSPECIFIED폐기=%d",
			s.Batches, s.Events, s.EventsStored, s.Duplicates, s.EventsDuplicate,
			s.ContentsStored, s.ContentsDropped,
			s.SessionsWritten, s.SessionsClosed, s.RollupRows,
			s.WriteErrors, s.PruneErrors, s.ForwardDropped, s.DroppedTemporality)
	}
	if d.srv != nil {
		r := d.srv.Stats()
		d.log.Printf("수신기 요약: 수락=%d 버림=%d 디코드=%d 디코드실패=%d 거부항목=%d "+
			"인증실패=%d 과대=%d 잘못된요청=%d 미지원타입=%d",
			r.Accepted, r.Dropped, r.Decoded, r.DecodeErrors, r.Rejected,
			r.Unauthorized, r.TooLarge, r.BadRequest, r.Unsupported)
	}
	if d.fwd != nil {
		f := d.fwd.Stats()
		d.log.Printf("전달 요약: 큐=%d 전송=%d 큐포화버림=%d 시그널차단=%d scrub버림=%d 종료버림=%d "+
			"폐기4xx=%d 실패=%d 재시도=%d 토큰오류=%d 속성제거=%d 본문제거=%d",
			f.Enqueued, f.Sent, f.DroppedQueueFull, f.DroppedSignalDisabled, f.DroppedScrub, f.DroppedShutdown,
			f.Discarded, f.Failed, f.Retries, f.TokenErrors,
			f.AttributesRemoved, f.BodiesCleared)
	}
}

// stageDeadline 은 단계 예산과 전체 예산 중 먼저 오는 쪽을 고른다.
func stageDeadline(overall time.Time, budget time.Duration) time.Time {
	d := time.Now().Add(budget)
	if d.After(overall) {
		return overall
	}
	return d
}
