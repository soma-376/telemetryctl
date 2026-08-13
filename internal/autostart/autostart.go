// Package autostart 는 telemetryctl daemon 을 로그인 시 자동으로 띄우도록 OS 의 **사용자**
// 서비스 관리자에 등록한다 (PROJ-55).
//
// 배선이 opt-out 기본 ON 이 된 뒤(PROJ-45, ADR 0006) **데몬이 떠 있지 않은 상태는
// 텔레메트리가 로컬에도 회사에도 남지 않는 상태**이고, 이제 enroll 한 모든 사용자가 그
// 상태를 지나간다. 그전까지 코드가 할 수 있는 일은 경고 출력뿐이었다. 이 패키지가 그
// 구멍을 막는다.
//
// 재시작 정책(비정상 종료일 때만 재시작)의 배경은 ADR 0007 에 있다. 아래 네 결정은
// ADR 로 승격하지 않고 여기 주석으로 남긴다 — 되돌리기 쉽고 diff 만 봐도 드러나는
// 판단들이다.
//
// # 1. 사용자 수준 서비스만 쓴다
//
// macOS 는 LaunchAgent(~/Library/LaunchAgents), 리눅스는 `systemctl --user` 다.
// LaunchDaemon·시스템 유닛을 쓰지 않는 이유는 그것이 root 로 **로그인 전에** 돌아
// 사용자 로그인 키체인을 읽지 못하기 때문이다 — credential/go-keyring 이 실패하면
// receiver.EnsureToken 이 실패하고 데몬 전체가 뜨지 못한다. sudo 를 요구하는 것도
// 한 줄 설치 부트스트랩이 가정할 수 없는 조건이다.
//
// # 2. 빌드 태그를 쓰지 않는다 — 런타임 switch + 주입 가능한 GOOS
//
// 이 기능에는 컴파일 타임 플랫폼 의존성이 없다. exec.Command("launchctl", …) 는
// 리눅스에서도 컴파일되고 나머지는 os·filepath·문자열 조립뿐이다. 빌드 태그로 나누면
// 가장 값어치 있는 테스트(plist XML·systemd INI 문자열 생성)가 해당 OS 러너에서만 돌고
// `go vet ./...` 도 현재 GOOS 파일만 본다. 런타임 switch 면 **두 렌더러 전부가 모든
// 러너에서** 단위 테스트된다. 선례: installer.BackupDir, runtimeinfo.Info.ProcessAlive.
//
// # 3. 등록 상태를 state.json 에 저장하지 않는다
//
// installer.Local 이 담는 "설정된 의도" 는 **그 값에서 다른 산출물이 파생된 경우**를
// 말한다 — 벤더 설정 파일이 Local.ListenPort 로부터 쓰였기 때문에 runtime.json 과의
// 어긋남이 신호가 된다. 자동 실행에는 그런 파생이 없다. plist·unit 파일 **자체가
// 산출물**이고 OS 서비스 관리자가 권위 있는 소스다.
//
// 저장하면 세 번째 진실이 생겨 거짓말을 한다 — 사용자가 `systemctl --user disable` 하거나
// macOS 로그인 항목에서 끄면 state.json 만 enabled 로 남는다. 읽는 비용도 os.Stat 한 번 +
// 서비스 관리자 조회 한 번이라 캐시할 이유가 없고, 미enroll 장비에서도 Status 는 동작해야 한다.
//
// 대가: `autostart disable` 한 사용자가 다시 enroll 하면 조용히 재등록된다. re-enroll 은
// 드물고 명시적인 설정 행위이며 Apply 가 이미 local 배선을 매번 재적용하므로 받아들인다.
// 실제로 거슬린다는 보고가 오면 installer.Local 에 AutostartOptOut 을 추가하며 상태 스키마를
// 올린다 — **그 전에는 올리지 않는다.** 스키마 5 는 ADR 0006 Follow-up 2(기존 설치자
// 일괄 전환)의 몫이고 PROJ-55 가 그 번호를 소비하면 안 된다.
//
// # 4. linger 를 켜지 않는다
//
// `loginctl enable-linger` 없이는 로그인 시 시작·로그아웃 시 종료이고, 이것이 launchd
// LaunchAgent 와 **정확히 같은 의미론**이라 두 플랫폼이 대칭이 된다. linger 는 로그인한
// 사용자 없이 텔레메트리를 수집하는 프라이버시 의미론 변경이고, 세션이 없으면 Secret
// Service 도 없어 EnsureToken 이 실패해 크래시 루프가 보장된다. polkit 프롬프트도 뜰 수
// 있어 한 줄 설치 경로에 부적합하다. 대신 Enable 이 안내 한 줄을 돌려준다.
package autostart

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/your-org/pulsemetry/internal/hostenv"
)

