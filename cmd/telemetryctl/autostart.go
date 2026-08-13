package main

// `telemetryctl autostart enable | disable | status` (PROJ-55).
//
// 이 명령이 없던 동안 저장소가 할 수 있는 일은 "데몬을 직접 띄우세요" 라고 경고하는 것뿐이었다
// (local.go 의 warnDaemonNotRunning). PROJ-45 가 배선을 opt-out 으로 바꾸면서 그 경고를
// 지나가는 사람이 enroll 한 전원으로 늘었고, 그것이 이 티켓의 이유다.
//
// 하위 명령 파싱은 local.go 의 모양을 따른다 — 두 명령이 같은 인상을 주는 것이 낫다.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/your-org/pulsemetry/internal/autostart"
	"github.com/your-org/pulsemetry/internal/hostenv"
	"github.com/your-org/pulsemetry/internal/installer"
	"github.com/your-org/pulsemetry/internal/runtimeinfo"
	"github.com/your-org/pulsemetry/internal/store"
)

const (
	// autostartChangeTimeout 은 등록·해제 한 번의 상한이다. 조회용 autostartTimeout(2초,
	// statuslocal.go)보다 훨씬 넉넉한 이유는 `systemctl enable --now` 가 실제로 서비스를
	// 띄우고 launchd bootstrap 이 키체인 프롬프트를 띄울 수 있기 때문이다.
	autostartChangeTimeout = 30 * time.Second
)

// 등록 직후 데몬이 실제로 떴는지 확인하는 폴링이다.
//
// **이 폴링이 이 기능을 신뢰할 수 있게 만든다.** 두 서비스 관리자 모두 등록과 동시에
// 데몬을 띄우지만(RunAtLoad=true + bootstrap, enable --now) 바인딩과 runtime.json
// 기록에 수백 ms 가 걸린다. 확인하지 않으면 키체인 프롬프트·Secret Service 실패 같은
// 진짜 실패를 사용자가 조치할 수 있는 시점에 놓치고, 반대로 완전히 성공한 등록에서도
// 곧바로 "데몬이 실행 중이 아닙니다" 경고를 찍게 된다.
//
// const 가 아니라 var 인 것은 테스트가 줄이기 위해서다 — 데몬이 뜰 리 없는 테스트가
// 매번 5초를 기다리면 CLI 테스트 전체가 느려진다.
var (
	autostartWaitTimeout  = 5 * time.Second
	autostartWaitInterval = 500 * time.Millisecond
)

// managerFactory 는 테스트 seam 이다. autostart.Runner 가 공개 인터페이스라
// 테스트가 진짜 launchctl·systemctl 없이 이 명령 전체를 돌릴 수 있다.
type managerFactory func(execPath string, args []string) (*autostart.Manager, error)

func defaultManagerFactory(execPath string, args []string) (*autostart.Manager, error) {
	env, err := hostenv.Detect()
	if err != nil {
		return nil, fmt.Errorf("실행 환경 판별 실패: %w", err)
	}
	return autostart.New(autostart.Options{Env: env, ExecPath: execPath, Args: args})
}

func cmdAutostart(args []string) int {
	return runAutostart(os.Stdout, os.Stderr, args, defaultManagerFactory)
}

