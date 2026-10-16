package autostart

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/your-org/pulsemetry/internal/hostenv"
)

// 이 패키지의 테스트는 **하나도 빠짐없이** t.TempDir() 를 홈으로 주입한다.
// New 가 빈 HomeDir 를 거부하므로 그 규칙을 잊으면 컴파일이 아니라 테스트가 실패한다 —
// 개발자의 진짜 ~/Library/LaunchAgents 에 등록물이 생기는 일은 절대 없어야 한다.

type call struct {
	Name string
	Args []string
}

func (c call) key() string {
	return strings.Join(append([]string{c.Name}, c.Args...), " ")
}

// scriptedReply 는 명령 하나에 대한 응답이다. 등록하지 않은 명령은 성공(exit 0)이다.
type scriptedReply struct {
	Stdout string
	Stderr string
	Code   int
	Err    error
}

type fakeRunner struct {
	mu      sync.Mutex
	calls   []call
	replies map[string]scriptedReply
	// onCall 은 호출 **시점의** 파일 시스템을 들여다볼 때 쓴다.
	// (bootstrap 을 부를 때 plist 가 이미 디스크에 있는가 같은 것)
	onCall func(c call)
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) (string, string, int, error) {
	c := call{Name: name, Args: append([]string(nil), args...)}
	f.mu.Lock()
	f.calls = append(f.calls, c)
	f.mu.Unlock()
	if f.onCall != nil {
		f.onCall(c)
	}
	if r, ok := f.replies[c.key()]; ok {
		return r.Stdout, r.Stderr, r.Code, r.Err
	}
	return "", "", 0, nil
}

// keys 는 기록된 호출을 "name arg…" 문자열로 돌려준다. 순서 단언에 쓴다.
func (f *fakeRunner) keys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.calls))
	for _, c := range f.calls {
		out = append(out, c.key())
	}
	return out
}

func (f *fakeRunner) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func newFakeRunner(replies map[string]scriptedReply) *fakeRunner {
	if replies == nil {
		replies = map[string]scriptedReply{}
	}
	return &fakeRunner{replies: replies}
}

// testEnv 는 임시 홈을 가진 Env 다.
func testEnv(t *testing.T, goos string) hostenv.Env {
	t.Helper()
	return hostenv.Env{OS: goos, HomeDir: t.TempDir()}
}

// systemdPresent·systemdAbsent 는 리눅스 백엔드의 1·2단계 감지를 고정한다.
// 이것이 없으면 리눅스 테스트가 러너의 진짜 /run/systemd/system 에 좌우된다.
func systemdPresent() (func(string) (os.FileInfo, error), func(string) (string, error)) {
	stat := func(string) (os.FileInfo, error) { return os.Stat(os.TempDir()) }
	look := func(string) (string, error) { return "/usr/bin/systemctl", nil }
	return stat, look
}

func systemdAbsent() func(string) (os.FileInfo, error) {
	return func(string) (os.FileInfo, error) { return nil, fs.ErrNotExist }
}

// newTestManager 는 주입된 홈·Runner 를 가진 Manager 를 만든다.
//
// ExecPath 를 반드시 채운다. `go test` 의 테스트 바이너리는 임시 디렉터리에 있어
// resolveExecPath 가 ErrExecPathVolatile 로 거부하기 때문이다 — 그 거부는 의도된 동작이고
// 별도 테스트가 확인한다.
func newTestManager(t *testing.T, opts Options) *Manager {
	t.Helper()
	if opts.ExecPath == "" {
		opts.ExecPath = "/usr/local/bin/telemetryctl"
	}
	m, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

// mustWrite 는 테스트 픽스처 파일을 쓴다.
func mustWrite(t *testing.T, path string, content []byte, perm fs.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, content, perm); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}