// Kind 는 이 호스트에서 쓰는 서비스 관리자다.
type Kind string

const (
	KindLaunchd     Kind = "launchd"
	KindSystemdUser Kind = "systemd-user"
	KindNone        Kind = "none"
)

func (k Kind) String() string { return string(k) }

// TimeoutStopSec 은 systemd 가 SIGTERM 후 SIGKILL 까지 기다리는 시간이다.
//
// **daemon.DefaultShutdownTimeout 보다 반드시 커야 한다.** 작으면 systemd 가 flush
// 도중 SIGKILL 을 보내 미저장 집계를 잃고 runtime.json 이 남는다. 두 값이 어긋나면
// internal/daemon 의 테스트가 잡는다 — 그쪽에 두는 이유는 이 패키지가 daemon 을 import
// 하면 SQLite·protobuf 가 CLI 의 status 경로까지 딸려 들어오기 때문이다.
const TimeoutStopSec = 20 * time.Second

const (
	// ThrottleInterval 은 launchd 가 job 을 다시 띄우기까지의 최소 간격이다(초).
	// launchd 하한은 10 이다. 미enroll 상태의 크래시 루프를 싸게 만드는 것이 목적이다.
	ThrottleInterval = 30

	// RestartSec 은 systemd 재시작 간격이다. 기본값(100ms)은 깨진 설치를 초당 열 번 두드린다.
	RestartSec = 5 * time.Second
	// StartLimitIntervalSec·StartLimitBurst 는 재시작 폭주 제한이다. 미enroll 상태
	// (state.json 없음)에서 daemon.Run 이 즉시 실패하므로 이 상한이 필요하다.
	StartLimitIntervalSec = 300
	StartLimitBurst       = 5
)

const (
	syslogIdentifier    = "pulsemetry"
	defaultDaemonSubcmd = "daemon"
)

// 센티널. installer.ErrGRPCUnsupported 와 같은 방식으로 errors.Is 판정에 쓴다.
//
// ErrNotRegistered 는 **만들지 않는다.** "없음은 오류가 아니다" 규칙(installer.LoadState 가
// (nil, nil), runtimeinfo.Read)을 따른다 — Status 는 Status{Registered:false}, nil 을,
// Disable 은 Result{AlreadyInState:true}, nil 을 돌려준다(installer.LocalReport.AlreadyInState 대칭).
var (
	// ErrUnsupportedPlatform 은 windows 다. 작업 스케줄러 등록은 PROJ-56 이다.
	ErrUnsupportedPlatform = errors.New("이 OS 는 아직 자동 실행 등록을 지원하지 않습니다")
	// ErrNoServiceManager 는 systemd 가 없는 리눅스다 (WSL1 · systemd 를 끈 WSL2 · 컨테이너).
	ErrNoServiceManager = errors.New("사용할 수 있는 서비스 관리자가 없습니다")
	// ErrExecPathVolatile 은 실행 파일이 임시 디렉터리에 있다는 뜻이다 (`go run`).
	ErrExecPathVolatile = errors.New("실행 파일이 임시 디렉터리에 있어 등록할 수 없습니다")
)