func runAutostart(stdout, stderr io.Writer, args []string, newManager managerFactory) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "오류: autostart 하위 명령이 필요합니다 (enable|disable|status)")
		return 2
	}
	sub := args[0]
	switch sub {
	case "enable", "disable", "status":
	default:
		fmt.Fprintf(stderr, "오류: 알 수 없는 autostart 하위 명령 %q (enable|disable|status)\n", sub)
		return 2
	}

	fs := flag.NewFlagSet("autostart "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	statePath := fs.String("state", "", "설치 상태 파일 경로")
	dataDir := fs.String("data-dir", "", "데이터 디렉터리 (미지정 시 상태 파일 설정 → ~/.pulsemetry)")
	execPath := fs.String("exec-path", "", "서비스에 등록할 telemetryctl 절대 경로 (enable 전용)")
	force := fs.Bool("force", false, "데몬이 이미 실행 중이어도 등록을 진행 (enable 전용)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}

	target, err := resolveLocalTarget(*dataDir, *statePath)
	if err != nil {
		fmt.Fprintln(stderr, "오류:", err)
		return 1
	}
	// 상태 파일이 깨진 것은 알리되 막지는 않는다. 자동 실행 등록은 state.json 을 **쓰지
	// 않으므로**(internal/autostart 패키지 주석 3번) 설치가 깨져 있어도 등록·해제·조회가
	// 동작해야 한다.
	warnStateErr(stderr, target)

	switch sub {
	case "status":
		return runAutostartStatus(stdout, stderr, newManager)
	case "disable":
		return runAutostartDisable(stdout, stderr, newManager)
	default:
		return runAutostartEnable(stdout, stderr, target, newManager, *execPath, *force)
	}
}

func runAutostartEnable(stdout, stderr io.Writer, target localTarget, newManager managerFactory, execPath string, force bool) int {
	execPath = strings.TrimSpace(execPath)
	if execPath != "" && !filepath.IsAbs(execPath) {
		fmt.Fprintf(stderr, "오류: --exec-path 는 절대 경로여야 합니다: %q\n", execPath)
		return 2
	}

	// **어떤 파일을 쓰기 전에** 이미 도는 데몬을 확인한다. 단순 포트 충돌보다 나쁘다:
	// 두 번째 데몬은 FixedPort=false 라 임의 포트로 조용히 폴백하고, 두 데몬이 같은
	// runtime.json 을 쓰며(마지막 writer 가 이긴다), `local enable` 이 그 임의 포트로
	// 벤더 설정을 재배선하고, 한 SQLite 파일에 writer 가 둘이 된다.
	//
	// 이 체크를 internal/autostart 가 아니라 여기에 두는 이유는 그 패키지가 receiver·HTTP
	// 의존을 갖지 않게 유지하기 위해서다 (statuslocal.go 가 healthResponse 를 재선언한 것과 같은 이유).
	if running, _ := daemonRunning(target.DataDir); running && !force {
		pid, endpoint := runningDaemonCoords(target.DataDir)
		fmt.Fprintf(stderr, "오류: 이미 데몬이 실행 중입니다 (pid %d · %s).\n", pid, orDash(endpoint))
		fmt.Fprintln(stderr, "      지금 자동 실행을 등록하면 두 번째 데몬이 떠서 포트가 폴백되고 runtime.json 이 충돌합니다.")
		fmt.Fprintf(stderr, "      실행 중인 데몬을 먼저 종료(Ctrl-C 또는 kill %d)한 뒤 다시 실행하세요.\n", pid)
		fmt.Fprintln(stderr, "      그래도 진행하려면 --force 입니다.")
		return 2
	}

	m, err := newManager(execPath, autostartArgs(target))
	if err != nil {
		printAutostartSetupError(stderr, err)
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), autostartChangeTimeout)
	defer cancel()
	res, err := m.Enable(ctx)
	if err != nil {
		fmt.Fprintln(stderr, "자동 실행 등록 실패:", err)
		printAutostartRemedy(stderr, err)
		return 1
	}

	fmt.Fprintf(stdout, "자동 실행 등록 완료 · %s\n", res.Kind)
	fmt.Fprintf(stdout, "  등록 파일: %s\n", res.UnitPath)
	fmt.Fprintf(stdout, "  실행 파일: %s\n", res.ExecPath)
	for _, note := range res.Notes {
		fmt.Fprintf(stdout, "  %s\n", note)
	}
	fmt.Fprintln(stdout, "  되돌리려면 `telemetryctl autostart disable` 입니다.")

	if waitForDaemon(target.DataDir) {
		fmt.Fprintln(stdout, "  데몬: 실행 중 (헬스체크 응답 확인)")
		return 0
	}
	fmt.Fprintf(stdout, "  경고: %s 안에 데몬이 응답하지 않았습니다.\n", autostartWaitTimeout)
	fmt.Fprintln(stdout, "        등록은 됐으므로 잠시 뒤 `telemetryctl status` 로 다시 확인하세요.")
	printAutostartLogHint(stdout, res.Kind, res.LogPath)
	return 0
}

func runAutostartDisable(stdout, stderr io.Writer, newManager managerFactory) int {
	m, err := newManager("", nil)
	if err != nil {
		printAutostartSetupError(stderr, err)
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), autostartChangeTimeout)
	defer cancel()
	res, err := m.Disable(ctx)
	if err != nil {
		fmt.Fprintln(stderr, "자동 실행 등록 해제 실패:", err)
		return 1
	}
	if res.AlreadyInState {
		// "없음은 오류가 아니다" — installer.DisableLocal 의 AlreadyInState 와 같은 규칙이다.
		fmt.Fprintf(stdout, "자동 실행은 이미 등록돼 있지 않습니다 (%s)\n", res.UnitPath)
		return 0
	}
	fmt.Fprintf(stdout, "자동 실행 등록 해제 완료 · %s\n", res.Kind)
	fmt.Fprintf(stdout, "  지운 파일: %s\n", res.UnitPath)
	for _, note := range res.Notes {
		fmt.Fprintf(stdout, "  %s\n", note)
	}
	fmt.Fprintln(stdout, "  다음 로그인부터는 데몬이 자동으로 뜨지 않습니다.")
	return 0
}

