package main

// `telemetryctl local enable [--port 4318] | disable` (PROJ-36 12단계).
//
// 이 명령이 이 저장소에서 유일하게 사용자의 기존 설정을 바꾸는 지점이다. 그래서 출력이
// 다른 명령보다 수다스럽다 — 무엇이 어떻게 바뀌었고, 왜 그렇게 바뀌었고, 지금 상태가
// 실제로 동작하는 상태인지까지 말한다. 특히 **데몬이 떠 있지 않은 채 재배선된 상태**는
// 텔레메트리가 통째로 사라지는 상태라(계획서 「리스크」 1행) 반드시 경고해야 한다.
// PROJ-55 부터는 경고에 그치지 않고 `telemetryctl autostart enable` 이라는 실제 해법을
// 안내한다 — 그리고 이미 등록돼 있다면 다른 조언을 준다 (warnDaemonNotRunning).

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/your-org/pulsemetry/internal/installer"
	"github.com/your-org/pulsemetry/internal/receiver"
	"github.com/your-org/pulsemetry/internal/runtimeinfo"
)

func cmdLocal(args []string) int { return runLocal(os.Stdout, os.Stderr, args) }

func runLocal(stdout, stderr io.Writer, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "오류: local 하위 명령이 필요합니다 (enable|disable)")
		return 2
	}
	sub := args[0]
	switch sub {
	case "enable", "disable":
	default:
		fmt.Fprintf(stderr, "오류: 알 수 없는 local 하위 명령 %q (enable|disable)\n", sub)
		return 2
	}

	fs := flag.NewFlagSet("local "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	statePath := fs.String("state", "", "설치 상태 파일 경로")
	dataDir := fs.String("data-dir", "", "데이터 디렉터리 (미지정 시 상태 파일 설정 → ~/.pulsemetry)")
	var port *int
	if sub == "enable" {
		port = fs.Int("port", 0, "로컬 수신 포트 (미지정 시 상태 파일 설정 → 4318)")
	}
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}

	target, err := resolveLocalTarget(*dataDir, *statePath)
	if err != nil {
		fmt.Fprintln(stderr, "오류:", err)
		return 1
	}
	// 상태 파일이 깨졌으면 진행하지 않는다. 조회 명령과 달리 이 명령은 상태를 **쓴다** —
	// 못 읽은 파일 위에 쓰면 설치 정보를 통째로 날린다.
	if target.StateErr != nil {
		fmt.Fprintln(stderr, "오류:", target.StateErr)
		return 1
	}
	if target.State == nil {
		fmt.Fprintf(stderr, "오류: pulsemetry 가 설치되어 있지 않습니다 (상태 파일 없음: %s)\n", target.StatePath)
		fmt.Fprintln(stderr, "      먼저 `telemetryctl enroll --invite <코드>` 를 실행하세요.")
		return 2
	}

	if sub == "disable" {
		return runLocalDisable(stdout, stderr, target)
	}
	return runLocalEnable(stdout, stderr, target, *port)
}