// Options 는 Manager 구성이다.
type Options struct {
	// Env 는 필수다. HomeDir 가 비면 New 가 거부한다 — 이것이 단위 테스트가 개발자의
	// 진짜 ~/Library/LaunchAgents 로 새지 못하게 하는 **구조적** 보장이다.
	Env hostenv.Env

	// GOOS 는 백엔드 선택을 덮어쓴다. 비우면 Env.OS, 그것도 비면 runtime.GOOS.
	// ubuntu 러너가 darwin 백엔드를 fake Runner 로 끝까지 돌릴 수 있게 하는 레버다.
	GOOS string

	// UID 는 launchd 도메인(gui/<uid>)이다. 0 이면 os.Getuid().
	UID int

	// ExecPath 는 서비스 파일에 적을 실행 파일 경로다. 비우면 resolveExecPath 가 정한다.
	// 명시하면 휘발성 검사를 건너뛴다 — 패키저·CI 의 탈출구이자 테스트 seam 이다.
	ExecPath string

	// Args 는 실행 파일에 넘길 인자다. 비우면 ["daemon"].
	//
	// **기본값이 정확히 ["daemon"] 인 것이 중요하다.** --state·--data-dir 을 유닛에 구우면
	// state.json 의 위치가 두 곳에 존재하게 되고, installer.EnableLocal 이
	// state.Local.DataDir 를 바꾸는 순간 조용히 어긋난다. 나머지 기본값은 서비스 관리자
	// 아래서 전부 올바르게 풀린다(HOME 이 설정되므로 DefaultStatePath → state.Local → 기본값).
	// --listen 생략은 단지 허용 가능한 게 아니라 바람직하다 — FixedPort=false 라 부팅 시
	// 일시적 포트 충돌이 하드 실패(→ 재시작 루프)가 아니라 우아한 폴백이 된다.
	Args []string

	// Runner 는 서비스 관리자 CLI 호출 한 번이다. 비우면 execRunner.
	Runner Runner

	// LookPath·Stat 은 systemd 감지의 테스트 seam 이다. 비우면 exec.LookPath·os.Stat.
	// 이것이 없으면 리눅스 백엔드 테스트가 러너의 진짜 /run/systemd/system 에 좌우된다.
	LookPath func(string) (string, error)
	Stat     func(string) (os.FileInfo, error)
}

// goos 는 백엔드 선택에 쓸 GOOS 다. Options.GOOS > Env.OS > runtime.GOOS.
func (o Options) goos() string {
	if o.GOOS != "" {
		return o.GOOS
	}
	if o.Env.OS != "" {
		return o.Env.OS
	}
	return runtime.GOOS
}

func (o Options) lookPath(name string) (string, error) {
	if o.LookPath != nil {
		return o.LookPath(name)
	}
	return execLookPath(name)
}

func (o Options) stat(path string) (os.FileInfo, error) {
	if o.Stat != nil {
		return o.Stat(path)
	}
	return os.Stat(path)
}

// Result 는 Enable·Disable 이 실제로 한 일이다.
type Result struct {
	Kind     Kind
	UnitPath string
	// LogPath 는 launchd 의 표준 오류 로그다. systemd 는 journald 이므로 빈 문자열이다.
	LogPath string
	// AlreadyInState 는 할 일이 없었다는 뜻이다 (Disable 전용).
	AlreadyInState bool
	// ExecPath 는 서비스 파일에 적힌 실행 파일 경로다.
	ExecPath string
	// Notes 는 사용자에게 알려야 할 안내다. 실패가 아니다 — 로그 위치, linger 의미론,
	// macOS 로그인 항목 토글 같은 것이다.
	Notes []string
}

// Status 는 "설정된 의도" 와 "관측된 현실" 을 나란히 담는다.
//
// 분류할 수 없는 I/O 에만 오류를 낸다. "미등록"·"systemd 없음"·"launchctl 출력 파싱 실패"
// 는 전부 **상태**로 이 구조체에 담긴다. status 명령이 어떤 환경에서도 멈추면 안 되기
// 때문이다. Enable 만 비대칭이다 — 서비스 관리자가 없으면 반드시 하드 오류다.
type Status struct {
	Kind Kind
	// UnitPath 는 등록되지 않았어도 채운다. 사용자가 찾아볼 경로다.
	UnitPath string
	LogPath  string

	// Registered 는 우리 파일이 디스크에 있다는 뜻이다 (의도).
	Registered bool
	// Loaded 는 서비스 관리자가 이 job 을 알고 있다는 뜻이다 (현실).
	Loaded bool
	// Running 은 서비스 관리자가 보는 프로세스가 살아 있다는 뜻이다 (현실).
	//
	// 데몬 생존의 **권위 있는** 신호는 여기가 아니라 runtime.json + /healthz 다.
	// 이 값은 서비스 관리자의 시각일 뿐이고, launchctl 출력 형식은 릴리스마다 바뀐다.
	Running  bool
	PID      int
	LastExit int
	// Restarts 는 systemd NRestarts 다. launchd 는 채우지 않는다.
	Restarts int

	RegisteredExecPath string
	CurrentExecPath    string
	ExecPathDrift      bool
	// ExecPathMissing 은 등록된 경로에 파일이 없다는 뜻이다. 크래시 루프의 가장 흔한 원인이다.
	ExecPathMissing bool

	// Supported 가 false 면 이 호스트에서는 자동 실행을 등록할 수 없다. 사유는 Detail 에 있다.
	Supported bool
	// Detail 은 사람이 읽을 한 줄이다 (미지원 사유·파싱 실패·비정상 상태어).
	Detail string
}

