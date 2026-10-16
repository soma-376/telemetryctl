package config

import (
	"encoding/json"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func TestCodexHookCommandsAcrossPlatforms(t *testing.T) {
	for _, tt := range []struct{ goos, executable, dataDir, want string }{
		{"windows", `C:\Program Files\사용자 O'Brien\$tools\pulsemetry.exe`, `C:\Data\O'Brien $cache`, `& 'C:\Program Files\사용자 O''Brien\$tools\pulsemetry.exe' hook codex --data-dir 'C:\Data\O''Brien $cache'`},
		{"linux", `/opt/O'Brien/$tools/pulsemetry`, `/home/사용자/.pulsemetry`, `'/opt/O'"'"'Brien/$tools/pulsemetry' hook codex --data-dir '/home/사용자/.pulsemetry'`},
		{"darwin", `/Applications/Pulsemetry App/bin/pulsemetry`, `/Users/O'Brien/.pulsemetry`, `'/Applications/Pulsemetry App/bin/pulsemetry' hook codex --data-dir '/Users/O'"'"'Brien/.pulsemetry'`},
	} {
		t.Run(tt.goos, func(t *testing.T) {
			got := renderCodexHookCommand(tt.executable, tt.dataDir, tt.goos)
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
			if !sameCodexHook(map[string]any{"type": "command", "command": got}, "different-install-path") {
				t.Fatal("이전 설치 훅을 식별하지 못함")
			}
		})
	}
}

func TestCodexHookLegacyOwnership(t *testing.T) {
	for _, command := range []string{
		`pulsemetry hook codex`,
		`"C:\Old Install\pulsemetry.exe" hook codex`,
		`& "C:\Old Install\pulsemetry.exe" hook codex`,
		`& 'C:\Old Install\pulsemetry.exe' hook codex --data-dir 'C:\Data'`,
		`'/opt/old/pulsemetry' hook codex`,
	} {
		if !sameCodexHook(map[string]any{"type": "command", "command": command}, "new-command") {
			t.Errorf("구버전 명령 누락: %s", command)
		}
	}
	for _, command := range []string{`& 'C:\Tools\custom.exe' hook codex`, `& 'C:\Tools\pulsemetry.exe' status`} {
		if sameCodexHook(map[string]any{"type": "command", "command": command}, "new-command") {
			t.Errorf("사용자 명령을 소유함: %s", command)
		}
	}
}

func TestCodexHookUpgradeAndRemovalAcrossPlatforms(t *testing.T) {
	for _, goos := range []string{"windows", "linux", "darwin"} {
		t.Run(goos, func(t *testing.T) {
			old := renderCodexHookCommand("/old/pulsemetry", "", goos)
			updated := renderCodexHookCommand("/new/pulsemetry", "/data", goos)
			root := map[string]any{"hooks": map[string]any{
				"SessionStart": []map[string]any{{"hooks": []map[string]any{
					{"type": "command", "command": old},
					{"type": "command", "command": "user-hook"},
				}}},
			}}
			mergeCodexHooks(root, true, updated)
			mergeCodexHooks(root, true, updated)
			groups := root["hooks"].(map[string]any)["SessionStart"].([]map[string]any)
			if len(groups) != 2 {
				t.Fatalf("재등록 후 중복/유실: %#v", groups)
			}
			mergeCodexHooks(root, false, updated)
			hooks := root["hooks"].(map[string]any)
			groups = hooks["SessionStart"].([]map[string]any)
			if len(groups) != 1 || groups[0]["hooks"].([]map[string]any)[0]["command"] != "user-hook" {
				t.Fatalf("사용자 훅 보존 실패: %#v", groups)
			}
			if _, exists := hooks["SessionEnd"]; exists {
				t.Fatal("종료 훅 잔재")
			}
		})
	}
}

// 현재 OS의 실제 셸에 특수문자를 전달해 확장·분할되지 않는지 확인한다.
func TestHookArgumentRoundTripInNativeShell(t *testing.T) {
	value := "space 한글 O'Brien $HOME `echo` $(echo injected); & end"
	var command *exec.Cmd
	if runtime.GOOS == "windows" {
		command = exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
			"[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new(); ConvertTo-Json -Compress -InputObject "+quoteHookArg(value, "windows"))
	} else {
		command = exec.Command("sh", "-c", "printf '%s' "+quoteHookArg(value, runtime.GOOS))
	}
	out, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if runtime.GOOS == "windows" {
		if err := json.Unmarshal([]byte(strings.TrimPrefix(got, "\ufeff")), &got); err != nil {
			t.Fatal(err)
		}
	}
	if got != value {
		t.Fatalf("셸이 인자를 변경함: got %q want %q", got, value)
	}
}
