package guiinstall

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setup(t *testing.T) Options {
	t.Helper()
	dir := t.TempDir()
	return Options{Platform: "linux", Architecture: "arm64", HomeDir: dir, DataDir: filepath.Join(dir, "data"), DesktopAvailable: true, Ready: func() bool { return true }, Launch: func(string) error { return nil }}
}

func TestInstallDownloadsThenWaitsAndOpensOnlyOnce(t *testing.T) {
	o := setup(t)
	requests, launches, checks := 0, 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/base/bin/pulsemetry_gui_linux_arm64.AppImage" {
			t.Errorf("경로: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte("appimage"))
	}))
	defer server.Close()
	o.ServerURL = server.URL + "/base"
	o.Ready = func() bool { checks++; return checks >= 2 }
	o.Launch = func(path string) error {
		launches++
		b, err := os.ReadFile(path)
		if err != nil || string(b) != "appimage" {
			t.Fatal("설치 전에 실행함")
		}
		return nil
	}
	result, err := Install(context.Background(), o)
	if err != nil || !result.Installed || !result.Opened || checks < 2 {
		t.Fatalf("최초 설치: %+v %v", result, err)
	}
	fi, _ := os.Stat(result.Path)
	if fi.Mode().Perm() != 0o755 {
		t.Fatal("AppImage 실행 권한 없음")
	}
	result, err = Install(context.Background(), o)
	if err != nil || result.Installed || result.Opened || requests != 1 || launches != 1 {
		t.Fatalf("중복 설치/실행: %+v %v", result, err)
	}
}

func TestReadinessAndLaunchFailureRemainRetryable(t *testing.T) {
	o := setup(t)
	target := filepath.Join(o.HomeDir, ".local/share/pulsemetry/Pulsemetry.AppImage")
	_ = os.MkdirAll(filepath.Dir(target), 0o755)
	_ = os.WriteFile(target, []byte("existing"), 0o755)
	o.Ready = func() bool { return false }
	o.WaitTimeout = time.Millisecond
	launches := 0
	o.Launch = func(string) error { launches++; return errors.New("failed") }
	result, err := Install(context.Background(), o)
	if err != nil || result.DeferredReason == "" || launches != 0 {
		t.Fatalf("준비 전 실행: %+v %v", result, err)
	}
	o.Ready = func() bool { return true }
	if _, err = Install(context.Background(), o); err == nil {
		t.Fatal("실행 실패 누락")
	}
	o.Launch = func(string) error { launches++; return nil }
	result, err = Install(context.Background(), o)
	if err != nil || !result.Opened || launches != 2 {
		t.Fatalf("실패 뒤 재시도: %+v %v", result, err)
	}
}

func TestDownloadFailureDoesNotLaunchOrInstall(t *testing.T) {
	for _, status := range []int{302, 404, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			o := setup(t)
			launches := 0
			o.Launch = func(string) error { launches++; return nil }
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "/other")
				w.WriteHeader(status)
			}))
			defer server.Close()
			o.ServerURL = server.URL
			result, err := Install(context.Background(), o)
			if err == nil || launches != 0 || result.Installed {
				t.Fatalf("실패한 다운로드 실행: %+v %v", result, err)
			}
		})
	}
}

func TestHeadlessInstallDoesNotConsumeFirstLaunch(t *testing.T) {
	o := setup(t)
	o.DesktopAvailable = false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("app")) }))
	defer server.Close()
	o.ServerURL = server.URL
	result, err := Install(context.Background(), o)
	if err != nil || !result.Installed || result.Opened || result.DeferredReason == "" {
		t.Fatalf("헤드리스: %+v %v", result, err)
	}
	o.DesktopAvailable = true
	result, err = Install(context.Background(), o)
	if err != nil || !result.Opened {
		t.Fatalf("이후 첫 실행: %+v %v", result, err)
	}
}

func TestMacCopiesBundleAndDetachesOnFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		o := setup(t)
		o.Platform = "darwin"
		temp := t.TempDir()
		target := filepath.Join(temp, "Applications/Pulsemetry.app")
		detached := false
		o.Run = func(ctx context.Context, name string, args ...string) error {
			if name == "hdiutil" && args[0] == "detach" {
				detached = true
				return nil
			}
			if name == "ditto" {
				if fail {
					return errors.New("copy failed")
				}
				executable := filepath.Join(args[1], "Contents/MacOS/Pulsemetry")
				_ = os.MkdirAll(filepath.Dir(executable), 0o755)
				return os.WriteFile(executable, []byte("app"), 0o755)
			}
			return nil
		}
		err := installPackage(context.Background(), o, "package.dmg", temp, target)
		if (err != nil) != fail || !detached {
			t.Fatalf("마운트 정리: err=%v detached=%v", err, detached)
		}
	}
}

func TestWindowsUsesSilentUserInstaller(t *testing.T) {
	o := setup(t)
	o.Platform = "windows"
	called := false
	o.Run = func(_ context.Context, name string, args ...string) error {
		called = true
		if name != "installer.exe" || strings.Join(args, " ") != "/S" {
			t.Fatal("설치 명령", name, args)
		}
		return nil
	}
	if err := installPackage(context.Background(), o, "installer.exe", t.TempDir(), filepath.Join(t.TempDir(), "Pulsemetry.exe")); err != nil || !called {
		t.Fatal(err)
	}
}

// 실제 패키지 검증은 명시된 로컬 DMG만 쓰며 앱 실행은 대체한다.
func TestNativeMacPackageInstallation(t *testing.T) {
	packagePath := os.Getenv("PULSEMETRY_TEST_GUI_PACKAGE")
	if packagePath == "" {
		t.Skip("실제 DMG 경로를 지정하면 실행")
	}
	o := setup(t)
	o.Platform = "darwin"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, packagePath) }))
	defer server.Close()
	o.ServerURL = server.URL
	opened := false
	o.Launch = func(path string) error {
		opened = true
		if !strings.HasSuffix(path, "/Applications/Pulsemetry.app") {
			t.Fatal(path)
		}
		return nil
	}
	result, err := Install(context.Background(), o)
	if err != nil || !result.Installed || !result.Opened || !opened {
		t.Fatalf("실제 패키지 설치: %+v %v", result, err)
	}
}

func TestMacLaunchDelegatesToSingleInstance(t *testing.T) {
	o := setup(t)
	o.Platform = "darwin"
	executable := filepath.Join(o.HomeDir, "Applications/Pulsemetry.app/Contents/MacOS/Pulsemetry")
	_ = os.MkdirAll(filepath.Dir(executable), 0o755)
	_ = os.WriteFile(executable, []byte("installed"), 0o755)
	o.Launch = nil
	called := false
	o.Run = func(_ context.Context, name string, args ...string) error {
		called = true
		if name != "open" || len(args) != 2 || args[0] != "-n" || args[1] != filepath.Join(o.HomeDir, "Applications/Pulsemetry.app") {
			t.Fatalf("재실행: %s %v", name, args)
		}
		return nil
	}
	result, err := Install(context.Background(), o)
	if err != nil || !result.Opened || !called {
		t.Fatalf("단일 인스턴스 열기: %+v %v", result, err)
	}
}
