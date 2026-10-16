package desktopinstall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLinuxAppImageRegistrationAndRemoval(t *testing.T) {
	for _, name := range []string{"Pulsemetry.AppImage", "pulsemetry_gui_linux_amd64.AppImage", "pulsemetry_gui_linux_arm64.AppImage"} {
		for _, force := range []bool{false, true} {
			mode := map[bool]string{false: "first-launch", true: "bootstrap"}[force]
			t.Run(name+"/"+mode, func(t *testing.T) {
				home := testHome(t)
				image := filepath.Join(home, "Downloads", "release files", name)
				writeFixture(t, image, "AppImage fixture")
				extra := filepath.Join(filepath.Dir(image), "notes.txt")
				writeFixture(t, extra, "user file")
				t.Setenv("HOME", home)
				t.Setenv("APPIMAGE", image)
				installed, err := RegisterGUI(force)
				if err != nil || !installed {
					t.Fatalf("installed=%v err=%v", installed, err)
				}
				helper := HelperPath(home)
				if content, err := os.ReadFile(helper); err != nil || string(content) != "AppImage fixture" {
					t.Fatalf("independent helper: %q, %v", content, err)
				}
				launchers := []string{}
				for entry, target := range map[string]string{"pulsemetry": image, "pulsemetry-uninstall": helper} {
					launcher := filepath.Join(home, ".local", "share", "applications", entry+".desktop")
					content, err := os.ReadFile(launcher)
					if err != nil || !strings.Contains(string(content), "Exec=\""+target+"\"\n") {
						t.Fatalf("launcher %s: %q, %v", entry, content, err)
					}
					launchers = append(launchers, launcher)
				}
				receipt, err := LoadReceipt(ReceiptPath(home))
				if err != nil || len(receipt.Components["gui"]) != 1 || receipt.Components["gui"][0].Path != image {
					t.Fatalf("downloaded image not registered: %+v, %v", receipt, err)
				}
				result := Remove(context.Background(), home, Options{Stop: func(context.Context) error { return nil }})
				if !result.Success {
					t.Fatal(result.Error)
				}
				if _, err := os.Stat(image); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("AppImage remains: %v", err)
				}
				if _, err := os.Stat(helper); err != nil {
					t.Fatalf("helper removed before finalization: %v", err)
				}
				if err := Finalize(home); err != nil {
					t.Fatal(err)
				}
				for _, removed := range append(launchers, helper, ReceiptPath(home)) {
					if _, err := os.Stat(removed); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("not removed: %s: %v", removed, err)
					}
				}
				if content, err := os.ReadFile(extra); err != nil || string(content) != "user file" {
					t.Fatalf("user file changed: %q, %v", content, err)
				}
			})
		}
	}
}

func TestLinuxAppImageRegistrationIgnoresOtherFiles(t *testing.T) {
	for _, name := range []string{"", "Other.AppImage", "pulsemetry_gui_linux_386.AppImage", "pulsemetry_gui_linux_arm64.AppImage.backup", "PulsemetryUninstall.AppImage"} {
		t.Run(name, func(t *testing.T) {
			home := testHome(t)
			image := ""
			if name != "" {
				image = filepath.Join(home, name)
				writeFixture(t, image, "unmanaged file")
			}
			t.Setenv("HOME", home)
			t.Setenv("APPIMAGE", image)
			installed, err := RegisterGUI(false)
			if err != nil || installed {
				t.Fatalf("installed=%v err=%v", installed, err)
			}
			if _, err := os.Stat(ReceiptPath(home)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unexpected registration: %v", err)
			}
		})
	}
}