// backend 는 플랫폼별 구현이다. New 가 GOOS 로 고른다.
type backend interface {
	kind() Kind
	unitPath() string
	logPath() string
	enable(ctx context.Context, execPath string, args []string) (Result, error)
	disable(ctx context.Context) (Result, error)
	status(ctx context.Context) (Status, error)
}

// Manager 는 이 호스트의 자동 실행 등록을 다룬다.
type Manager struct {
	opts Options
	be   backend
}

// New 는 이 호스트에 맞는 Manager 를 만든다.
//
// windows 는 ErrUnsupportedPlatform 을 감싼 오류다 (PROJ-56). 그 경우에도 파일을 쓰거나
// 외부 명령을 부르지 않는다.
func New(opts Options) (*Manager, error) {
	if opts.Env.HomeDir == "" {
		// 하드 오류다. 이것이 테스트가 진짜 홈으로 새는 것을 막는 구조적 보장이다.
		return nil, errors.New("autostart: Options.Env.HomeDir 가 필요합니다")
	}
	if opts.Runner == nil {
		opts.Runner = execRunner{}
	}
	if len(opts.Args) == 0 {
		opts.Args = []string{defaultDaemonSubcmd}
	}

	m := &Manager{opts: opts}
	switch goos := opts.goos(); goos {
	case "darwin":
		uid := opts.UID
		if uid == 0 {
			uid = os.Getuid()
		}
		m.be = &darwinBackend{opts: opts, uid: uid}
	case "linux":
		m.be = &linuxBackend{opts: opts}
	default:
		return nil, fmt.Errorf("%w (%s). Windows 자동 실행 등록은 후속 티켓입니다", ErrUnsupportedPlatform, goos)
	}
	return m, nil
}

func (m *Manager) Kind() Kind       { return m.be.kind() }
func (m *Manager) UnitPath() string { return m.be.unitPath() }
func (m *Manager) LogPath() string  { return m.be.logPath() }

// Enable 은 서비스를 등록하고 즉시 띄운다.
//
// 실행 파일이 임시 디렉터리에 있으면(`go run`) ErrExecPathVolatile 로 **거부한다.**
// 사라질 경로를 재시작 정책과 함께 등록하면 영구 크래시 루프가 되고, 그것은 거부보다
// 훨씬 나쁘다. Options.ExecPath 로 명시하면 이 검사를 건너뛴다.
func (m *Manager) Enable(ctx context.Context) (Result, error) {
	execPath, err := m.execPathForWrite()
	if err != nil {
		return Result{Kind: m.be.kind(), UnitPath: m.be.unitPath(), LogPath: m.be.logPath()}, err
	}
	return m.be.enable(ctx, execPath, m.opts.Args)
}

// Disable 은 서비스를 정지하고 등록을 해제한다.
// 등록된 적이 없으면 오류가 아니라 Result{AlreadyInState: true} 다.
func (m *Manager) Disable(ctx context.Context) (Result, error) { return m.be.disable(ctx) }

// Status 는 지금 상태를 관측한다. 분류할 수 없는 I/O 에만 오류를 낸다.
func (m *Manager) Status(ctx context.Context) (Status, error) {
	st, err := m.be.status(ctx)
	if err != nil {
		return st, err
	}
	// 현재 실행 파일 경로는 best-effort 다. 휘발성이어도 (`go run` 으로 status 를 부르는 것은
	// README 에 문서화된 개발 루프다) 드리프트 비교는 여전히 뜻이 있다.
	st.CurrentExecPath = m.execPathBestEffort()
	if st.RegisteredExecPath != "" {
		if st.CurrentExecPath != "" && st.CurrentExecPath != st.RegisteredExecPath {
			st.ExecPathDrift = true
		}
		if _, err := os.Stat(st.RegisteredExecPath); err != nil {
			st.ExecPathMissing = true
		}
	}
	return st, nil
}

// execPathForWrite 는 서비스 파일에 적을 경로를 정한다. 휘발성이면 오류다.
func (m *Manager) execPathForWrite() (string, error) {
	if m.opts.ExecPath != "" {
		return m.opts.ExecPath, nil
	}
	return resolveExecPath()
}

// execPathBestEffort 는 드리프트 비교용 현재 경로다. 휘발성 오류를 삼킨다.
func (m *Manager) execPathBestEffort() string {
	if m.opts.ExecPath != "" {
		return m.opts.ExecPath
	}
	p, _ := resolveExecPath()
	return p
}
