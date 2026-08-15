package autostart

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/your-org/pulsemetry/internal/hostenv"
)

// Tier 2 — exec 를 fakeRunner 로 갈아 끼우고 **명령 순서와 인자**를 단언한다.
// 실제 등록은 하지 않는다. 세 러너 모두에서 돈다.

func darwinOptions(t *testing.T, fr *fakeRunner) Options {
	t.Helper()
	return Options{
		Env:      testEnv(t, "darwin"),
		GOOS:     "darwin",
		UID:      501, // os.Getuid() 에 좌우되지 않게 고정한다
		Runner:   fr,
		ExecPath: "/usr/local/bin/telemetryctl",
	}
}

func linuxOptions(t *testing.T, fr *fakeRunner) Options {
	t.Helper()
	// XDG_CONFIG_HOME 이 개발자 환경에 설정돼 있으면 유닛이 임시 홈 밖으로 나간다.
	t.Setenv("XDG_CONFIG_HOME", "")
	stat, look := systemdPresent()
	return Options{
		Env:      testEnv(t, "linux"),
		GOOS:     "linux",
		Runner:   fr,
		ExecPath: "/usr/local/bin/telemetryctl",
		Stat:     stat,
		LookPath: look,
	}
}

func TestNew는빈HomeDir를거부한다(t *testing.T) {
	// 이것이 단위 테스트가 개발자의 진짜 ~/Library/LaunchAgents 로 새지 못하게 하는
	// 구조적 보장이다. 없애면 안 된다.
	if _, err := New(Options{GOOS: "darwin"}); err == nil {
		t.Fatal("빈 HomeDir 를 받아들였다")
	}
}

func TestNew는windows에서ErrUnsupportedPlatform이다(t *testing.T) {
	env := testEnv(t, "windows")
	fr := newFakeRunner(nil)

	m, err := New(Options{Env: env, GOOS: "windows", Runner: fr, ExecPath: `C:\bin\telemetryctl.exe`})
	if !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("New err = %v, want ErrUnsupportedPlatform", err)
	}
	if m != nil {
		t.Fatal("windows 에서 Manager 를 돌려줬다")
	}
	// 파일도 쓰지 않고 명령도 부르지 않아야 한다.
	if fr.count() != 0 {
		t.Fatalf("exec 호출 %d회, want 0", fr.count())
	}
	entries, err := os.ReadDir(env.HomeDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("홈에 파일이 생겼다: %v", entries)
	}
}

func TestDarwinEnable명령순서(t *testing.T) {
	fr := newFakeRunner(nil)
	opts := darwinOptions(t, fr)
	plistPath := LaunchAgentPath(opts.Env)

	// bootstrap 을 부르는 **시점에** plist 가 이미 디스크에 0644 로 있어야 한다.
	// 순서가 뒤집히면 launchctl 이 없는 파일을 읽고 실패한다.
	var perm os.FileMode
	var presentAtBootstrap bool
	fr.onCall = func(c call) {
		if c.Name == "launchctl" && len(c.Args) > 0 && c.Args[0] == "bootstrap" {
			if fi, err := os.Stat(plistPath); err == nil {
				presentAtBootstrap = true
				perm = fi.Mode().Perm()
			}
		}
	}

	m := newTestManager(t, opts)
	res, err := m.Enable(context.Background())
	if err != nil {
		t.Fatalf("Enable: %v", err)
	}

	want := []string{
		// 과거 `launchctl disable` 로 굳은 override 해제
		"launchctl enable gui/501/" + Label,
		// 멱등 재등록 — 로드된 적이 없으면 exit 3 이고 그것은 정상이다
		"launchctl bootout gui/501/" + Label,
		// RunAtLoad=true 라 이것이 곧 기동이다. kickstart 를 따로 부르지 않는다.
		"launchctl bootstrap gui/501 " + plistPath,
	}
	if got := fr.keys(); !reflect.DeepEqual(got, want) {
		t.Fatalf("명령 순서가 다르다:\n got %v\nwant %v", got, want)
	}
	if !presentAtBootstrap {
		t.Fatal("bootstrap 시점에 plist 가 디스크에 없었다")
	}
	// 권한만 떼어 낸다. windows 는 POSIX 권한 비트를 저장하지 않고 FILE_ATTRIBUTE_READONLY
	// 한 비트로 축약해서, os.Stat 이 읽기 전용이면 0444 를 아니면 0666 을 합성한다 —
	// **0644 는 그 OS 에서 관측 자체가 불가능하다.** AtomicWriteFile 이 넘긴 0o644 도
	// windows 의 Chmod 에서는 owner write 비트만 쓰여 "읽기 전용 해제" 로 끝난다.
	// 즉 0666 은 파일이 느슨하게 쓰인 것이 아니라 그 정보가 애초에 없다는 뜻이다.
	//
	// 위의 명령 순서·bootstrap 시점 존재 단언은 windows 에서도 유효하므로 함께 끄지 않는다.
	// perm 은 Enable 중 onCall 에서 이미 포착된 값이고 여기서는 읽기만 한다.
	t.Run("plist 권한은 0644 다", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("windows 는 POSIX 권한 비트를 쓰지 않는다")
		}
		if perm != 0o644 {
			t.Errorf("plist 권한 = %v, want 0644", perm)
		}
	})
	if res.Kind != KindLaunchd || res.UnitPath != plistPath {
		t.Errorf("Result = %+v", res)
	}
	// launchd 는 로그 디렉터리를 만들어 주지 않는다.
	if _, err := os.Stat(LogDir(opts.Env)); err != nil {
		t.Errorf("로그 디렉터리가 없다: %v", err)
	}
	// 절대 부르면 안 되는 명령 — 영구 override 를 써서 미래의 enable 을 방해한다.
	for _, k := range fr.keys() {
		if strings.HasPrefix(k, "launchctl disable ") {
			t.Errorf("launchctl disable 을 불렀다: %s", k)
		}
	}
}

