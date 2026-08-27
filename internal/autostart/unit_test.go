package autostart

import (
	"strings"
	"testing"
)

// Tier 1 — systemd unit 렌더러. plist 와 같은 이유로 세 러너 모두에서 돈다.

func defaultUnit(execPath string) string {
	return string(renderUnit(unitParams{ExecPath: execPath, Args: []string{"daemon"}}))
}

// sectionOf 는 key 가 속한 [섹션] 이름을 돌려준다. 못 찾으면 빈 문자열이다.
func sectionOf(unit, key string) string {
	section := ""
	for _, line := range strings.Split(unit, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "#"):
			continue
		case strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]"):
			section = strings.Trim(line, "[]")
		case strings.HasPrefix(line, key+"="):
			return section
		}
	}
	return ""
}

func TestRenderUnit(t *testing.T) {
	out := defaultUnit("/usr/local/bin/telemetryctl")

	t.Run("StartLimitIntervalSec 는 [Unit] 섹션에 있다", func(t *testing.T) {
		// systemd v229 부터 이 두 키는 [Service] 가 아니라 [Unit] 에 있어야 한다.
		// [Service] 에 두면 systemd 가 경고만 내고 **제한이 걸리지 않아** 미enroll 상태의
		// 크래시 루프가 무한이 된다.
		for _, key := range []string{"StartLimitIntervalSec", "StartLimitBurst"} {
			if got := sectionOf(out, key); got != "Unit" {
				t.Errorf("%s 가 [%s] 에 있다, want [Unit]\n%s", key, got, out)
			}
		}
	})

	t.Run("Restart 는 on-failure 다", func(t *testing.T) {
		// ADR 0007. always 로 바꾸면 systemctl --user stop 이 무의미해진다.
		if !strings.Contains(out, "\nRestart=on-failure\n") {
			t.Errorf("Restart=on-failure 가 없다:\n%s", out)
		}
		if strings.Contains(out, "Restart=always") {
			t.Errorf("Restart=always 가 있다:\n%s", out)
		}
	})

	t.Run("WantedBy 는 default.target 이다", func(t *testing.T) {
		if got := sectionOf(out, "WantedBy"); got != "Install" {
			t.Errorf("WantedBy 가 [%s] 에 있다, want [Install]", got)
		}
		if !strings.Contains(out, "WantedBy=default.target") {
			t.Errorf("WantedBy=default.target 이 없다:\n%s", out)
		}
	})

	t.Run("TimeoutStopSec 는 20 이다", func(t *testing.T) {
		// daemon.DefaultShutdownTimeout(15s)보다 커야 한다는 불변식은
		// internal/daemon 의 테스트가 지킨다. 여기서는 렌더링된 값만 확인한다.
		if !strings.Contains(out, "\nTimeoutStopSec=20\n") {
			t.Errorf("TimeoutStopSec=20 이 없다:\n%s", out)
		}
	})

	t.Run("RestartSec 는 5 다", func(t *testing.T) {
		if !strings.Contains(out, "\nRestartSec=5\n") {
			t.Errorf("RestartSec=5 가 없다:\n%s", out)
		}
	})

	t.Run("ExecStart 는 exec 와 daemon 뿐이다", func(t *testing.T) {
		if !strings.Contains(out, "\nExecStart=/usr/local/bin/telemetryctl daemon\n") {
			t.Errorf("ExecStart 가 기대와 다르다:\n%s", out)
		}
		// --state·--data-dir 이 구워지면 state.json 의 위치가 두 곳이 된다.
		for _, forbidden := range []string{"--state", "--data-dir", "--listen"} {
			if strings.Contains(out, forbidden) {
				t.Errorf("ExecStart 에 %s 가 구워져 있다:\n%s", forbidden, out)
			}
		}
	})

	t.Run("샌드박싱 지시자를 넣지 않는다", func(t *testing.T) {
		// ProtectHome 은 ~/.pulsemetry 를 통째로 가리고, PrivateTmp 와 D-Bus 제한은
		// go-keyring 의 Secret Service 접근을 깬다. 둘 다 데몬이 뜨지 못하는 결과다.
		for _, directive := range []string{"ProtectHome=", "PrivateTmp=", "ProtectSystem="} {
			for _, line := range strings.Split(out, "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), directive) {
					t.Errorf("샌드박싱 지시자 %s 가 활성 설정으로 들어갔다:\n%s", directive, out)
				}
			}
		}
	})

	t.Run("SyslogIdentifier 는 pulsemetry 다", func(t *testing.T) {
		if !strings.Contains(out, "\nSyslogIdentifier=pulsemetry\n") {
			t.Errorf("SyslogIdentifier 가 없다:\n%s", out)
		}
	})
}

func TestUnitCommandLine공백이든경로를따옴표로감싼다(t *testing.T) {
	tests := []struct {
		name string
		exec string
		want string
	}{
		{"평범한 경로는 그대로", "/usr/local/bin/telemetryctl", "/usr/local/bin/telemetryctl daemon"},
		{"공백이 있으면 감싼다", "/opt/my apps/telemetryctl", `"/opt/my apps/telemetryctl" daemon`},
		{"따옴표를 이스케이프한다", `/opt/a"b/telemetryctl`, `"/opt/a\"b/telemetryctl" daemon`},
		{"백슬래시를 이스케이프한다", `/opt/a\b/telemetryctl`, `"/opt/a\\b/telemetryctl" daemon`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := unitCommandLine(tc.exec, []string{"daemon"}); got != tc.want {
				t.Fatalf("unitCommandLine = %q, want %q", got, tc.want)
			}
		})
	}
}
