package main

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/autostart"
	"github.com/your-org/pulsemetry/internal/hostenv"
	"github.com/your-org/pulsemetry/internal/installer"
)

// 이 파일의 테스트는 **진짜 launchctl·systemctl 을 절대 부르지 않는다.**
// autostart.Runner 가 공개 인터페이스라 명령 실행 지점을 통째로 갈아 끼울 수 있고,
// 홈은 t.TempDir() 로 주입한다. 그러지 않으면 CI 러너의 실제 gui/$UID 도메인에
// 등록이 일어나고 그 등록은 테스트가 끝나도 사라지지 않는다.

type fakeCmdRunner struct {
	calls []string
	code  int
}

func (f *fakeCmdRunner) Run(_ context.Context, name string, args ...string) (string, string, int, error) {
	f.calls = append(f.calls, strings.Join(append([]string{name}, args...), " "))
	return "", "", f.code, nil
}

// fakeFactory 는 임시 홈과 fake Runner 를 물린 managerFactory 다.
func fakeFactory(t *testing.T, runner autostart.Runner) (managerFactory, hostenv.Env) {
	t.Helper()
	env := hostenv.Env{OS: "darwin", HomeDir: t.TempDir()}
	f := func(execPath string, args []string) (*autostart.Manager, error) {
		if execPath == "" {
			execPath = "/usr/local/bin/telemetryctl"
		}
		return autostart.New(autostart.Options{
			Env: env, GOOS: "darwin", UID: 501, Runner: runner, ExecPath: execPath, Args: args,
		})
	}
	return f, env
}

// shortenAutostartWait 는 데몬이 뜰 리 없는 테스트가 5초를 기다리지 않게 한다.
func shortenAutostartWait(t *testing.T) {
	t.Helper()
	timeout, interval := autostartWaitTimeout, autostartWaitInterval
	autostartWaitTimeout, autostartWaitInterval = 10*time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { autostartWaitTimeout, autostartWaitInterval = timeout, interval })
}

func TestRunAutostart하위명령파싱(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantErr  string
	}{
		{"하위 명령이 없으면 사용법 오류", nil, 2, "enable|disable|status"},
		{"알 수 없는 하위 명령", []string{"restart"}, 2, `"restart"`},
		{"알 수 없는 플래그", []string{"status", "--nope"}, 2, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			factory, _ := fakeFactory(t, &fakeCmdRunner{})

			got := runAutostart(&stdout, &stderr, tc.args, factory)
			if got != tc.wantCode {
				t.Fatalf("종료 코드 = %d, want %d (stderr: %s)", got, tc.wantCode, stderr.String())
			}
			if tc.wantErr != "" && !strings.Contains(stderr.String(), tc.wantErr) {
				t.Errorf("stderr 에 %q 가 없다:\n%s", tc.wantErr, stderr.String())
			}
		})
	}
}

func TestRunAutostartEnable(t *testing.T) {
	shortenAutostartWait(t)

	t.Run("등록하고 경로를 알린다", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		runner := &fakeCmdRunner{}
		factory, env := fakeFactory(t, runner)
		tmp := t.TempDir()

		code := runAutostart(&stdout, &stderr,
			[]string{"enable", "--state", filepath.Join(tmp, "state.json"), "--data-dir", tmp}, factory)
		if code != 0 {
			t.Fatalf("종료 코드 = %d (stderr: %s)", code, stderr.String())
		}

		out := stdout.String()
		for _, want := range []string{"자동 실행 등록 완료", autostart.LaunchAgentPath(env), "autostart disable"} {
			if !strings.Contains(out, want) {
				t.Errorf("stdout 에 %q 가 없다:\n%s", want, out)
			}
		}
		// 데몬이 뜰 리 없으므로 확인 실패를 알려야 한다 — 조용히 성공으로 보이면 안 된다.
		if !strings.Contains(out, "데몬이 응답하지 않았습니다") {
			t.Errorf("데몬 미기동을 알리지 않았다:\n%s", out)
		}
		if len(runner.calls) == 0 {
			t.Fatal("launchctl 을 부르지 않았다")
		}
	})

	t.Run("상대 경로 --exec-path 는 사용법 오류다", func(t *testing.T) {
		// 상대 경로를 유닛에 적으면 서비스 관리자가 cwd 기준으로 풀어 엉뚱한 파일을 띄운다.
		var stdout, stderr bytes.Buffer
		factory, _ := fakeFactory(t, &fakeCmdRunner{})

		code := runAutostart(&stdout, &stderr, []string{"enable", "--exec-path", "./telemetryctl"}, factory)
		if code != 2 {
			t.Fatalf("종료 코드 = %d, want 2", code)
		}
		if !strings.Contains(stderr.String(), "절대 경로") {
			t.Errorf("stderr:\n%s", stderr.String())
		}
	})
}