func TestDarwinEnable기본인자는daemon하나뿐이다(t *testing.T) {
	// --state·--data-dir 이 유닛에 구워지면 state.json 의 위치가 두 곳이 되고,
	// installer.EnableLocal 이 state.Local.DataDir 를 바꾸는 순간 조용히 어긋난다.
	fr := newFakeRunner(nil)
	opts := darwinOptions(t, fr)
	m := newTestManager(t, opts)
	if _, err := m.Enable(context.Background()); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	content, err := os.ReadFile(LaunchAgentPath(opts.Env))
	if err != nil {
		t.Fatalf("plist 읽기: %v", err)
	}
	want := "<key>ProgramArguments</key>\n\t<array>\n" +
		"\t\t<string>/usr/local/bin/telemetryctl</string>\n" +
		"\t\t<string>daemon</string>\n\t</array>"
	if !strings.Contains(string(content), want) {
		t.Fatalf("ProgramArguments 가 기대와 다르다:\n%s", content)
	}
}

func TestDarwinEnable은bootout의exit3를허용한다(t *testing.T) {
	// 등록된 적이 없는 깨끗한 호스트의 정상 경로다. 여기서 실패하면 첫 등록이 늘 실패한다.
	fr := newFakeRunner(map[string]scriptedReply{
		"launchctl bootout gui/501/" + Label: {Code: 3, Stderr: "Boot-out failed: 3: No such process"},
	})
	m := newTestManager(t, darwinOptions(t, fr))
	if _, err := m.Enable(context.Background()); err != nil {
		t.Fatalf("bootout exit 3 가 enable 을 실패시켰다: %v", err)
	}
}

func TestDarwinEnable은bootstrap실패를전파한다(t *testing.T) {
	tests := []struct {
		name     string
		code     int
		stderr   string
		wantText string
	}{
		{
			name: "일반 실패는 stderr 를 담는다", code: 112,
			stderr:   "Bootstrap failed: 112: Could not find specified service",
			wantText: "Could not find specified service",
		},
		{
			// CI 러너와 SSH 세션에서 실제로 나는 실패다. errno 만 보여 주면 아무도 못 고친다.
			name: "exit 5 는 GUI 세션 부재를 지목한다", code: 5,
			stderr:   "Bootstrap failed: 5: Input/output error",
			wantText: "GUI 로그인 세션이 없습니다",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fr := newFakeRunner(nil)
			opts := darwinOptions(t, fr)
			key := "launchctl bootstrap gui/501 " + LaunchAgentPath(opts.Env)
			fr.replies = map[string]scriptedReply{key: {Code: tc.code, Stderr: tc.stderr}}

			m := newTestManager(t, opts)
			_, err := m.Enable(context.Background())
			if err == nil {
				t.Fatal("bootstrap 실패가 전파되지 않았다")
			}
			if !strings.Contains(err.Error(), tc.wantText) {
				t.Fatalf("오류 메시지에 %q 가 없다: %v", tc.wantText, err)
			}
		})
	}
}

