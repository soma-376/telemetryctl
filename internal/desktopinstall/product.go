package desktopinstall

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func HelperPath(home string) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Applications", "Uninstall Pulsemetry.app", "Contents", "MacOS", "PulsemetryUninstall")
	case "windows":
		return filepath.Join(home, ".pulsemetry", "uninstaller", "PulsemetryUninstall.exe")
	default:
		return filepath.Join(home, ".local", "share", "pulsemetry", "PulsemetryUninstall.AppImage")
	}
}

func IsUninstaller() bool {
	exe, _ := os.Executable()
	if runtime.GOOS == "linux" && os.Getenv("APPIMAGE") != "" {
		exe = os.Getenv("APPIMAGE")
	}
	return strings.Contains(filepath.ToSlash(exe), "/Uninstall Pulsemetry.app/") || strings.HasPrefix(filepath.Base(exe), "PulsemetryUninstall.")
}

func copyProgram(src, dst string) error {
	hash, err := fingerprint(src)
	if err != nil {
		return err
	}
	if previous, _ := fingerprint(dst); previous == hash {
		return nil
	}
	if err = regularPath(dst); err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return err
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".pulsemetry-copy-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err = tmp.Chmod(0700); err == nil {
		_, err = tmp.Write(b)
	}
	closeErr := tmp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp.Name(), dst)
}

// RegisterGUI는 배포 위치의 GUI만 등록한다. 개발 작업 트리는 인수하지 않는다.
func RegisterGUI(force bool) (bool, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return false, err
	}
	exe, err := os.Executable()
	if err != nil {
		return false, err
	}
	source := exe
	if runtime.GOOS == "linux" {
		source = os.Getenv("APPIMAGE")
	}
	if !force {
		receipt, err := LoadReceipt(ReceiptPath(home))
		if err != nil {
			return false, err
		}
		if files, ok := receipt.Components["gui"]; ok {
			for _, f := range files {
				if f.Path == source {
					return true, nil
				}
			}
			// 다른 설치 경로의 기록을 자동으로 대체하지 않는다.
			return false, nil
		}
	}
	var files []string
	helper := HelperPath(home)
	var helperFiles []string
	switch runtime.GOOS {
	case "darwin":
		root := filepath.Dir(filepath.Dir(filepath.Dir(exe)))
		if filepath.Base(root) != "Pulsemetry.app" {
			return false, nil
		}
		if filepath.Dir(root) != filepath.Join(home, "Applications") && filepath.Dir(root) != "/Applications" {
			return false, nil
		}
		for _, rel := range []string{"Contents/MacOS/Pulsemetry", "Contents/Info.plist", "Contents/Resources/icons.icns", "Contents/Resources/Assets.car", "Contents/_CodeSignature/CodeResources"} {
			src := filepath.Join(root, filepath.FromSlash(rel))
			if _, err := os.Stat(src); os.IsNotExist(err) && (rel == "Contents/_CodeSignature/CodeResources" || rel == "Contents/Resources/Assets.car") {
				continue
			}
			if _, err := fingerprint(src); err != nil {
				return false, err
			}
			files = append(files, src)
		}
		helperRoot := filepath.Dir(filepath.Dir(filepath.Dir(helper)))
		packagedHelper := filepath.Join(root, "Contents", "Helpers", "Uninstall Pulsemetry.app")
		for _, rel := range []string{"Contents/MacOS/PulsemetryUninstall", "Contents/Info.plist", "Contents/Resources/icons.icns", "Contents/_CodeSignature/CodeResources"} {
			src := filepath.Join(packagedHelper, filepath.FromSlash(rel))
			if _, err := os.Stat(src); os.IsNotExist(err) && rel == "Contents/_CodeSignature/CodeResources" {
				continue
			}
			dst := filepath.Join(helperRoot, filepath.FromSlash(rel))
			if err := copyProgram(src, dst); err != nil {
				return false, err
			}
			files = append(files, src)
			helperFiles = append(helperFiles, dst)
		}
	case "windows":
		root := filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Pulsemetry")
		if os.Getenv("LOCALAPPDATA") == "" || !strings.EqualFold(exe, filepath.Join(root, "Pulsemetry.exe")) {
			return false, nil
		}
		files = []string{exe}
		shortcuts, err := guiShortcuts()
		if err != nil {
			return false, err
		}
		for _, shortcut := range shortcuts {
			if _, err := os.Stat(shortcut); err == nil {
				files = append(files, shortcut)
			}
		}
		if _, err := os.Stat(filepath.Join(root, "uninstall.exe")); err == nil {
			files = append(files, filepath.Join(root, "uninstall.exe"))
		}
		if err := copyProgram(exe, helper); err != nil {
			return false, err
		}
		helperFiles = []string{helper}
	default:
		image := os.Getenv("APPIMAGE")
		if image == "" || filepath.Base(image) != "Pulsemetry.AppImage" {
			return false, nil
		}
		if err := copyProgram(image, helper); err != nil {
			return false, err
		}
		files = []string{image}
		helperFiles = []string{helper}
	}
	receipt := ReceiptPath(home)
	if err := RegisterFiles(receipt, "gui", files); err != nil {
		return false, err
	}
	if err := RegisterFiles(receipt, "uninstaller", helperFiles); err != nil {
		return false, err
	}
	if err := registerLauncher(home, helper, source); err != nil {
		return false, err
	}
	return true, nil
}

