package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/your-org/pulsemetry/internal/contract"
	"github.com/your-org/pulsemetry/internal/installer"
	"github.com/your-org/pulsemetry/internal/otlpdecode"
	"github.com/your-org/pulsemetry/internal/receiver"
	"github.com/your-org/pulsemetry/internal/runtimeinfo"
)

// TestDefaultLocalPortMatchesReceiver 는 두 패키지에 따로 둔 기본 포트가 어긋나지 않는지
// 본다. installer 는 의존성 방향 때문에 receiver 를 import 하지 않으므로 (installer/local.go
// 주석) 이 대조는 양쪽을 다 아는 여기서만 할 수 있다.
func TestDefaultLocalPortMatchesReceiver(t *testing.T) {
	if installer.DefaultLocalPort != receiver.DefaultPort {
		t.Fatalf("installer.DefaultLocalPort = %d, receiver.DefaultPort = %d — 재배선 주소와 실제 수신 포트가 갈린다",
			installer.DefaultLocalPort, receiver.DefaultPort)
	}
}

// TestLocalEndpointMatchesServerEndpoint 는 벤더 설정에 적히는 주소와 수신기가 스스로
// 보고하는 주소가 같은 문자열인지 본다. 둘 중 하나만 127.0.0.1 이 되면 계약 검증이 깨진다.
func TestLocalEndpointMatchesServerEndpoint(t *testing.T) {
	srv, err := receiver.Start(receiver.Options{
		Token:  "ingest",
		Sink:   receiver.SinkFunc(func(context.Context, receiver.Batch) error { return nil }),
		Decode: otlpdecode.Options{InstallationID: "inst_cli"},
	})
	if err != nil {
		t.Skipf("수신기를 띄울 수 없다: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	}()

	if got, want := srv.Endpoint(), installer.LocalEndpoint(srv.Port()); got != want {
		t.Errorf("Server.Endpoint() = %q, installer.LocalEndpoint() = %q", got, want)
	}
	if strings.Contains(srv.Endpoint(), "127.0.0.1") {
		t.Errorf("수신기가 보고하는 endpoint 에 127.0.0.1 이 있다: %q", srv.Endpoint())
	}
}

// ---------------------------------------------------------------------------
// CLI 흐름
// ---------------------------------------------------------------------------

func TestRunLocalArgumentErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "하위 명령 없음", args: nil, want: "enable|disable"},
		{name: "알 수 없는 하위 명령", args: []string{"toggle"}, want: "알 수 없는"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := runLocal(&stdout, &stderr, tt.args); code != 2 {
				t.Errorf("종료 코드 = %d, want 2", code)
			}
			if !strings.Contains(stderr.String(), tt.want) {
				t.Errorf("stderr = %q, want %q 포함", stderr.String(), tt.want)
			}
		})
	}
}

// TestRunLocalRequiresInstall 은 미설치 상태에서 enroll 을 안내하는지 본다.
func TestRunLocalRequiresInstall(t *testing.T) {
	dir := t.TempDir()
	for _, sub := range []string{"enable", "disable"} {
		t.Run(sub, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runLocal(&stdout, &stderr, []string{
				sub, "--state", filepath.Join(dir, "state.json"), "--data-dir", dir,
			})
			if code != 2 {
				t.Errorf("종료 코드 = %d, want 2", code)
			}
			if !strings.Contains(stderr.String(), "설치되어 있지 않습니다") ||
				!strings.Contains(stderr.String(), "enroll") {
				t.Errorf("stderr = %q", stderr.String())
			}
		})
	}
}