func runLocalEnable(stdout, stderr io.Writer, target localTarget, portFlag int) int {
	// grpc 거부는 installer 도 하지만 여기서 먼저 끊는다. 키링에 ingest 토큰을 만들기
	// 전에 실패해야 아무 흔적도 남지 않는다.
	if target.State.Manifest.OTLP.Protocol == "grpc" {
		fmt.Fprintln(stderr, "오류:", installer.ErrGRPCUnsupported)
		return 1
	}

	port, note := resolveEnablePort(target, portFlag)
	if note != "" {
		fmt.Fprintln(stdout, note)
	}

	token, err := receiver.EnsureToken()
	if err != nil {
		fmt.Fprintln(stderr, "오류: 로컬 ingest 토큰 확보 실패:", err)
		return 1
	}

	report, err := installer.EnableLocal(installer.LocalOptions{
		StatePath:   target.StatePath,
		Port:        port,
		IngestToken: token,
	})
	if err != nil {
		fmt.Fprintln(stderr, "로컬 재배선 실패:", err)
		return 1
	}

	fmt.Fprintf(stdout, "로컬 재배선 켬 · endpoint=%s\n", report.Endpoint)
	printLocalTargets(stdout, report)
	fmt.Fprintln(stdout, "  원문·tool details: 로컬 수집을 위해 강제로 켰습니다"+
		" (회사로는 manifest Privacy 기준으로 제거 후 전달되므로 상위로 나가는 데이터는 그대로입니다)")
	fmt.Fprintln(stdout, "  Authorization: 로컬 ingest 토큰 (OS 키링) — 회사 telemetry token 은 키링으로 대피시켰습니다")
	if !report.TelemetryTokenStashed {
		fmt.Fprintln(stdout, "  경고: 기존 설정에서 회사 telemetry token 을 찾지 못했습니다."+
			" `local disable` 이 되돌리지 못하면 `telemetryctl reconnect` 를 실행하세요.")
	}
	warnDaemonNotRunning(stdout, target.DataDir, currentAutostartHint())
	return 0
}

func runLocalDisable(stdout, stderr io.Writer, target localTarget) int {
	report, err := installer.DisableLocal(installer.LocalOptions{StatePath: target.StatePath})
	if err != nil {
		fmt.Fprintln(stderr, "로컬 재배선 해제 실패:", err)
		return 1
	}
	if report.AlreadyInState {
		fmt.Fprintf(stdout, "로컬 재배선은 이미 꺼져 있습니다 · endpoint=%s\n", report.Endpoint)
		return 0
	}
	fmt.Fprintf(stdout, "로컬 재배선 끔 · endpoint=%s\n", report.Endpoint)
	printLocalTargets(stdout, report)
	fmt.Fprintln(stdout, "  벤더 설정을 회사 manifest 그대로 되돌렸습니다 (원문·tool details 강제도 해제).")
	fmt.Fprintln(stdout, "  로컬 ingest 토큰은 키링에 남겨 둡니다 — 데몬이 계속 쓰고, 다시 켤 때 재사용합니다.")
	return 0
}

func printLocalTargets(w io.Writer, report *installer.LocalReport) {
	for _, t := range report.Targets {
		fmt.Fprintf(w, "  - %s\n", t.Path)
		if t.BackupPath != "" {
			fmt.Fprintf(w, "    백업: %s\n", t.BackupPath)
		}
		fmt.Fprintf(w, "    관리 키: %v\n", t.ManagedKeys)
	}
}

// resolveEnablePort 는 재배선이 가리킬 포트를 정한다.
//
// --port 를 명시하면 그대로 쓴다. 명시하지 않았고 **데몬이 실제로 다른 포트에서 듣고
// 있으면 그 포트를 쓴다.** 이것이 계획서가 말한 포트 폴백 재병합 경로다 — 데몬이 4318 을
// 잡지 못해 폴백하면 벤더 설정이 가리키는 주소가 어긋나는데, 데몬은 "재병합이 필요하다"고
// 로그만 남기고(internal/daemon/runner.go) 실제 재병합은 이 명령의 몫이다.
// 조용히 바꾸지 않고 무엇을 왜 골랐는지 출력한다.
func resolveEnablePort(target localTarget, portFlag int) (port int, note string) {
	if portFlag > 0 {
		return portFlag, ""
	}
	configured := target.State.Local.ListenPort
	if configured <= 0 {
		configured = installer.DefaultLocalPort
	}

	status, err := runtimeinfo.Load(runtimeinfo.PathIn(target.DataDir))
	if err != nil || !status.Found || status.Stale || status.Info.ListenPort <= 0 {
		return configured, ""
	}
	if status.Info.ListenPort == configured {
		return configured, ""
	}
	return status.Info.ListenPort, fmt.Sprintf(
		"참고: 데몬이 실제로 듣고 있는 포트 %d 를 씁니다 (설정값 %d 는 사용 중이라 폴백된 것으로 보입니다). "+
			"설정값을 강제하려면 --port %d 를 쓰세요.",
		status.Info.ListenPort, configured, configured)
}

