// Package hostenv 는 실행 환경(OS/WSL)과 Codex·Claude Code 설정 파일 경로를 판별한다.
// Windows 와 WSL 은 서로 다른 홈 디렉터리를 쓰므로 (§5.1) 대상 파일을 잘못 잡으면
// telemetry 가 수집되지 않는다.
package hostenv

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type Env struct {
	OS      string // "windows" | "linux" | "darwin"
	IsWSL   bool
	HomeDir string
}

func Detect() (Env, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Env{}, err
	}
	return Env{
		OS:      runtime.GOOS,
		IsWSL:   isWSL(),
		HomeDir: home,
	}, nil
}

// isWSL 은 Linux 바이너리가 WSL 위에서 도는지 추정한다. Windows 네이티브에서는 항상 false.
func isWSL() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if v := os.Getenv("WSL_DISTRO_NAME"); v != "" {
		return true
	}
	b, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}
	v := strings.ToLower(string(b))
	return strings.Contains(v, "microsoft") || strings.Contains(v, "wsl")
}

// CodexConfigPath 는 Codex 의 config.toml 경로를 반환한다 (~/.codex/config.toml).
func (e Env) CodexConfigPath() string {
	return filepath.Join(e.HomeDir, ".codex", "config.toml")
}

// ClaudeSettingsPath 는 Claude Code 의 settings.json 경로를 반환한다 (~/.claude/settings.json).
func (e Env) ClaudeSettingsPath() string {
	return filepath.Join(e.HomeDir, ".claude", "settings.json")
}