// TestRunLocalEnableDisableRoundTrip 은 CLI 왕복 전체를 본다. 데몬을 띄우지 않았으므로
// enable 은 반드시 경고해야 한다 (계획서 「리스크」 1행).
func TestRunLocalEnableDisableRoundTrip(t *testing.T) {
	f := newCLIFixture(t)
	before := mustReadFile(t, f.claudePath)

	var stdout, stderr bytes.Buffer
	if code := runLocal(&stdout, &stderr, append([]string{"enable"}, f.args...)); code != 0 {
		t.Fatalf("enable 종료 코드 = %d\nstderr: %s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"로컬 재배선 켬",
		"http://localhost:4318",
		"원문·tool details",
		"경고: 데몬이 실행 중이 아닙니다",
		"telemetryctl daemon",
		"local disable",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("enable 출력에 %q 가 없다:\n%s", want, out)
		}
	}

	rewired := string(mustReadFile(t, f.claudePath))
	if !strings.Contains(rewired, "http://localhost:4318") {
		t.Errorf("Claude 설정이 재배선되지 않았다:\n%s", rewired)
	}
	if strings.Contains(rewired, "127.0.0.1") {
		t.Errorf("Claude 설정에 127.0.0.1 이 있다:\n%s", rewired)
	}

	stdout.Reset()
	stderr.Reset()
	if code := runLocal(&stdout, &stderr, append([]string{"disable"}, f.args...)); code != 0 {
		t.Fatalf("disable 종료 코드 = %d\nstderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "로컬 재배선 끔") {
		t.Errorf("disable 출력 = %q", stdout.String())
	}
	if !bytes.Equal(before, mustReadFile(t, f.claudePath)) {
		t.Errorf("CLI 왕복이 Claude 설정을 원래대로 돌리지 못했다:\n--- before ---\n%s\n--- after ---\n%s",
			before, mustReadFile(t, f.claudePath))
	}

	// 두 번째 disable 은 아무것도 하지 않고 성공해야 한다.
	stdout.Reset()
	if code := runLocal(&stdout, &stderr, append([]string{"disable"}, f.args...)); code != 0 {
		t.Fatalf("두 번째 disable 종료 코드 = %d", code)
	}
	if !strings.Contains(stdout.String(), "이미 꺼져 있습니다") {
		t.Errorf("두 번째 disable 출력 = %q", stdout.String())
	}
}

// TestRunLocalEnableRejectsGRPC 는 grpc 설치에서 거부되고 설정이 그대로인지 본다.
func TestRunLocalEnableRejectsGRPC(t *testing.T) {
	m := cliManifest()
	m.OTLP.Protocol = "grpc"
	m.OTLP.Compression = ""
	f := newCLIFixtureWith(t, m)
	before := mustReadFile(t, f.claudePath)

	var stdout, stderr bytes.Buffer
	if code := runLocal(&stdout, &stderr, append([]string{"enable"}, f.args...)); code != 1 {
		t.Fatalf("종료 코드 = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "grpc") {
		t.Errorf("stderr = %q, want grpc 를 설명하는 문구", stderr.String())
	}
	if !bytes.Equal(before, mustReadFile(t, f.claudePath)) {
		t.Error("거부됐는데 설정이 바뀌었다")
	}
}

// TestResolveEnablePortAdoptsDaemonPort 는 포트 폴백 재병합 경로를 본다.
// 데몬이 실제로 듣는 포트가 설정값과 다르면 --port 없이도 그 포트를 골라야 한다.
func TestResolveEnablePortAdoptsDaemonPort(t *testing.T) {
	dir := t.TempDir()
	writeRuntimeInfo(t, dir, 51999)

	target := localTarget{DataDir: dir, State: &installer.State{}}

	t.Run("설정값과 다르면 실제 포트를 쓴다", func(t *testing.T) {
		port, note := resolveEnablePort(target, 0)
		if port != 51999 {
			t.Errorf("port = %d, want 51999", port)
		}
		if !strings.Contains(note, "51999") || !strings.Contains(note, "--port") {
			t.Errorf("note = %q, want 무엇을 왜 골랐는지 설명", note)
		}
	})

	t.Run("--port 를 명시하면 그대로 쓴다", func(t *testing.T) {
		port, note := resolveEnablePort(target, 4318)
		if port != 4318 || note != "" {
			t.Errorf("port = %d note = %q, want 4318 / 빈 note", port, note)
		}
	})

	t.Run("runtime.json 이 없으면 설정값", func(t *testing.T) {
		empty := localTarget{DataDir: t.TempDir(), State: &installer.State{}}
		empty.State.Local.ListenPort = 4444
		port, note := resolveEnablePort(empty, 0)
		if port != 4444 || note != "" {
			t.Errorf("port = %d note = %q, want 4444 / 빈 note", port, note)
		}
	})

	t.Run("설정이 비면 기본 포트", func(t *testing.T) {
		empty := localTarget{DataDir: t.TempDir(), State: &installer.State{}}
		port, _ := resolveEnablePort(empty, 0)
		if port != installer.DefaultLocalPort {
			t.Errorf("port = %d, want %d", port, installer.DefaultLocalPort)
		}
	})
}

// TestDaemonRunningDetection 은 "데몬 없음" 판정의 근거를 본다.
func TestDaemonRunningDetection(t *testing.T) {
	t.Run("runtime.json 없음", func(t *testing.T) {
		running, detail := daemonRunning(t.TempDir())
		if running {
			t.Fatal("running = true, want false")
		}
		if !strings.Contains(detail, "runtime.json") {
			t.Errorf("detail = %q", detail)
		}
	})

	t.Run("살아 있는 pid 지만 헬스체크 실패", func(t *testing.T) {
		dir := t.TempDir()
		writeRuntimeInfo(t, dir, 1) // 1번 포트에는 아무도 없다
		running, detail := daemonRunning(dir)
		if running {
			t.Fatal("running = true, want false")
		}
		if !strings.Contains(detail, "헬스체크") {
			t.Errorf("detail = %q, want 헬스체크 실패 설명", detail)
		}
	})
}

// ---------------------------------------------------------------------------
// 헬퍼
// ---------------------------------------------------------------------------

type cliFixture struct {
	claudePath string
	codexPath  string
	args       []string
}

func cliManifest() contract.Manifest {
	return contract.Manifest{
		SchemaVersion:  1,
		ConfigRevision: 2,
		OTLP: contract.OTLP{
			Endpoint: "https://collector.example.com",
			Protocol: "http/protobuf",
		},
		Signals: contract.Signals{Logs: true, Metrics: true},
	}
}

func newCLIFixture(t *testing.T) cliFixture { return newCLIFixtureWith(t, cliManifest()) }

// newCLIFixtureWith 는 enroll 이 끝난 임시 홈을 만든다.
//
// HOME 을 임시 디렉터리로 바꾸는 것이 중요하다. installer 가 백업 디렉터리를 hostenv 에서
// 파생하므로, 그대로 두면 테스트가 개발자의 진짜 홈에 백업 파일을 뿌린다.
func newCLIFixtureWith(t *testing.T, m contract.Manifest) cliFixture {
	t.Helper()
	keyring.MockInit()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows

	f := cliFixture{
		claudePath: filepath.Join(home, ".claude", "settings.json"),
		codexPath:  filepath.Join(home, ".codex", "config.toml"),
	}
	statePath := filepath.Join(home, ".pulsemetry", "state.json")
	dataDir := filepath.Join(home, ".pulsemetry")
	f.args = []string{"--state", statePath, "--data-dir", dataDir}

	if _, err := installer.Apply(&contract.Enrollment{
		InstallationID:    "inst_cli",
		InstallationToken: "pit_secret",
		TelemetryToken:    "company-telemetry-token",
		Manifest:          m,
	}, installer.Options{
		ClaudePath: f.claudePath,
		CodexPath:  f.codexPath,
		StatePath:  statePath,
		BackupDir:  filepath.Join(home, "backups"),
		ServerURL:  "https://enroll.example.com",
	}); err != nil {
		t.Fatalf("installer.Apply: %v", err)
	}
	return f
}

func writeRuntimeInfo(t *testing.T, dataDir string, port int) {
	t.Helper()
	if err := runtimeinfo.Write(runtimeinfo.PathIn(dataDir), runtimeinfo.Info{
		PID:          os.Getpid(), // 살아 있는 pid 라야 Stale 이 아니다
		Endpoint:     installer.LocalEndpoint(port),
		ListenPort:   port,
		ListenAddrs:  []string{"127.0.0.1:" + strconv.Itoa(port)},
		DataDir:      dataDir,
		DatabasePath: filepath.Join(dataDir, "pulsemetry.db"),
	}); err != nil {
		t.Fatalf("runtimeinfo.Write: %v", err)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}