func runAutostartStatus(stdout, stderr io.Writer, newManager managerFactory) int {
	m, err := newManager("", nil)
	if err != nil {
		// status 는 진단 명령이라 미지원 환경에서도 종료 코드 0 이다 (statuslocal.go 의 규칙).
		if errors.Is(err, autostart.ErrUnsupportedPlatform) {
			fmt.Fprintln(stdout, "자동 실행: 지원하지 않는 환경 —", err)
			return 0
		}
		fmt.Fprintln(stderr, "오류:", err)
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), autostartTimeout)
	defer cancel()
	st, err := m.Status(ctx)
	if err != nil {
		fmt.Fprintln(stderr, "자동 실행 상태 조회 실패:", err)
		return 1
	}

	fmt.Fprintln(stdout, autostartSummary(st))
	fmt.Fprintf(stdout, "  등록 파일: %s\n", st.UnitPath)
	if st.LogPath != "" {
		fmt.Fprintf(stdout, "  로그: %s\n", st.LogPath)
	}
	if st.Kind == autostart.KindSystemdUser && st.Supported {
		fmt.Fprintf(stdout, "  로그: %s\n", autostart.JournalCommand)
	}
	if st.RegisteredExecPath != "" {
		fmt.Fprintf(stdout, "  등록된 실행 파일: %s\n", st.RegisteredExecPath)
	}
	if st.CurrentExecPath != "" {
		fmt.Fprintf(stdout, "  지금 실행 파일: %s\n", st.CurrentExecPath)
	}
	if st.Registered && (st.PID > 0 || st.LastExit != 0 || st.Restarts > 0) {
		fmt.Fprintf(stdout, "  서비스 관리자 관측: pid %d · 마지막 종료 코드 %d · 재시작 %d회\n",
			st.PID, st.LastExit, st.Restarts)
	}
	return 0
}

// autostartSummary 는 status 한 줄이다. statuslocal.go 의 블록도 같은 문장을 쓴다 —
// 두 곳이 다른 말을 하면 사용자가 어느 쪽을 믿어야 할지 모른다.
func autostartSummary(st autostart.Status) string {
	switch {
	case !st.Supported && !st.Registered:
		return "자동 실행: 지원하지 않는 환경 (" + orDash(st.Detail) + ")"
	case !st.Registered:
		return "자동 실행: 등록 안 됨 — `telemetryctl autostart enable` 로 등록하세요"
	}

	line := fmt.Sprintf("자동 실행: 등록됨 (%s · %s", st.Kind, autostartLabelFor(st.Kind))
	switch {
	case st.Running && st.PID > 0:
		line += fmt.Sprintf(" · 로드됨 · pid %d)", st.PID)
	case st.Loaded:
		line += " · 로드됨)"
	default:
		line += " · 로드 안 됨)"
	}
	switch {
	case st.ExecPathMissing:
		// 크래시 루프의 가장 흔한 원인이다. 드리프트보다 먼저 말한다.
		line += " · 경고: 등록된 실행 파일이 존재하지 않습니다"
	case st.ExecPathDrift:
		line += " · 경고: 등록된 실행 파일이 지금 바이너리와 다릅니다"
	}
	if st.Detail != "" {
		line += " · " + st.Detail
	}
	return line
}

func autostartLabelFor(kind autostart.Kind) string {
	if kind == autostart.KindSystemdUser {
		return autostart.UnitName
	}
	return autostart.Label
}

// autostartArgs 는 유닛에 구울 데몬 인자를 만든다.
//
// **기본은 ["daemon"] 뿐이다.** --state·--data-dir 을 유닛에 구우면 state.json 의 위치가
// 두 곳에 존재하게 되고, installer.EnableLocal 이 state.Local.DataDir 를 바꾸는 순간
// 조용히 어긋난다. 그래서 **기본값과 다를 때만** 굽고, 상태 파일이 이미 같은 값을 담고
// 있으면 굽지 않는다 — 진실원을 하나로 유지한다.
func autostartArgs(target localTarget) []string {
	args := []string{"daemon"}

	if def, err := installer.DefaultStatePath(); target.StatePath != "" && (err != nil || target.StatePath != def) {
		args = append(args, "--state", target.StatePath)
	}
	if target.State == nil || target.State.Local.DataDir != target.DataDir {
		if def, err := defaultDataDir(); err != nil || target.DataDir != def {
			args = append(args, "--data-dir", target.DataDir)
		}
	}
	return args
}

func defaultDataDir() (string, error) {
	env, err := hostenv.Detect()
	if err != nil {
		return "", err
	}
	return store.DefaultDataDir(env), nil
}