func TestRunAutostartDisable은등록이없어도성공한다(t *testing.T) {
	// "없음은 오류가 아니다" — installer.DisableLocal 의 AlreadyInState 와 같은 규칙이다.
	var stdout, stderr bytes.Buffer
	runner := &fakeCmdRunner{code: 3} // launchctl bootout: No such process
	factory, _ := fakeFactory(t, runner)

	code := runAutostart(&stdout, &stderr, []string{"disable"}, factory)
	if code != 0 {
		t.Fatalf("종료 코드 = %d (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "이미 등록돼 있지 않습니다") {
		t.Errorf("stdout:\n%s", stdout.String())
	}
}

func TestRunAutostartStatus는미등록에서도종료코드0이다(t *testing.T) {
	// status 는 진단 명령이라 어떤 상태에서도 동작해야 한다 (statuslocal.go 머리말).
	var stdout, stderr bytes.Buffer
	factory, _ := fakeFactory(t, &fakeCmdRunner{code: 113})

	code := runAutostart(&stdout, &stderr, []string{"status"}, factory)
	if code != 0 {
		t.Fatalf("종료 코드 = %d (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "등록 안 됨") {
		t.Errorf("stdout:\n%s", stdout.String())
	}
}

func TestAutostartSummary(t *testing.T) {
	tests := []struct {
		name string
		st   autostart.Status
		want string
	}{
		{
			name: "미등록은 다음 할 일을 준다",
			st:   autostart.Status{Kind: autostart.KindLaunchd, Supported: true},
			want: "등록 안 됨 — `telemetryctl autostart enable`",
		},
		{
			name: "실행 중이면 pid 를 보여 준다",
			st: autostart.Status{
				Kind: autostart.KindLaunchd, Supported: true,
				Registered: true, Loaded: true, Running: true, PID: 4821,
			},
			want: "등록됨 (launchd · com.your-org.pulsemetry.daemon · 로드됨 · pid 4821)",
		},
		{
			name: "로드되지 않았으면 그렇게 말한다",
			st: autostart.Status{
				Kind: autostart.KindSystemdUser, Supported: true, Registered: true,
			},
			want: "로드 안 됨",
		},
		{
			// 크래시 루프의 가장 흔한 원인이다. 드리프트보다 먼저 말한다.
			name: "실행 파일이 사라진 것이 드리프트보다 먼저다",
			st: autostart.Status{
				Kind: autostart.KindLaunchd, Supported: true, Registered: true, Loaded: true,
				ExecPathDrift: true, ExecPathMissing: true,
			},
			want: "등록된 실행 파일이 존재하지 않습니다",
		},
		{
			name: "드리프트만 있으면 드리프트를 말한다",
			st: autostart.Status{
				Kind: autostart.KindLaunchd, Supported: true, Registered: true, Loaded: true,
				ExecPathDrift: true,
			},
			want: "지금 바이너리와 다릅니다",
		},
		{
			name: "미지원 환경은 사유를 담는다",
			st: autostart.Status{
				Kind: autostart.KindSystemdUser,
				// WSL 사용자에게 무엇을 해야 하는지 알려 주는 유일한 자리다.
				Detail: "WSL 에서 systemd 가 켜져 있지 않습니다",
			},
			want: "지원하지 않는 환경 (WSL 에서 systemd 가 켜져 있지 않습니다)",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := autostartSummary(tc.st); !strings.Contains(got, tc.want) {
				t.Fatalf("autostartSummary = %q, want %q 를 포함", got, tc.want)
			}
		})
	}
}

func TestAutostartArgs(t *testing.T) {
	t.Run("기본은 daemon 하나뿐이다", func(t *testing.T) {
		// --state·--data-dir 을 유닛에 구우면 state.json 의 위치가 두 곳이 되고,
		// installer.EnableLocal 이 state.Local.DataDir 를 바꾸는 순간 조용히 어긋난다.
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home) // windows 러너용

		target, err := resolveLocalTarget("", "")
		if err != nil {
			t.Fatalf("resolveLocalTarget: %v", err)
		}
		got := autostartArgs(target)
		if len(got) != 1 || got[0] != "daemon" {
			t.Fatalf("autostartArgs = %v, want [daemon]", got)
		}
	})

	t.Run("기본값과 다른 경로는 굽는다", func(t *testing.T) {
		// 패키저·CI 가 비표준 경로를 쓸 수 있어야 한다. 명시했고 기본값과 다를 때만이다.
		tmp := t.TempDir()
		statePath := filepath.Join(tmp, "state.json")
		target, err := resolveLocalTarget(tmp, statePath)
		if err != nil {
			t.Fatalf("resolveLocalTarget: %v", err)
		}
		got := strings.Join(autostartArgs(target), " ")
		for _, want := range []string{"daemon", "--state " + statePath, "--data-dir " + tmp} {
			if !strings.Contains(got, want) {
				t.Errorf("autostartArgs = %q, want %q 를 포함", got, want)
			}
		}
	})
}

