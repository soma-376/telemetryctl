// Package guiinstall은 사용자 권한으로 GUI를 설치하고 준비된 데몬에 연결할 첫 창을 연다.
package guiinstall

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/your-org/pulsemetry/internal/config"
	"github.com/your-org/pulsemetry/internal/instancelock"
)

const MaxDownloadBytes = 512 << 20

// Options의 함수들은 OS를 변경하지 않는 설치 흐름 테스트의 경계다.
type Options struct {
	Platform, Architecture, HomeDir, LocalAppData, DataDir, ServerURL string
	DesktopAvailable                                                  bool
	Ready                                                             func() bool
	HTTP                                                              *http.Client
	Run                                                               func(context.Context, string, ...string) error
	Launch                                                            func(string) error
	WaitTimeout                                                       time.Duration
}

type Result struct {
	Path              string
	Installed, Opened bool
	DeferredReason    string
}

func AssetName(platform, arch string) (string, error) {
	ext, ok := map[string]string{"darwin": ".dmg", "windows": ".exe", "linux": ".AppImage"}[platform]
	if !ok || (arch != "amd64" && arch != "arm64") {
		return "", errors.New("지원하지 않는 GUI 설치 대상")
	}
	return "pulsemetry_gui_" + platform + "_" + arch + ext, nil
}