// warnDaemonNotRunning 은 재배선했는데 받을 쪽이 없을 때 경고한다.
//
// 이 상태는 텔레메트리가 로컬에도 회사에도 남지 않는 상태다. 계획서 「리스크」 표 1행이
// 지목한 바로 그 위험이다.
//
// PROJ-45 로 배선이 opt-out 이 되면서 이 경고의 청중이 늘었다 — 이제 `local enable` 을
// 실행한 사람뿐 아니라 **enroll 한 모든 사람**이 이 상태를 지나간다. 그래서 localTarget
// 이 아니라 dataDir 만 받는다. enroll 직후에는 아직 localTarget 을 만들 수 없다.
//
// PROJ-55 부터 **자동 실행 힌트를 함께 받는다.** 등록돼 있는데도 데몬이 없는 상태와
// 아예 등록되지 않은 상태는 다음에 할 일이 완전히 다르다 — 전자는 기동에 실패한 것이라
// 볼 것이 로그이고, 후자는 등록하면 되는 것이다. 같은 문장을 주면 둘 중 한쪽은 늘 틀린다.
func warnDaemonNotRunning(w io.Writer, dataDir string, hint autostartHint) {
	running, detail := daemonRunning(dataDir)
	if running {
		fmt.Fprintln(w, "  데몬: 실행 중 (헬스체크 응답 확인)")
		return
	}
	fmt.Fprintf(w, "  경고: 데몬이 실행 중이 아닙니다 (%s).\n", detail)
	fmt.Fprintln(w, "        지금 상태로는 Claude Code·Codex 의 텔레메트리가 로컬에도 회사에도 남지 않습니다.")
	switch {
	case hint.Registered:
		fmt.Fprintln(w, "        자동 실행은 등록돼 있습니다 — 기동에 실패했을 수 있습니다.")
		printAutostartLogHint(w, hint.Kind, hint.LogPath)
		fmt.Fprintln(w, "        `telemetryctl autostart status` 로 등록 상태를 확인하세요.")
	case hint.Unsupported:
		fmt.Fprintln(w, "        이 환경은 자동 실행 등록을 지원하지 않습니다.")
		fmt.Fprintln(w, "        `telemetryctl daemon` 을 직접 실행하세요.")
	default:
		fmt.Fprintln(w, "        `telemetryctl autostart enable` 로 로그인 시 자동 실행을 등록하세요.")
		fmt.Fprintln(w, "        지금 바로 띄우려면 `telemetryctl daemon` 입니다.")
	}
	fmt.Fprintln(w, "        되돌리려면 `telemetryctl local disable` 입니다.")
}

// daemonRunning 은 runtime.json 과 /healthz 두 단계로 데몬 생존을 확인한다.
// pid 는 재사용되므로 runtime.json 만으로는 확정이 아니다 (runtimeinfo.Load 주석).
func daemonRunning(dataDir string) (bool, string) {
	status, err := runtimeinfo.Load(runtimeinfo.PathIn(dataDir))
	switch {
	case err != nil:
		return false, fmt.Sprintf("runtime.json 읽기 실패: %v", err)
	case !status.Found:
		return false, "runtime.json 없음"
	case status.Stale:
		return false, fmt.Sprintf("낡은 runtime.json — pid %d 가 살아 있지 않음", status.Info.PID)
	case strings.TrimSpace(status.Info.Endpoint) == "":
		return false, "runtime.json 에 endpoint 가 없음"
	}
	if _, err := probeHealth(status.Info.Endpoint); err != nil {
		return false, fmt.Sprintf("헬스체크 응답 없음: %v", err)
	}
	return true, ""
}