func TestDarwinDisable(t *testing.T) {
	t.Run("bootout 후 파일을 지운다", func(t *testing.T) {
		fr := newFakeRunner(nil)
		opts := darwinOptions(t, fr)
		m := newTestManager(t, opts)
		if _, err := m.Enable(context.Background()); err != nil {
			t.Fatalf("Enable: %v", err)
		}

		plistPath := LaunchAgentPath(opts.Env)
		// 파일을 먼저 지우면 로드된 job 이 로그아웃까지 남는다. 순서를 확인한다.
		var existedAtBootout bool
		fr.onCall = func(c call) {
			if c.Name == "launchctl" && len(c.Args) > 0 && c.Args[0] == "bootout" {
				_, err := os.Stat(plistPath)
				existedAtBootout = err == nil
			}
		}

		res, err := m.Disable(context.Background())
		if err != nil {
			t.Fatalf("Disable: %v", err)
		}
		if !existedAtBootout {
			t.Error("bootout 시점에 plist 가 이미 지워져 있었다")
		}
		if _, err := os.Stat(plistPath); err == nil {
			t.Error("plist 가 남아 있다")
		}
		if res.AlreadyInState {
			t.Error("등록돼 있었는데 AlreadyInState 다")
		}
	})

	t.Run("깨끗한 호스트에서는 AlreadyInState 로 성공한다", func(t *testing.T) {
		// "없음은 오류가 아니다" — installer.LoadState 와 같은 규칙이다.
		fr := newFakeRunner(map[string]scriptedReply{
			"launchctl bootout gui/501/" + Label: {Code: 3},
		})
		m := newTestManager(t, darwinOptions(t, fr))
		res, err := m.Disable(context.Background())
		if err != nil {
			t.Fatalf("Disable: %v", err)
		}
		if !res.AlreadyInState {
			t.Fatal("AlreadyInState 가 false 다")
		}
	})
}

