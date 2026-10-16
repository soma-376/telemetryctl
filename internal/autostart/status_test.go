package autostart

import (
	"strings"
	"testing"
)

// launchctl print 의 실제 출력 모양이다. **이 포맷은 macOS 릴리스마다 바뀐다** —
// 그래서 로드 여부는 종료 코드로만 판정하고 여기서 뽑는 값은 전부 기회주의적이다.
const launchctlPrintSample = `com.your-org.pulsemetry.daemon = {
	active count = 1
	path = /Users/tester/Library/LaunchAgents/com.your-org.pulsemetry.daemon.plist
	state = running

	program = /usr/local/bin/telemetryctl
	arguments = {
		/usr/local/bin/telemetryctl
		daemon
	}

	default environment = {
		PATH => /usr/bin:/bin:/usr/sbin:/sbin
	}

	pid = 4821
	immediate reason = speculative
	forks = 0
	execs = 1
	initialized = 1
	last exit code = 0

	spawn type = daemon (3)
	jetsam priority = 40
}`

const launchctlPrintCrashed = `com.your-org.pulsemetry.daemon = {
	active count = 0
	state = waiting
	last exit code = 1
	runs = 12
}`

func TestParseLaunchctlPrint(t *testing.T) {
	tests := []struct {
		name        string
		out         string
		wantPID     int
		wantRunning bool
		wantExit    int
		wantDetail  bool
	}{
		{"실행 중이면 pid 와 state 를 읽는다", launchctlPrintSample, 4821, true, 0, false},
		{"죽어 있으면 마지막 종료 코드를 읽는다", launchctlPrintCrashed, 0, false, 1, false},
		{
			// 형식이 바뀌어도 죽지 않는다. status 는 어떤 환경에서도 멈추면 안 된다.
			name: "형식이 바뀌어도 죽지 않는다", out: "완전히 다른 출력\n{ }\n", wantDetail: true,
		},
		{name: "빈 출력도 죽지 않는다", out: "", wantDetail: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := Status{Kind: KindLaunchd}
			parseLaunchctlPrint(tc.out, &st)
			if st.PID != tc.wantPID {
				t.Errorf("PID = %d, want %d", st.PID, tc.wantPID)
			}
			if st.Running != tc.wantRunning {
				t.Errorf("Running = %v, want %v", st.Running, tc.wantRunning)
			}
			if st.LastExit != tc.wantExit {
				t.Errorf("LastExit = %d, want %d", st.LastExit, tc.wantExit)
			}
			if got := st.Detail != ""; got != tc.wantDetail {
				t.Errorf("Detail 채워짐 = %v (%q), want %v", got, st.Detail, tc.wantDetail)
			}
		})
	}
}

// systemctl show 의 실제 출력 모양이다. --value 는 systemd ≥230 을 요구하므로
// 평범한 Key=Value 로 받는다. `systemctl status` 는 절대 파싱하지 않는다.
const systemctlShowSample = `MainPID=4821
ExecMainStatus=0
FragmentPath=/home/tester/.config/systemd/user/pulsemetry-daemon.service
NRestarts=2`

func TestParseSystemctlShow(t *testing.T) {
	t.Run("네 필드를 읽는다", func(t *testing.T) {
		st := Status{Kind: KindSystemdUser}
		parseSystemctlShow(systemctlShowSample, &st)
		if st.PID != 4821 {
			t.Errorf("PID = %d, want 4821", st.PID)
		}
		if st.LastExit != 0 {
			t.Errorf("LastExit = %d, want 0", st.LastExit)
		}
		if st.Restarts != 2 {
			t.Errorf("Restarts = %d, want 2", st.Restarts)
		}
	})

	t.Run("MainPID 0 은 실행 중이 아니라는 뜻이다", func(t *testing.T) {
		st := Status{Kind: KindSystemdUser}
		parseSystemctlShow("MainPID=0\nExecMainStatus=1\nNRestarts=5", &st)
		if st.PID != 0 {
			t.Errorf("PID = %d, want 0", st.PID)
		}
		if st.LastExit != 1 {
			t.Errorf("LastExit = %d, want 1", st.LastExit)
		}
	})

	t.Run("쓰레기를 먹여도 죽지 않는다", func(t *testing.T) {
		st := Status{Kind: KindSystemdUser}
		parseSystemctlShow("완전히 다른 출력\n=\nMainPID=없음\n", &st)
		if st.PID != 0 || st.Restarts != 0 {
			t.Fatalf("쓰레기 입력에서 값이 채워졌다: %+v", st)
		}
	})
}

func TestOneLine(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"여러 줄을 한 줄로", "첫 줄\n둘째 줄\n", "첫 줄 둘째 줄"},
		{"빈 값은 안내 문구로", "   \n\t", "출력 없음"},
		{"연속 공백을 하나로", "a    b", "a b"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := oneLine(tc.in); got != tc.want {
				t.Fatalf("oneLine = %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("긴 출력은 자른다", func(t *testing.T) {
		got := oneLine(strings.Repeat("x", 500))
		if len([]rune(got)) > 301 {
			t.Fatalf("oneLine 길이 = %d, 상한을 넘었다", len([]rune(got)))
		}
		if !strings.HasSuffix(got, "…") {
			t.Fatalf("잘렸으면 말줄임표가 있어야 한다: %q", got)
		}
	})
}

func TestAppendDetail(t *testing.T) {
	if got := appendDetail("", "첫 사유"); got != "첫 사유" {
		t.Fatalf("appendDetail = %q", got)
	}
	if got := appendDetail("첫 사유", "둘째 사유"); got != "첫 사유 · 둘째 사유" {
		t.Fatalf("appendDetail = %q", got)
	}
	if got := appendDetail("첫 사유", "  "); got != "첫 사유" {
		t.Fatalf("빈 사유를 붙였다: %q", got)
	}
}
