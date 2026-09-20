package codexapp

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveExecutable(t *testing.T) {
	for _, tc := range []struct {
		name, goos, path, want          string
		lookupErr, validateErr, wantErr error
	}{
		{name: "PATH 우선", goos: "darwin", path: "/custom/codex", want: "/custom/codex"},
		{name: "macOS 기본 경로", goos: "darwin", lookupErr: exec.ErrNotFound, want: "/opt/homebrew/bin/codex"},
		{name: "Linux 기본 경로 탐색 안 함", goos: "linux", lookupErr: exec.ErrNotFound, wantErr: exec.ErrNotFound},
		{name: "Windows 기본 경로 탐색 안 함", goos: "windows", lookupErr: exec.ErrNotFound, wantErr: exec.ErrNotFound},
		{name: "권한 오류 유지", goos: "darwin", lookupErr: os.ErrPermission, wantErr: os.ErrPermission},
		{name: "현재 디렉터리 실행 거부 유지", goos: "darwin", lookupErr: exec.ErrDot, wantErr: exec.ErrDot},
		{name: "모든 후보 없음", goos: "darwin", lookupErr: exec.ErrNotFound, validateErr: os.ErrNotExist, wantErr: exec.ErrNotFound},
		{name: "후보 실행 권한 없음", goos: "darwin", lookupErr: exec.ErrNotFound, validateErr: os.ErrPermission, wantErr: os.ErrPermission},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveExecutableFor(tc.goos, func(string) (string, error) { return tc.path, tc.lookupErr }, func(path string) (string, error) { return path, tc.validateErr })
			if got != tc.want || !errors.Is(err, tc.wantErr) {
				t.Fatalf("got (%q, %v), want (%q, %v)", got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestResolveExecutableIntelFallback(t *testing.T) {
	got, err := resolveExecutableFor("darwin", func(string) (string, error) { return "", exec.ErrNotFound }, func(path string) (string, error) {
		if path == "/opt/homebrew/bin/codex" {
			return "", os.ErrNotExist
		}
		return path, nil
	})
	if err != nil || got != "/usr/local/bin/codex" {
		t.Fatalf("got (%q, %v)", got, err)
	}
}

func TestExecutablePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "codex")
	if err := os.WriteFile(path, []byte("test"), 0700); err != nil {
		t.Fatal(err)
	}
	got, err := executablePath(path)
	if err != nil || !filepath.IsAbs(got) {
		t.Fatalf("got (%q, %v)", got, err)
	}
	if _, err := executablePath(dir); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("directory: %v", err)
	}
	if _, err := executablePath(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing: %v", err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := executablePath(path); !errors.Is(err, os.ErrPermission) {
			t.Fatalf("permission: %v", err)
		}
	}
}
