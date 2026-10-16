package autostart

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/your-org/pulsemetry/internal/hostenv"
)

// 로그 회전은 디렉터리 주입으로 어느 OS 에서든 단위 테스트된다.

func TestRotateLogs(t *testing.T) {
	t.Run("상한을 넘으면 사본을 만들고 원본을 비운다", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, logFileName)
		body := strings.Repeat("x", 100)
		mustWrite(t, path, []byte(body), 0o644)

		if err := rotateLogsIn(dir, 50); err != nil {
			t.Fatalf("rotateLogsIn: %v", err)
		}

		// copy-truncate 다. launchd 가 fd 를 잡고 있어 rename 은 여기서 깨진다 —
		// 이름만 바뀌고 데몬은 계속 같은 inode 에 쓴다.
		backup, err := os.ReadFile(path + ".1")
		if err != nil {
			t.Fatalf("사본이 없다: %v", err)
		}
		if string(backup) != body {
			t.Errorf("사본 내용이 다르다: %d바이트", len(backup))
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("원본이 사라졌다: %v", err)
		}
		if info.Size() != 0 {
			t.Errorf("원본 크기 = %d, want 0", info.Size())
		}
	})

	t.Run("상한 이하면 건드리지 않는다", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, logFileName)
		mustWrite(t, path, []byte("작은 로그"), 0o644)

		if err := rotateLogsIn(dir, MaxLogBytes); err != nil {
			t.Fatalf("rotateLogsIn: %v", err)
		}
		if _, err := os.Stat(path + ".1"); err == nil {
			t.Error("회전할 필요가 없는데 사본을 만들었다")
		}
		content, err := os.ReadFile(path)
		if err != nil || string(content) != "작은 로그" {
			t.Errorf("원본이 바뀌었다: %q (%v)", content, err)
		}
	})

	t.Run("파일이 없으면 오류가 아니다", func(t *testing.T) {
		// 데몬 첫 기동 직후의 정상 상태다.
		if err := rotateLogsIn(t.TempDir(), 10); err != nil {
			t.Fatalf("rotateLogsIn: %v", err)
		}
	})

	t.Run("두 파일을 모두 본다", func(t *testing.T) {
		dir := t.TempDir()
		for _, name := range []string{logFileName, errLogFileName} {
			mustWrite(t, filepath.Join(dir, name), []byte(strings.Repeat("y", 100)), 0o644)
		}
		if err := rotateLogsIn(dir, 50); err != nil {
			t.Fatalf("rotateLogsIn: %v", err)
		}
		for _, name := range []string{logFileName, errLogFileName} {
			if _, err := os.Stat(filepath.Join(dir, name+".1")); err != nil {
				t.Errorf("%s 사본이 없다: %v", name, err)
			}
		}
	})

	t.Run("사본은 하나만 보관한다", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, logFileName)

		mustWrite(t, path, []byte(strings.Repeat("1", 100)), 0o644)
		if err := rotateLogsIn(dir, 50); err != nil {
			t.Fatalf("첫 회전: %v", err)
		}
		mustWrite(t, path, []byte(strings.Repeat("2", 100)), 0o644)
		if err := rotateLogsIn(dir, 50); err != nil {
			t.Fatalf("둘째 회전: %v", err)
		}

		backup, err := os.ReadFile(path + ".1")
		if err != nil {
			t.Fatalf("사본 읽기: %v", err)
		}
		if !strings.HasPrefix(string(backup), "2") {
			t.Error("사본이 최신으로 갱신되지 않았다")
		}
		if _, err := os.Stat(path + ".2"); err == nil {
			t.Error(".2 를 만들었다 — 하나만 보관해야 한다")
		}
	})
}

func TestRotateLogs는LogDir이비면아무것도하지않는다(t *testing.T) {
	// 이 판단이 이 패키지 안에 있기 때문에 internal/daemon 이 플랫폼을 몰라도 된다.
	for _, goos := range []string{"linux", "windows"} {
		env := hostenv.Env{OS: goos, HomeDir: t.TempDir()}
		if err := RotateLogs(env); err != nil {
			t.Fatalf("%s: RotateLogs = %v, want nil", goos, err)
		}
	}
}