func registerLauncher(home, helper, gui string) error {
	switch runtime.GOOS {
	case "windows":
		key := `HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\soma-376Pulsemetry`
		for _, v := range [][2]string{{"DisplayName", "Pulsemetry"}, {"UninstallString", `"` + helper + `" --uninstall`}, {"DisplayIcon", helper}, {"Publisher", "soma-376"}} {
			if out, err := exec.Command("reg.exe", "add", key, "/v", v[0], "/t", "REG_SZ", "/d", v[1], "/f").CombinedOutput(); err != nil {
				return fmt.Errorf("제거 등록 실패: %w (%s)", err, out)
			}
		}
	case "linux":
		var files []string
		for _, item := range [][3]string{{"pulsemetry", "Pulsemetry", gui}, {"pulsemetry-uninstall", "Uninstall Pulsemetry", helper}} {
			name := filepath.Join(home, ".local", "share", "applications", item[0]+".desktop")
			if err := regularPath(name); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
				return err
			}
			quoted := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`, "`", "\\`", "%", "%%").Replace(item[2])
			body := "[Desktop Entry]\nType=Application\nName=" + item[1] + "\nExec=\"" + quoted + "\"\nTerminal=false\nCategories=Utility;\n"
			if err := os.WriteFile(name, []byte(body), 0600); err != nil {
				return err
			}
			files = append(files, name)
		}
		return RegisterFiles(ReceiptPath(home), "launcher", files)
	}
	return nil
}

// RegisterCLI는 부트스트랩의 고정 사용자 경로에 설치된 바이너리만 관리한다.
func RegisterCLI() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	expected := filepath.Join(home, ".pulsemetry", "bin", "pulsemetry")
	if runtime.GOOS == "windows" {
		expected = filepath.Join(os.Getenv("LOCALAPPDATA"), "Pulsemetry", "bin", "pulsemetry.exe")
	}
	if exe != expected {
		return nil
	}
	return RegisterFiles(ReceiptPath(home), "cli", []string{exe})
}

// Finalize는 제품 설정 정리가 끝난 뒤 제거 도구와 OS 진입점만 정리한다.
func Finalize(home string) error {
	return finalize(home, unregisterLauncher)
}

func finalize(home string, unregister func() error) error {
	receipt := ReceiptPath(home)
	r, err := LoadReceipt(receipt)
	if err != nil {
		return err
	}
	// 바뀐 제거 도구는 일부만 지워 망가뜨리지 않는다.
	for _, component := range []string{"uninstaller", "launcher"} {
		for _, f := range r.Components[component] {
			hash, err := fingerprint(f.Path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			if hash != f.SHA256 {
				return fmt.Errorf("변경된 제거 도구를 보존했습니다: %s", f.Path)
			}
		}
	}
	if err := unregister(); err != nil {
		return err
	}
	if _, err := RemoveFiles(receipt, "uninstaller", "launcher"); err != nil {
		return err
	}
	// 앱 번들의 알려진 빈 디렉터리만 정리한다. 추가 파일이 있으면 os.Remove가 거부한다.
	for _, files := range r.Components {
		for _, f := range files {
			parent := filepath.Dir(f.Path)
			for i := 0; i < 3; i++ {
				if filepath.Base(parent) == "Applications" || filepath.Base(parent) == "Programs" || parent == home {
					break
				}
				if strings.Contains(filepath.ToSlash(parent), ".app/") || strings.HasSuffix(parent, ".app") {
					if err := os.Remove(parent); err != nil {
						break
					}
				} else {
					break
				}
				parent = filepath.Dir(parent)
			}
		}
	}
	remaining, err := LoadReceipt(receipt)
	if err != nil {
		return err
	}
	if len(remaining.Components) == 0 {
		return os.Remove(receipt)
	}
	return nil
}

func unregisterLauncher() error {
	if runtime.GOOS == "windows" {
		// reg delete가 없는 키에 대해 1을 반환하므로 조회 성공한 경우만 삭제한다.
		key := `HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\soma-376Pulsemetry`
		if err := exec.Command("reg.exe", "query", key).Run(); err == nil {
			if err := exec.Command("reg.exe", "delete", key, "/f").Run(); err != nil {
				return err
			}
		}
	}
	return nil
}
