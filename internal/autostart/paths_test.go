package autostart

import (
	"path/filepath"
	"testing"

	"github.com/your-org/pulsemetry/internal/hostenv"
)

func TestUnitPath는XDG_CONFIG_HOME을존중한다(t *testing.T) {
	// systemd **자신**이 사용자 유닛을 이 변수로 해석한다. 무시하면 그 변수가 설정된
	// 장비에서 systemd 가 영원히 읽지 않을 파일을 쓰게 되고, 등록은 성공했는데
	// 아무 일도 일어나지 않는다.
	env := hostenv.Env{OS: "linux", HomeDir: "/home/tester"}

	t.Run("미설정이면 홈 아래 .config 다", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", "")
		want := filepath.Join("/home/tester", ".config", "systemd", "user", UnitName)
		if got := UnitPath(env); got != want {
			t.Fatalf("UnitPath = %q, want %q", got, want)
		}
	})

	t.Run("설정되면 그 아래다", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", filepath.Join("/home/tester", "cfg"))
		want := filepath.Join("/home/tester", "cfg", "systemd", "user", UnitName)
		if got := UnitPath(env); got != want {
			t.Fatalf("UnitPath = %q, want %q", got, want)
		}
	})

	t.Run("공백만 있으면 미설정으로 본다", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", "   ")
		want := filepath.Join("/home/tester", ".config", "systemd", "user", UnitName)
		if got := UnitPath(env); got != want {
			t.Fatalf("UnitPath = %q, want %q", got, want)
		}
	})
}

func TestLaunchAgentPath는사용자홈아래다(t *testing.T) {
	env := hostenv.Env{OS: "darwin", HomeDir: "/Users/tester"}
	want := filepath.Join("/Users/tester", "Library", "LaunchAgents", Label+".plist")
	if got := LaunchAgentPath(env); got != want {
		// 시스템 수준(/Library/LaunchAgents)으로 새면 sudo 가 필요해지고
		// LaunchDaemon 과 혼동되어 키체인 접근이 깨진다.
		t.Fatalf("LaunchAgentPath = %q, want %q", got, want)
	}
}

func TestLogDir(t *testing.T) {
	home := filepath.Join("/home", "x")
	tests := []struct {
		name string
		goos string
		want string
	}{
		{"darwin 은 ~/Library/Logs 다", "darwin", filepath.Join(home, "Library", "Logs", "pulsemetry")},
		// 빈 문자열이 "우리가 로그를 관리하지 않는다" 는 신호이고,
		// 그 덕에 internal/daemon 이 플랫폼을 몰라도 된다.
		{"linux 는 journald 라 빈 문자열이다", "linux", ""},
		{"windows 는 아직 없다", "windows", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := hostenv.Env{OS: tc.goos, HomeDir: home}
			if got := LogDir(env); got != tc.want {
				t.Fatalf("LogDir = %q, want %q", got, tc.want)
			}
			if tc.want == "" {
				if p := LogPath(env); p != "" {
					t.Fatalf("LogPath = %q, want 빈 문자열", p)
				}
				if p := ErrLogPath(env); p != "" {
					t.Fatalf("ErrLogPath = %q, want 빈 문자열", p)
				}
			}
		})
	}
}