func TestEnable은멱등이다(t *testing.T) {
	fr := newFakeRunner(nil)
	opts := darwinOptions(t, fr)
	m := newTestManager(t, opts)
	ctx := context.Background()

	if _, err := m.Enable(ctx); err != nil {
		t.Fatalf("첫 Enable: %v", err)
	}
	first, err := os.ReadFile(LaunchAgentPath(opts.Env))
	if err != nil {
		t.Fatalf("plist 읽기: %v", err)
	}
	if _, err := m.Enable(ctx); err != nil {
		t.Fatalf("둘째 Enable: %v", err)
	}
	second, err := os.ReadFile(LaunchAgentPath(opts.Env))
	if err != nil {
		t.Fatalf("plist 읽기: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("두 번 등록했더니 plist 가 달라졌다:\n%s\n---\n%s", first, second)
	}
}

func TestLinuxEnable명령순서(t *testing.T) {
	fr := newFakeRunner(nil)
	opts := linuxOptions(t, fr)
	unitPath := UnitPath(opts.Env)

	var presentAtReload bool
	fr.onCall = func(c call) {
		if c.key() == "systemctl --user daemon-reload" {
			_, err := os.Stat(unitPath)
			presentAtReload = err == nil
		}
	}

	m := newTestManager(t, opts)
	if _, err := m.Enable(context.Background()); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	want := []string{
		"systemctl --user daemon-reload",
		"systemctl --user enable --now " + UnitName,
	}
	if got := fr.keys(); !reflect.DeepEqual(got, want) {
		t.Fatalf("명령 순서가 다르다:\n got %v\nwant %v", got, want)
	}
	if !presentAtReload {
		t.Error("daemon-reload 시점에 unit 파일이 없었다")
	}
	// linger 를 켜지 않는다는 결정의 회귀 방어선이다 (프라이버시 의미론 변경 + 크래시 루프).
	for _, k := range fr.keys() {
		if strings.Contains(k, "enable-linger") {
			t.Errorf("loginctl enable-linger 를 불렀다: %s", k)
		}
	}
}

func TestLinuxDisable명령순서(t *testing.T) {
	fr := newFakeRunner(nil)
	opts := linuxOptions(t, fr)
	m := newTestManager(t, opts)
	ctx := context.Background()
	if _, err := m.Enable(ctx); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	unitPath := UnitPath(opts.Env)
	fr.calls = nil
	// 파일이 있어야 systemd 가 유닛을 찾아 정지하고 default.target.wants 링크를 지운다.
	var existedAtDisable, goneAtReload bool
	fr.onCall = func(c call) {
		switch c.key() {
		case "systemctl --user disable --now " + UnitName:
			_, err := os.Stat(unitPath)
			existedAtDisable = err == nil
		case "systemctl --user daemon-reload":
			_, err := os.Stat(unitPath)
			goneAtReload = err != nil
		}
	}

	if _, err := m.Disable(ctx); err != nil {
		t.Fatalf("Disable: %v", err)
	}

	want := []string{
		"systemctl --user disable --now " + UnitName,
		"systemctl --user daemon-reload",
	}
	if got := fr.keys(); !reflect.DeepEqual(got, want) {
		t.Fatalf("명령 순서가 다르다:\n got %v\nwant %v", got, want)
	}
	if !existedAtDisable {
		t.Error("disable --now 시점에 unit 파일이 이미 지워져 있었다")
	}
	if !goneAtReload {
		t.Error("daemon-reload 시점에 unit 파일이 아직 남아 있었다")
	}
}

func TestLinuxEnable은systemd가없으면거부한다(t *testing.T) {
	tests := []struct {
		name     string
		isWSL    bool
		wantText string
	}{
		{"WSL 은 wsl.conf 를 지목한다", true, "[boot] systemd=true"},
		{"비-WSL 은 init 을 지목한다", false, "init 이 systemd 가 아닙니다"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", "")
			fr := newFakeRunner(nil)
			env := testEnv(t, "linux")
			env.IsWSL = tc.isWSL
			_, look := systemdPresent()

			m := newTestManager(t, Options{
				Env: env, GOOS: "linux", Runner: fr, ExecPath: "/usr/local/bin/telemetryctl",
				Stat: systemdAbsent(), LookPath: look,
			})
			_, err := m.Enable(context.Background())
			if !errors.Is(err, ErrNoServiceManager) {
				t.Fatalf("err = %v, want ErrNoServiceManager", err)
			}
			// 고치는 방법이 다르므로 메시지가 달라야 한다.
			if !strings.Contains(err.Error(), tc.wantText) {
				t.Fatalf("오류 메시지에 %q 가 없다: %v", tc.wantText, err)
			}
			// 아무 파일도 쓰지 않고 아무 명령도 부르지 않아야 한다.
			if fr.count() != 0 {
				t.Errorf("exec 호출 %d회, want 0", fr.count())
			}
			if _, err := os.Stat(UnitPath(env)); err == nil {
				t.Error("unit 파일을 썼다")
			}
		})
	}
}

func TestLinuxEnable은버스실패시쓴파일을되돌린다(t *testing.T) {
	// systemd 가 영원히 읽지 않을 파일을 남기면 다음 status 가 "등록됨" 이라고 거짓말한다.
	fr := newFakeRunner(map[string]scriptedReply{
		"systemctl --user daemon-reload": {Code: 1, Stderr: "Failed to connect to bus: No medium found"},
	})
	opts := linuxOptions(t, fr)
	m := newTestManager(t, opts)

	_, err := m.Enable(context.Background())
	if !errors.Is(err, ErrNoServiceManager) {
		t.Fatalf("err = %v, want ErrNoServiceManager", err)
	}
	if !strings.Contains(err.Error(), "XDG_RUNTIME_DIR") {
		t.Errorf("오류가 원인을 지목하지 않는다: %v", err)
	}
	if _, err := os.Stat(UnitPath(opts.Env)); err == nil {
		t.Fatal("실패했는데 unit 파일이 남아 있다")
	}
}

func TestStatus는미등록을오류로만들지않는다(t *testing.T) {
	tests := []struct {
		name string
		opts func(*testing.T, *fakeRunner) Options
	}{
		{"darwin", darwinOptions},
		{"linux", linuxOptions},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// launchctl print 는 미로드에서 종료 코드가 0 이 아니다.
			fr := newFakeRunner(map[string]scriptedReply{
				"launchctl print gui/501/" + Label: {Code: 113, Stderr: "Could not find service"},
			})
			opts := tc.opts(t, fr)
			m := newTestManager(t, opts)

			st, err := m.Status(context.Background())
			if err != nil {
				t.Fatalf("Status: %v", err)
			}
			if st.Registered {
				t.Error("등록한 적 없는데 Registered 다")
			}
			if st.Loaded {
				t.Error("로드한 적 없는데 Loaded 다")
			}
			// 등록되지 않았어도 경로는 채운다 — 사용자가 찾아볼 자리다.
			if st.UnitPath == "" {
				t.Error("UnitPath 가 비었다")
			}
		})
	}
}

