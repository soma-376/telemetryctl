package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/your-org/pulsemetry/internal/desktopinstall"

	"github.com/wailsapp/wails/v3/pkg/application"
)

type Removal struct {
	enabled bool
	app     *application.App
	mu      sync.Mutex
	busy    bool
	done    bool
}

func (r *Removal) Preview(deleteData bool) (desktopinstall.Preview, error) {
	if !r.enabled {
		return desktopinstall.Preview{}, fmt.Errorf("제거 전용 창에서만 사용할 수 있습니다")
	}
	_, p, err := desktopinstall.Prepare(desktopinstall.Options{DeleteData: deleteData})
	return p, err
}

func (a *App) OpenUninstaller() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if _, err = os.Stat(desktopinstall.HelperPath(home)); err != nil {
		return fmt.Errorf("설치된 제거 도구가 없습니다. 정식 설치 파일로 Pulsemetry를 다시 설치하세요: %w", err)
	}
	cmd := exec.Command(desktopinstall.HelperPath(home), "--uninstall")
	if err = cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	a.Quit()
	return nil
}

func (r *Removal) Execute(deleteData bool) (desktopinstall.Result, error) {
	r.mu.Lock()
	if r.busy || r.done {
		r.mu.Unlock()
		return desktopinstall.Result{}, fmt.Errorf("이미 제거를 실행했거나 실행 중입니다")
	}
	r.busy = true
	r.mu.Unlock()
	defer func() { r.mu.Lock(); r.busy = false; r.mu.Unlock() }()
	if !r.enabled {
		return desktopinstall.Result{}, fmt.Errorf("제거 전용 창에서만 사용할 수 있습니다")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return desktopinstall.Result{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	args := []string{"--uninstall-cleanup"}
	if deleteData {
		args = append(args, "--delete-data")
	}
	cmd := exec.CommandContext(ctx, desktopinstall.HelperPath(home), args...)
	output, err := cmd.Output()
	if err != nil {
		return desktopinstall.Result{}, fmt.Errorf("제거 도구 실행 실패: %w", err)
	}
	var result desktopinstall.Result
	if err = json.Unmarshal(output, &result); err != nil {
		return result, fmt.Errorf("제거 결과를 확인할 수 없습니다: %w", err)
	}
	r.mu.Lock()
	r.done = result.Success
	r.mu.Unlock()
	return result, nil
}

// removalCommand는 Wails 창을 만들기 전에 작업 프로세스 모드를 처리한다.
func removalCommand() bool {
	if len(os.Args) < 2 {
		return false
	}
	mode := os.Args[1]
	if mode == "--uninstall-finalize" {
		defer cleanupFinalizer()
		_, _ = io.Copy(io.Discard, os.Stdin)
		home, err := os.UserHomeDir()
		if err != nil {
			os.Exit(1)
		}
		for i := 0; i < 30; i++ {
			err = desktopinstall.Finalize(home)
			if err == nil {
				return true
			}
			time.Sleep(100 * time.Millisecond)
		}
		_ = os.WriteFile(filepath.Join(home, ".pulsemetry", "uninstall-error.txt"), []byte(err.Error()), 0600)
		return true
	}
	if mode != "--uninstall-cleanup" && mode != "--register-product" {
		return false
	}
	if mode == "--register-product" {
		installed, err := desktopinstall.RegisterGUI(true)
		if err != nil || !installed {
			fmt.Fprintln(os.Stderr, "설치 등록 실패:", err)
			os.Exit(1)
		}
		return true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		_ = json.NewEncoder(os.Stdout).Encode(desktopinstall.Result{Error: err.Error()})
		return true
	}
	deleteData := len(os.Args) == 3 && os.Args[2] == "--delete-data"
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	result := desktopinstall.Remove(ctx, home, desktopinstall.Options{DeleteData: deleteData})
	_ = json.NewEncoder(os.Stdout).Encode(result)
	return true
}

// 실행 파일 복사는 파일 경로를 셸로 해석하지 않는다.
func copyRemovalExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	closeErr := out.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func removalExecutable() (string, error) {
	if runtime.GOOS == "linux" && os.Getenv("APPIMAGE") != "" {
		return os.Getenv("APPIMAGE"), nil
	}
	return os.Executable()
}

// Finish는 성공한 제거 창이 닫힌 뒤에만 자기 파일을 지우도록 작업을 넘긴다.
func (r *Removal) Finish() error {
	r.mu.Lock()
	quit := false
	defer func() {
		r.mu.Unlock()
		if quit {
			r.app.Quit()
		}
	}()
	if !r.enabled || !r.done || r.busy {
		return fmt.Errorf("설치 정리를 먼저 완료해야 합니다")
	}
	src, err := removalExecutable()
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "pulsemetry-uninstall-")
	if err != nil {
		return err
	}
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	if runtime.GOOS == "linux" {
		ext = ".AppImage"
	}
	target := filepath.Join(dir, "finish"+ext)
	if err = copyRemovalExecutable(src, target); err != nil {
		_ = os.RemoveAll(dir)
		return err
	}
	cmd := exec.Command(target, "--uninstall-finalize")
	pipe, err := cmd.StdinPipe()
	if err != nil {
		_ = os.RemoveAll(dir)
		return err
	}
	if err = cmd.Start(); err != nil {
		_ = pipe.Close()
		_ = os.RemoveAll(dir)
		return err
	}
	// 부모 프로세스 종료 시 파이프가 닫힌다. Finalize는 그때까지 기다린다.
	go func() { _ = cmd.Wait(); _ = pipe.Close() }()
	r.busy = true
	quit = true
	return nil
}
