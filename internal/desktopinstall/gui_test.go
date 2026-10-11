package desktopinstall

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/credential"
	"github.com/your-org/pulsemetry/internal/installer"
)

func TestRemovalWaitsForRunningGUIBeforeCleanup(t *testing.T) {
	home, opts, files := guiRemovalFixture(t)
	lease, err := AcquireGUI(home)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	requested, release, exited := make(chan struct{}), make(chan struct{}), make(chan struct{})
	watchDone, closeErr := make(chan struct{}), make(chan error, 1)
	var releaseOnce sync.Once
	releaseGUI := func() { releaseOnce.Do(func() { close(release) }) }
	go func() {
		defer close(watchDone)
		WatchRemoval(ctx, home, func() {
			close(requested)
			select {
			case <-release:
				close(exited)
				closeErr <- lease.Close()
			case <-ctx.Done():
			}
		})
	}()
	defer func() {
		cancel()
		releaseGUI()
		<-watchDone
		_ = lease.Close()
	}()
	stopped := make(chan struct{})
	opts.Stop = func(context.Context) error {
		select {
		case <-exited:
		default:
			return errors.New("GUI 종료 전에 데몬 정리를 시작했습니다")
		}
		for name, expected := range files {
			if content, err := os.ReadFile(name); err != nil || string(content) != expected {
				return fmt.Errorf("데몬 종료 전에 파일이 변경됐습니다: %s", name)
			}
		}
		close(stopped)
		return nil
	}
	credentials := 0
	opts.DeleteCredential = func(credential.Account) error {
		select {
		case <-stopped:
		default:
			return errors.New("데몬 종료 전에 자격증명을 삭제했습니다")
		}
		credentials++
		return nil
	}
	result := make(chan Result, 1)
	go func() { result <- Remove(ctx, home, opts) }()
	select {
	case <-requested:
	case got := <-result:
		t.Fatalf("GUI 종료 요청 전에 제거가 끝났습니다: %+v", got)
	case <-ctx.Done():
		t.Fatal("GUI 종료 요청을 받지 못했습니다")
	}
	select {
	case <-stopped:
		t.Fatal("실행 중인 GUI의 잠금을 기다리지 않았습니다")
	case got := <-result:
		t.Fatalf("GUI 종료 전에 제거가 끝났습니다: %+v", got)
	default:
	}
	assertGUIRemovalFiles(t, files)
	releaseGUI()
	select {
	case got := <-result:
		if !got.Success {
			t.Fatalf("GUI 종료 후 제거 실패: %+v", got)
		}
	case <-ctx.Done():
		t.Fatal("GUI 종료 후 제거가 끝나지 않았습니다")
	}
	if err := <-closeErr; err != nil {
		t.Fatal(err)
	}
	if credentials != 4 {
		t.Fatalf("자격증명 정리 횟수: %d", credentials)
	}
	for name := range files {
		if name == ReceiptPath(home) {
			continue
		}
		if _, err := os.Stat(name); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("제거되지 않은 파일: %s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(guiDir(home), "stop")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("종료 요청이 남았습니다: %v", err)
	}
}

func TestRemovalCancellationPreservesFilesWhileGUIIsRunning(t *testing.T) {
	home, opts, files := guiRemovalFixture(t)
	lease, err := AcquireGUI(home)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopped, credentials := false, false
	opts.Stop = func(context.Context) error { stopped = true; return nil }
	opts.DeleteCredential = func(credential.Account) error { credentials = true; return nil }
	result := make(chan Result, 1)
	go func() { result <- Remove(ctx, home, opts) }()
	marker := filepath.Join(guiDir(home), "stop")
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		select {
		case got := <-result:
			t.Fatalf("GUI 잠금 대기 전에 제거가 끝났습니다: %+v", got)
		case <-deadline.C:
			t.Fatal("GUI 종료 요청이 기록되지 않았습니다")
		case <-tick.C:
		}
	}
	cancel()
	select {
	case got := <-result:
		if got.Success || got.Error == "" {
			t.Fatalf("실행 중인 GUI를 두고 제거가 성공했습니다: %+v", got)
		}
	case <-deadline.C:
		t.Fatal("GUI 종료 대기 취소가 반영되지 않았습니다")
	}
	if stopped || credentials {
		t.Fatalf("GUI 종료 실패 뒤 정리를 진행했습니다: stop=%v credentials=%v", stopped, credentials)
	}
	assertGUIRemovalFiles(t, files)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("취소 뒤 종료 요청이 남았습니다: %v", err)
	}
}

func guiRemovalFixture(t *testing.T) (string, Options, map[string]string) {
	t.Helper()
	home := testHome(t)
	state, data := filepath.Join(home, "state.json"), filepath.Join(home, "data")
	program := filepath.Join(home, "program")
	files := map[string]string{
		state:                                `{"state_schema_version":5,"installation_id":"test-installation","local":{"enabled":false,"store_content":true}}`,
		installer.ManagedPath(state):         `{"version":1,"installation_id":"test-installation","targets":[]}`,
		filepath.Join(data, "pulsemetry.db"): "fake database",
		program:                              "program",
	}
	for name, content := range files {
		writeFixture(t, name, content)
	}
	if err := RegisterFiles(ReceiptPath(home), "gui", []string{program}); err != nil {
		t.Fatal(err)
	}
	receipt, err := os.ReadFile(ReceiptPath(home))
	if err != nil {
		t.Fatal(err)
	}
	files[ReceiptPath(home)] = string(receipt)
	return home, Options{StatePath: state, DataDir: data, DeleteData: true}, files
}

func assertGUIRemovalFiles(t *testing.T, files map[string]string) {
	t.Helper()
	for name, expected := range files {
		content, err := os.ReadFile(name)
		if err != nil || string(content) != expected {
			t.Fatalf("GUI 종료 전에 파일이 변경됐습니다: %s: %v", name, err)
		}
	}
}