func TestStatus는드리프트를보고한다(t *testing.T) {
	fr := newFakeRunner(nil)
	opts := darwinOptions(t, fr)
	m := newTestManager(t, opts)
	if _, err := m.Enable(context.Background()); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	// 바이너리를 옮긴 상황을 흉내낸다 (재enroll 없는 업그레이드).
	moved := opts
	moved.ExecPath = "/opt/homebrew/bin/telemetryctl"
	m2 := newTestManager(t, moved)

	st, err := m2.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Registered {
		t.Fatal("등록돼 있는데 Registered 가 false 다")
	}
	if st.RegisteredExecPath != "/usr/local/bin/telemetryctl" {
		t.Errorf("RegisteredExecPath = %q", st.RegisteredExecPath)
	}
	if !st.ExecPathDrift {
		t.Error("경로가 달라졌는데 ExecPathDrift 가 false 다")
	}
	// 등록된 경로에 파일이 없다 — 크래시 루프의 가장 강한 설명이다.
	if !st.ExecPathMissing {
		t.Error("등록된 경로가 없는데 ExecPathMissing 이 false 다")
	}
}

func TestLinuxStatus는systemd부재를상태로담는다(t *testing.T) {
	// status 는 진단 명령이라 어떤 환경에서도 멈추면 안 된다 (statuslocal.go 의 규칙).
	t.Setenv("XDG_CONFIG_HOME", "")
	fr := newFakeRunner(nil)
	_, look := systemdPresent()
	env := testEnv(t, "linux")

	m := newTestManager(t, Options{
		Env: env, GOOS: "linux", Runner: fr, ExecPath: "/usr/local/bin/telemetryctl",
		Stat: systemdAbsent(), LookPath: look,
	})
	st, err := m.Status(context.Background())
	if err != nil {
		t.Fatalf("Status 가 오류를 냈다: %v", err)
	}
	if st.Supported {
		t.Error("systemd 가 없는데 Supported 다")
	}
	if st.Detail == "" {
		t.Error("사유가 비었다")
	}
	if fr.count() != 0 {
		t.Errorf("systemd 가 없는데 exec 를 %d회 불렀다", fr.count())
	}
}

// Tier 3 — 실제 등록. CI 에 넣지 않는다.
//
// 두 러너 모두 구조적으로 적대적이다: macOS 러너는 UID 에 GUI 로그인 세션이 없어
// `bootstrap gui/$UID` 가 exit 5 로 실패하고, ubuntu 러너는 사용자에게 systemd user
// manager 도 XDG_RUNTIME_DIR 도 없어 "Failed to connect to bus" 가 난다.
// 저장소의 인라인 skip 관례를 따른다 (runtimeinfo_test.go·state_test.go — 테스트에
// 빌드 태그를 쓰지 않는다).
func TestE2E실제등록(t *testing.T) {
	if os.Getenv("PULSEMETRY_E2E_AUTOSTART") != "1" {
		t.Skip("실제 서비스 등록 테스트는 PULSEMETRY_E2E_AUTOSTART=1 일 때만 돈다")
	}
	execPath := os.Getenv("PULSEMETRY_E2E_EXEC")
	if execPath == "" {
		t.Skip("PULSEMETRY_E2E_EXEC 에 설치된 telemetryctl 경로가 필요하다 (go build -o dist/telemetryctl)")
	}
	if !filepath.IsAbs(execPath) {
		t.Fatalf("PULSEMETRY_E2E_EXEC 는 절대 경로여야 한다: %q", execPath)
	}

	env, err := hostenv.Detect()
	if err != nil {
		t.Fatalf("hostenv.Detect: %v", err)
	}
	m, err := New(Options{Env: env, ExecPath: execPath})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	if _, err := m.Enable(ctx); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	t.Cleanup(func() {
		if _, err := m.Disable(ctx); err != nil {
			t.Errorf("Disable: %v", err)
		}
	})

	st, err := m.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Registered {
		t.Error("Enable 했는데 Registered 가 false 다")
	}
	if !st.Loaded {
		t.Errorf("Enable 했는데 Loaded 가 false 다: %s", st.Detail)
	}
}
