package desktopinstall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/your-org/pulsemetry/internal/installer"
	"github.com/your-org/pulsemetry/internal/instancelock"
)

func guiDir(home string) string { return filepath.Join(home, ".pulsemetry", "gui-runtime") }

// AcquireGUI는 설치된 GUI와 제거 작업의 프로그램 파일 사용을 직렬화한다.
func AcquireGUI(home string) (*instancelock.Lock, error) {
	if err := regularPath(filepath.Join(guiDir(home), ".pulsemetry.lock")); err != nil {
		return nil, err
	}
	return instancelock.Acquire(guiDir(home))
}

func WatchRemoval(ctx context.Context, home string, quit func()) {
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := os.Stat(filepath.Join(guiDir(home), "stop")); err == nil {
				quit()
				return
			}
		}
	}
}

func stopGUI(ctx context.Context, home string) (*instancelock.Lock, error) {
	dir := guiDir(home)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	marker := filepath.Join(dir, "stop")
	if err := regularPath(marker); err != nil {
		return nil, err
	}
	if err := os.WriteFile(marker, []byte("uninstall\n"), 0600); err != nil {
		return nil, err
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		lock, err := AcquireGUI(home)
		if err == nil {
			return lock, nil
		}
		select {
		case <-ctx.Done():
			return nil, errors.New("Pulsemetry 창 종료를 확인하지 못했습니다. 앱을 종료하고 다시 시도하세요")
		case <-ticker.C:
		}
	}
}

func ClearRemovalRequest(home string) error {
	err := os.Remove(filepath.Join(guiDir(home), "stop"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

type Result struct {
	Success   bool     `json:"success"`
	Error     string   `json:"error,omitempty"`
	Warnings  []string `json:"warnings"`
	Preserved []string `json:"preserved"`
}

func Remove(ctx context.Context, home string, opts Options) Result {
	result := Result{Warnings: []string{}, Preserved: []string{}}
	// 파일 기록 손상은 설정을 변경하기 전에 발견한다.
	receipt := ReceiptPath(home)
	if _, err := LoadReceipt(receipt); err != nil {
		result.Error = err.Error()
		return result
	}
	if opts.StatePath == "" {
		opts.StatePath = filepath.Join(home, ".pulsemetry", "state.json")
	}
	if opts.DataDir == "" {
		opts.DataDir = filepath.Join(home, ".pulsemetry")
		state, err := installer.LoadState(opts.StatePath)
		if err != nil {
			result.Error = err.Error()
			return result
		}
		if state != nil && state.Local.DataDir != "" {
			opts.DataDir = state.Local.DataDir
		}
	}
	if opts.Stop == nil {
		opts.Stop = func(ctx context.Context) error { return Stop(ctx, opts.StatePath, opts.DataDir) }
	}
	plan, preview, err := Prepare(opts)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Warnings = preview.Warnings
	stopCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	lock, err := stopGUI(stopCtx, home)
	defer ClearRemovalRequest(home)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer lock.Close()
	if plan.AlreadyRemoved {
		if err = opts.Stop(ctx); err != nil {
			result.Error = err.Error()
			return result
		}
	}
	if err = plan.Execute(ctx); err != nil {
		result.Error = err.Error()
		return result
	}
	dataLease, err := instancelock.Acquire(opts.DataDir)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer dataLease.Close()
	result.Preserved, err = RemoveFiles(receipt, "gui", "cli")
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Success = true
	return result
}