// runningDaemonCoords 는 preflight 메시지에 실을 pid 와 endpoint 다.
// 값을 못 읽어도 preflight 판정은 daemonRunning 이 이미 내렸으므로 0·"" 로 진행한다.
func runningDaemonCoords(dataDir string) (pid int, endpoint string) {
	st, err := runtimeinfo.Load(runtimeinfo.PathIn(dataDir))
	if err != nil || !st.Found {
		return 0, ""
	}
	return st.Info.PID, st.Info.Endpoint
}

// waitForDaemon 은 등록 직후 데몬이 뜰 때까지 짧게 기다린다. 상한이 고정이고
// 절대 치명적이지 않다 — 실패해도 등록 자체는 이미 끝났다.
func waitForDaemon(dataDir string) bool {
	deadline := time.Now().Add(autostartWaitTimeout)
	for {
		if running, _ := daemonRunning(dataDir); running {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(autostartWaitInterval)
	}
}

// currentAutostartStatus 는 이 호스트의 자동 실행 상태를 한 번 조회한다.
// status 블록(statuslocal.go)과 데몬 미실행 경고(local.go)가 함께 쓴다.
func currentAutostartStatus() (autostart.Status, error) {
	m, err := defaultManagerFactory("", nil)
	if err != nil {
		return autostart.Status{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), autostartTimeout)
	defer cancel()
	return m.Status(ctx)
}

// autostartHint 는 데몬이 떠 있지 않을 때 줄 조언을 가른다.
//
// 자동 실행이 **등록돼 있는데도** 데몬이 없으면 "`telemetryctl daemon` 을 실행하세요" 는
// 틀린 조언이다 — 등록은 됐는데 기동에 실패한 것이므로 볼 것은 로그다. 조언이 완전히
// 갈리기 때문에 warnDaemonNotRunning 이 이 값을 받는다.
type autostartHint struct {
	Registered bool
	Kind       autostart.Kind
	LogPath    string
	// Unsupported 는 이 호스트에서 자동 실행을 등록할 수 없다는 뜻이다 (windows·비-systemd).
	Unsupported bool
}

// currentAutostartHint 는 조회 실패를 삼킨다. 이 값의 유일한 쓰임이 "조언을 고르는 것"
// 이라서, 못 읽었으면 가장 무난한 조언(등록을 권한다)으로 떨어지는 편이 낫다.
func currentAutostartHint() autostartHint {
	st, err := currentAutostartStatus()
	if err != nil {
		return autostartHint{Unsupported: errors.Is(err, autostart.ErrUnsupportedPlatform)}
	}
	return autostartHint{
		Registered:  st.Registered,
		Kind:        st.Kind,
		LogPath:     st.LogPath,
		Unsupported: !st.Supported && !st.Registered,
	}
}

// printAutostartSetupError 는 Manager 를 만들지 못한 이유를 알린다.
func printAutostartSetupError(stderr io.Writer, err error) {
	if errors.Is(err, autostart.ErrUnsupportedPlatform) {
		// 실패한 것이 없다. Windows 사용자에게 무서운 어조를 쓰지 않는다.
		fmt.Fprintln(stderr, "지원하지 않는 환경:", err)
		return
	}
	fmt.Fprintln(stderr, "오류:", err)
}

// printAutostartRemedy 는 센티널별로 다음에 할 일을 안내한다. 사유마다 고치는 방법이
// 완전히 다르므로 한 문장으로 뭉뚱그리지 않는다.
func printAutostartRemedy(stderr io.Writer, err error) {
	switch {
	case errors.Is(err, autostart.ErrExecPathVolatile):
		// README 에 문서화된 개발 루프(`go run ./cmd/telemetryctl …`)가 여기 걸린다.
		// `go run` 을 명시적으로 지목하지 않으면 모든 개발자가 버그로 신고한다.
		fmt.Fprintln(stderr, "      `go run` 으로 실행하면 바이너리가 임시 디렉터리에 있어 등록할 수 없습니다.")
		fmt.Fprintln(stderr, "      사라질 경로를 등록하면 재시작 정책 아래 영구 크래시 루프가 됩니다.")
		fmt.Fprintln(stderr, "      `go build -o dist/telemetryctl ./cmd/telemetryctl` 후 그 파일로 실행하거나,")
		fmt.Fprintln(stderr, "      `--exec-path <설치된 절대 경로>` 를 지정하세요.")
	case errors.Is(err, autostart.ErrNoServiceManager):
		fmt.Fprintln(stderr, "      자동 실행 없이도 `telemetryctl daemon` 을 직접 띄우면 수집은 정상 동작합니다.")
	}
}

func printAutostartLogHint(w io.Writer, kind autostart.Kind, logPath string) {
	if kind == autostart.KindSystemdUser {
		fmt.Fprintf(w, "        로그: %s\n", autostart.JournalCommand)
		return
	}
	if logPath != "" {
		fmt.Fprintf(w, "        로그: %s\n", logPath)
	}
}