func TestEnableAutostartBestEffort는배선안된설치를건너뛴다(t *testing.T) {
	// 배선하지 않았으면 벤더 설정이 회사 Collector 를 직접 가리킨다. 그 상태의 데몬은
	// 아무것도 받지 못하므로, 로그인마다 할 일 없는 프로세스를 띄우는 것은 도움이 아니다.
	var stdout, stderr bytes.Buffer
	hint := enableAutostartBestEffort(&stdout, &stderr, &installer.Report{LocalEnabled: false})

	if hint.Registered {
		t.Error("배선되지 않았는데 등록했다")
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("조용히 넘어가야 한다:\nstdout=%s\nstderr=%s", stdout.String(), stderr.String())
	}
}

func TestRunEnroll은실패하면등록기를부르지않는다(t *testing.T) {
	// 등록은 Apply 성공 뒤여야 한다. 먼저 등록하면 state.json 이 없어 데몬 기동이
	// 실패하고, 재시작 정책 아래 스로틀 크래시 루프가 된다.
	var stdout, stderr bytes.Buffer
	called := false
	reg := func(_, _ io.Writer, _ *installer.Report) autostartHint {
		called = true
		return autostartHint{}
	}

	if code := runEnroll(&stdout, &stderr, nil, reg); code != 2 {
		t.Fatalf("종료 코드 = %d, want 2", code)
	}
	if called {
		t.Fatal("enroll 이 실패했는데 자동 실행 등록을 시도했다")
	}
	if !strings.Contains(stderr.String(), "--invite") {
		t.Errorf("stderr:\n%s", stderr.String())
	}
}

func TestWarnDaemonNotRunning은힌트에따라조언을바꾼다(t *testing.T) {
	// 등록돼 있는데도 데몬이 없는 상태에 "데몬을 실행하세요" 는 틀린 조언이다.
	tests := []struct {
		name    string
		hint    autostartHint
		want    string
		notWant string
	}{
		{
			name: "미등록이면 등록을 권한다",
			hint: autostartHint{},
			want: "autostart enable",
		},
		{
			name:    "등록돼 있으면 로그를 보라고 한다",
			hint:    autostartHint{Registered: true, Kind: autostart.KindLaunchd, LogPath: "/tmp/daemon.err.log"},
			want:    "/tmp/daemon.err.log",
			notWant: "autostart enable",
		},
		{
			name:    "미지원 환경이면 직접 실행을 권한다",
			hint:    autostartHint{Unsupported: true},
			want:    "직접 실행하세요",
			notWant: "autostart enable",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			// 데몬이 있을 리 없는 빈 디렉터리다.
			warnDaemonNotRunning(&out, t.TempDir(), tc.hint)

			got := out.String()
			if !strings.Contains(got, tc.want) {
				t.Errorf("출력에 %q 가 없다:\n%s", tc.want, got)
			}
			if tc.notWant != "" && strings.Contains(got, tc.notWant) {
				t.Errorf("출력에 %q 가 있으면 안 된다:\n%s", tc.notWant, got)
			}
		})
	}
}

func TestUsage에autostart가있다(t *testing.T) {
	// 명령을 추가하고 사용법에 적는 것을 잊으면 사용자에게는 없는 기능과 같다.
	var out bytes.Buffer
	writeUsage(&out)

	for _, want := range []string{
		"autostart enable|disable|status",
		"autostart 옵션:",
		"--exec-path",
		"--force",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("사용법에 %q 가 없다", want)
		}
	}
	// 이 티켓이 없앤 문구다. 남아 있으면 사용자가 아직 방법이 없다고 믿는다.
	if strings.Contains(out.String(), "자동 실행 등록은 후속 티켓") {
		t.Error("사용법에 낡은 '후속 티켓' 문구가 남아 있다")
	}
}