func Install(ctx context.Context, o Options) (result Result, err error) {
	asset, err := AssetName(o.Platform, o.Architecture)
	if err != nil {
		return result, err
	}
	if o.Ready == nil || !filepath.IsAbs(o.HomeDir) || !filepath.IsAbs(o.DataDir) {
		return result, errors.New("GUI 설치 경로 또는 준비 확인이 없음")
	}
	switch o.Platform {
	case "darwin":
		result.Path = filepath.Join(o.HomeDir, "Applications", "Pulsemetry.app")
	case "windows":
		if !filepath.IsAbs(o.LocalAppData) {
			return result, errors.New("LOCALAPPDATA 경로가 없음")
		}
		result.Path = filepath.Join(o.LocalAppData, "Programs", "Pulsemetry", "Pulsemetry.exe")
	case "linux":
		result.Path = filepath.Join(o.HomeDir, ".local", "share", "pulsemetry", "Pulsemetry.AppImage")
	}
	markerDir := filepath.Join(o.DataDir, "gui")
	lock, err := instancelock.Acquire(markerDir)
	if err != nil {
		return result, err
	}
	defer lock.Close()
	if o.Run == nil {
		o.Run = func(ctx context.Context, name string, args ...string) error {
			return exec.CommandContext(ctx, name, args...).Run()
		}
	}
	if o.Launch == nil {
		o.Launch = func(path string) error {
			if o.Platform == "darwin" {
				// 재실행은 Wails 단일 인스턴스 처리로 보내 메인 창만 복원한다.
				return o.Run(ctx, "open", "-n", path)
			}
			// GUI 프로세스는 설치 명령의 종료·컨텍스트 취소 뒤에도 유지한다.
			cmd := exec.Command(path)
			if err := cmd.Start(); err != nil {
				return err
			}
			return cmd.Process.Release()
		}
	}
	executable := result.Path
	if o.Platform == "darwin" {
		executable = filepath.Join(result.Path, "Contents", "MacOS", "Pulsemetry")
	}
	if _, err = os.Stat(executable); os.IsNotExist(err) {
		temp, err := os.MkdirTemp("", "pulsemetry-gui-")
		if err != nil {
			return result, err
		}
		defer os.RemoveAll(temp)
		packagePath := filepath.Join(temp, asset)
		if err = download(ctx, o, asset, packagePath); err != nil {
			return result, err
		}
		if err = installPackage(ctx, o, packagePath, temp, result.Path); err != nil {
			return result, err
		}
		if fi, statErr := os.Stat(executable); statErr != nil || !fi.Mode().IsRegular() {
			return result, errors.New("GUI 설치 후 실행 파일을 찾을 수 없음")
		}
		result.Installed = true
	} else if err != nil {
		return result, err
	}
	marker := filepath.Join(markerDir, "opened")
	if _, err := os.Stat(marker); err == nil {
		return result, nil
	} else if !os.IsNotExist(err) {
		return result, err
	}
	if !o.DesktopAvailable {
		result.DeferredReason = "데스크톱 세션이 없어 자동 실행을 건너뜁니다"
		return result, nil
	}
	if o.WaitTimeout == 0 {
		o.WaitTimeout = 15 * time.Second
	}
	readyCtx, cancel := context.WithTimeout(ctx, o.WaitTimeout)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for !o.Ready() {
		select {
		case <-readyCtx.Done():
			result.DeferredReason = "데몬이 아직 준비되지 않아 GUI 자동 실행을 보류합니다"
			return result, nil
		case <-ticker.C:
		}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := o.Launch(result.Path); err != nil {
		return result, errors.New("GUI 실행 실패 — 수집 데몬은 계속 실행됩니다")
	}
	result.Opened = true
	if err := config.AtomicWriteFile(marker, []byte("opened\n"), 0o600); err != nil {
		return result, fmt.Errorf("GUI 실행 기록 저장 실패: %w", err)
	}
	return result, nil
}

func download(ctx context.Context, o Options, asset, destination string) error {
	u, err := url.Parse(o.ServerURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		(u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"))) {
		return errors.New("GUI 다운로드 서버 주소가 올바르지 않음")
	}
	client := http.Client{Timeout: 5 * time.Minute}
	if o.HTTP != nil {
		client = *o.HTTP
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.JoinPath("bin", asset).String(), nil)
	if err != nil {
		return errors.New("GUI 다운로드 요청 생성 실패")
	}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("GUI 다운로드 연결 실패")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GUI 다운로드 실패 (HTTP %d)", resp.StatusCode)
	}
	if resp.ContentLength > MaxDownloadBytes {
		return errors.New("GUI 패키지가 크기 제한을 초과함")
	}
	f, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	n, copyErr := io.Copy(f, io.LimitReader(resp.Body, MaxDownloadBytes+1))
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil || n == 0 || n > MaxDownloadBytes {
		return errors.New("GUI 패키지 다운로드가 완료되지 않음")
	}
	return nil
}

func installPackage(ctx context.Context, o Options, source, temp, target string) (err error) {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	switch o.Platform {
	case "darwin":
		mount := filepath.Join(temp, "mount")
		if err := os.Mkdir(mount, 0o700); err != nil {
			return err
		}
		if err := o.Run(ctx, "hdiutil", "attach", "-readonly", "-nobrowse", "-mountpoint", mount, source); err != nil {
			return errors.New("GUI 디스크 이미지 열기 실패")
		}
		// 취소된 설치 컨텍스트로 정리를 시도하지 않는다.
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if detachErr := o.Run(cleanup, "hdiutil", "detach", mount); detachErr != nil {
				err = errors.Join(err, errors.New("GUI 디스크 이미지 해제 실패"))
			}
		}()
		staging, err := os.MkdirTemp(filepath.Dir(target), ".pulsemetry-install-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(staging)
		stagedApp := filepath.Join(staging, "Pulsemetry.app")
		if err := o.Run(ctx, "ditto", filepath.Join(mount, "Pulsemetry.app"), stagedApp); err != nil {
			return errors.New("GUI 앱 복사 실패")
		}
		return os.Rename(stagedApp, target)
	case "windows":
		// 릴리스는 사용자 범위 NSIS다. 기본 경로가 LOCALAPPDATA/Programs/Pulsemetry다.
		if err := o.Run(ctx, source, "/S"); err != nil {
			return errors.New("GUI 설치 프로그램 실패")
		}
		return nil
	default:
		if err := copyExecutable(source, target); err != nil {
			return err
		}
		// freedesktop Exec는 셸이 아니다. 예약 문자는 따옴표 안에서도 이스케이프한다.
		escaped := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "`", "\\`", "$", "\\$", "%", "%%").Replace(target)
		desktop := "[Desktop Entry]\nType=Application\nName=Pulsemetry\nExec=\"" + escaped + "\"\nTerminal=false\nCategories=Utility;\n"
		return config.AtomicWriteFile(filepath.Join(o.HomeDir, ".local", "share", "applications", "pulsemetry.desktop"), []byte(desktop), 0o644)
	}
}

// copyExecutable는 큰 AppImage를 메모리에 적재하지 않고 완성된 파일만 공개한다.
func copyExecutable(source, target string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	staged, err := os.CreateTemp(filepath.Dir(target), ".pulsemetry-")
	if err != nil {
		return err
	}
	defer os.Remove(staged.Name())
	_, copyErr := io.Copy(staged, input)
	syncErr := staged.Sync()
	closeErr := staged.Close()
	if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
		return err
	}
	if err := os.Chmod(staged.Name(), 0o755); err != nil {
		return err
	}
	return os.Rename(staged.Name(), target)
}
