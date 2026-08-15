package autostart

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/your-org/pulsemetry/internal/hostenv"
)

const (
	// Label 은 launchd job 레이블이자 plist 파일 이름의 어간이다 (파일명 = <Label>.plist).
	//
	// `.daemon` 접미사는 미래의 GUI 로그인 항목(com.your-org.pulsemetry.gui, ADR 0004)을
	// 위한 자리다. 지금 접미사를 빼면 나중에 레이블을 바꿔야 하고, 레이블이 바뀌면 기존
	// 설치의 등록이 고아가 된다.
	Label = "com.your-org.pulsemetry.daemon"

	// UnitName 은 systemd user unit 파일 이름이다.
	UnitName = "pulsemetry-daemon.service"

	// logFileName·errLogFileName 은 launchd 가 리다이렉트할 파일이다.
	//
	// 데몬 로거는 stdout 으로만 쓰고(cmd/telemetryctl/main.go 의 log.New(os.Stdout,…)),
	// stderr 에는 "daemon 실패:" 한 줄만 나간다. 두 파일을 나누면 err 쪽이 **순수한 크래시
	// 진단 파일**이 되어 사용자에게 "이 파일을 보라" 고 말할 수 있다.
	logFileName    = "daemon.log"
	errLogFileName = "daemon.err.log"
)

// LaunchAgentPath 는 macOS LaunchAgent plist 경로다.
//
// 시스템 수준(/Library/LaunchAgents·LaunchDaemons)이 아니라 사용자 홈이다 — 패키지
// 주석 「1. 사용자 수준 서비스만 쓴다」 참조.
func LaunchAgentPath(env hostenv.Env) string {
	return filepath.Join(env.HomeDir, "Library", "LaunchAgents", Label+".plist")
}

// UnitPath 는 systemd user unit 경로다.
//
// **$XDG_CONFIG_HOME 존중은 선택이 아니다.** 이 저장소는 데이터·상태 경로에 XDG 를 쓰지
// 않지만(store.DefaultDataDir 는 ~/.pulsemetry 고정), systemd **자신**이 사용자 유닛을
// 이 변수로 해석한다. 무시하면 그 변수가 설정된 장비에서 systemd 가 영원히 읽지 않을
// 파일을 쓰게 되고, 등록은 성공했는데 아무 일도 일어나지 않는다.
// 같은 성격의 예외 선례: installer.BackupDir 의 $LOCALAPPDATA 특수 처리.
func UnitPath(env hostenv.Env) string {
	if x := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); x != "" {
		return filepath.Join(x, "systemd", "user", UnitName)
	}
	return filepath.Join(env.HomeDir, ".config", "systemd", "user", UnitName)
}

// LogDir 는 자동 실행 로그 디렉터리다. **빈 문자열은 "로그 파일을 우리가 관리하지
// 않는다"** 는 뜻이다 — 리눅스는 journald 가, windows 는 아직 아무도 관리하지 않는다.
//
// installer.BackupDir 와 같은 모양의 런타임 switch 다. darwin 에서 ~/Library/Logs 를
// 쓰는 것은 Mac 사용자와 Console.app 이 보는 곳이기 때문이고, installer.BackupDir 가
// darwin 에서 ~/Library/Application Support 를 쓰는 것과 같은 결이다.
func LogDir(env hostenv.Env) string {
	switch goosOf(env) {
	case "darwin":
		return filepath.Join(env.HomeDir, "Library", "Logs", "pulsemetry")
	default:
		return ""
	}
}

// LogPath·ErrLogPath 는 LogDir 이 빈 경우 빈 문자열을 준다.
func LogPath(env hostenv.Env) string    { return logFileIn(LogDir(env), logFileName) }
func ErrLogPath(env hostenv.Env) string { return logFileIn(LogDir(env), errLogFileName) }

func logFileIn(dir, name string) string {
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, name)
}

// goosOf 는 Env.OS 를 쓰되 비어 있으면 runtime.GOOS 로 떨어진다.
// hostenv.Detect 는 항상 OS 를 채우므로 운영 경로에서는 언제나 Env.OS 다.
// 비는 것은 테스트가 Env{HomeDir: t.TempDir()} 만 채웠을 때다.
func goosOf(env hostenv.Env) string {
	if env.OS != "" {
		return env.OS
	}
	return runtime.GOOS
}
