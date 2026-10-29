package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"time"

	"github.com/your-org/pulsemetry/internal/guiinstall"
)

func cmdGUI(args []string) int { return runGUI(args, os.Stdout, os.Stderr) }

func runGUI(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || args[0] != "install" {
		fmt.Fprintln(stderr, "사용법: pulsemetry gui install")
		return 2
	}
	target, err := resolveLocalTarget("", "")
	if err != nil || target.StateErr != nil || target.State == nil {
		fmt.Fprintln(stderr, "GUI 설치 전에 pulsemetry enroll을 완료하세요.")
		return 1
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(stderr, "홈 디렉터리를 찾을 수 없습니다.")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	result, err := guiinstall.Install(ctx, guiinstall.Options{
		Platform: runtime.GOOS, Architecture: runtime.GOARCH, HomeDir: home,
		LocalAppData: os.Getenv("LOCALAPPDATA"), DataDir: target.DataDir, ServerURL: target.State.ServerURL,
		DesktopAvailable: runtime.GOOS != "linux" || os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != "",
		Ready:            func() bool { running, _ := daemonRunning(target.DataDir); return running },
	})
	if err != nil {
		fmt.Fprintln(stderr, "GUI 설치·실행 실패:", err)
		return 1
	}
	fmt.Fprintln(stdout, "GUI:", result.Path)
	if result.Opened {
		fmt.Fprintln(stdout, "데몬 준비를 확인하고 Pulsemetry를 열었습니다.")
	}
	if result.DeferredReason != "" {
		fmt.Fprintln(stdout, result.DeferredReason+". 준비된 후 pulsemetry gui install로 다시 시도할 수 있습니다.")
	}
	return 0
}
