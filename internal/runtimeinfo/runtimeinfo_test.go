package runtimeinfo

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func sample() Info {
	return Info{
		PID:          os.Getpid(),
		Endpoint:     "http://localhost:4318",
		ListenPort:   4318,
		ListenAddrs:  []string{"127.0.0.1:4318", "[::1]:4318"},
		DataDir:      "/home/dev/.pulsemetry",
		DatabasePath: "/home/dev/.pulsemetry/pulsemetry.db",
		Version:      "0.1.0",
	}
}

func TestWriteReadRoundTrip(t *testing.T) {
	path := PathIn(t.TempDir())
	want := sample()

	if err := Write(path, want); err != nil {
		t.Fatal(err)
	}
	got, found, err := Read(path)
	if err != nil || !found {
		t.Fatalf("Read = (found=%v, err=%v)", found, err)
	}
	if got.SchemaVersion != SchemaVersion {
		t.Errorf("schema_version = %d, want %d", got.SchemaVersion, SchemaVersion)
	}
	if got.StartedAt == "" {
		t.Error("started_at 이 비어 있다")
	}
	if got.PID != want.PID || got.ListenPort != want.ListenPort || got.Endpoint != want.Endpoint {
		t.Errorf("왕복 결과가 다르다: %+v", got)
	}
	if len(got.ListenAddrs) != 2 {
		t.Errorf("listen_addrs = %v", got.ListenAddrs)
	}
	if got.DatabasePath != want.DatabasePath {
		t.Errorf("database_path = %q", got.DatabasePath)
	}
}

func TestPathIn(t *testing.T) {
	if got := PathIn("/data"); got != filepath.Join("/data", FileName) {
		t.Errorf("PathIn = %q", got)
	}
}

// runtime.json 은 GUI 도 읽고 사용자도 열어 보는 파일이다. 비밀이 들어갈 자리가
// 하나라도 생기면 그 순간 토큰이 평문으로 디스크에 남는다.
//
// 필드 이름을 allowlist 로 못박는 이유: 나중에 누군가 Info 에 Token 필드를 더하면
// "그 값이 유출돼도 무해한가" 를 묻지 않고도 컴파일이 통과한다. 이 테스트가 그 질문을 대신한다.
func TestRuntimeJSONHasNoSecretFields(t *testing.T) {
	allowed := map[string]struct{}{
		"schema_version": {}, "pid": {}, "started_at": {},
		"endpoint": {}, "listen_port": {}, "listen_addrs": {},
		"data_dir": {}, "database_path": {}, "version": {},
	}

	path := PathIn(t.TempDir())
	if err := Write(path, sample()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for name := range fields {
		if _, ok := allowed[name]; !ok {
			t.Errorf("runtime.json 에 허용되지 않은 필드 %q 가 있다 — 비밀이 아닌지 검토하고 allowlist 에 추가하라", name)
		}
		lower := strings.ToLower(name)
		for _, banned := range []string{"token", "secret", "password", "credential", "auth"} {
			if strings.Contains(lower, banned) {
				t.Errorf("runtime.json 필드 %q 가 비밀처럼 보인다. 비밀은 키링에만 둔다", name)
			}
		}
	}
}

// ingest 토큰이 어떤 경로로도 파일에 실리지 않는지 본문 바이트로 확인한다.
func TestIngestTokenNeverAppearsInFile(t *testing.T) {
	const token = "SUPER-SECRET-INGEST-TOKEN-abcdef0123456789"

	path := PathIn(t.TempDir())
	info := sample()
	if err := Write(path, info); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), token) {
		t.Fatal("runtime.json 에 토큰이 들어 있다")
	}
	if strings.Contains(strings.ToLower(string(raw)), "bearer") {
		t.Fatal("runtime.json 에 인증 헤더 흔적이 있다")
	}
}

// 데몬이 자주 다시 쓰는 파일이라 권한이 느슨해지면 오래 노출된다.
func TestFilePermissionsAre0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 는 POSIX 권한 비트를 쓰지 않는다")
	}
	path := PathIn(t.TempDir())
	if err := Write(path, sample()); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mode().Perm(); got != fs.FileMode(0o600) {
		t.Errorf("권한 = %v, want 0600", got)
	}
}

// 미실행은 오류가 아니라 정상 상태다. GUI 의 ServiceStartup 이 error 를 받으면
// 앱 기동 자체가 중단된다 (계획서 「GUI 연동 형태」).
func TestReadMissingFileIsNotAnError(t *testing.T) {
	got, found, err := Read(PathIn(t.TempDir()))
	if err != nil {
		t.Fatalf("미실행 상태 조회: %v", err)
	}
	if found {
		t.Fatalf("없는 파일을 찾았다고 한다: %+v", got)
	}

	status, err := Load(PathIn(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if status.Found || status.Stale {
		t.Fatalf("Load = %+v, want 빈 상태", status)
	}
}

func TestReadCorruptFileIsAnError(t *testing.T) {
	path := PathIn(t.TempDir())
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"pid": `), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Read(path); err == nil {
		t.Fatal("부분 기록된 runtime.json 이 조용히 통과했다")
	}
}

func TestWriteRejectsIncompleteInfo(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Info)
	}{
		{name: "pid 없음", mutate: func(i *Info) { i.PID = 0 }},
		{name: "pid 음수", mutate: func(i *Info) { i.PID = -1 }},
		{name: "포트 없음", mutate: func(i *Info) { i.ListenPort = 0 }},
		{name: "endpoint 없음", mutate: func(i *Info) { i.Endpoint = "" }},
		{name: "주소 없음", mutate: func(i *Info) { i.ListenAddrs = nil }},
		{name: "데이터 디렉터리 없음", mutate: func(i *Info) { i.DataDir = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := PathIn(t.TempDir())
			info := sample()
			tt.mutate(&info)
			if err := Write(path, info); err == nil {
				t.Fatal("불완전한 Info 가 기록됐다")
			}
			if _, found, _ := Read(path); found {
				t.Fatal("거부됐는데 파일이 남았다")
			}
		})
	}
}

// 낡은 runtime.json 판별. pid 는 "확실히 죽었다" 만 신뢰할 수 있으므로,
// 여기서는 그 방향(죽은 pid → Stale)을 확정적으로 검증한다.
func TestLoadDetectsStaleFile(t *testing.T) {
	dir := t.TempDir()

	live := PathIn(dir)
	if err := Write(live, sample()); err != nil {
		t.Fatal(err)
	}
	status, err := Load(live)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Found {
		t.Fatal("파일을 찾지 못했다")
	}
	if status.Stale {
		t.Fatal("현재 프로세스의 pid 를 죽었다고 판정했다")
	}

	stalePath := PathIn(t.TempDir())
	dead := sample()
	dead.PID = deadPID(t)
	if err := Write(stalePath, dead); err != nil {
		t.Fatal(err)
	}
	status, err = Load(stalePath)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Found {
		t.Fatal("파일을 찾지 못했다")
	}
	if !status.Stale {
		t.Fatalf("죽은 pid %d 를 살아 있다고 판정했다", dead.PID)
	}
}

func TestProcessAliveRejectsNonPositivePID(t *testing.T) {
	for _, pid := range []int{0, -1, -12345} {
		if (Info{PID: pid}).ProcessAlive() {
			t.Errorf("pid %d 를 살아 있다고 판정했다", pid)
		}
	}
}

func TestRemoveIsIdempotent(t *testing.T) {
	path := PathIn(t.TempDir())
	if err := Write(path, sample()); err != nil {
		t.Fatal(err)
	}
	if err := Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := Read(path); found {
		t.Fatal("Remove 후에도 파일이 있다")
	}
	// graceful shutdown 이 두 번 불려도 실패하면 안 된다.
	if err := Remove(path); err != nil {
		t.Fatalf("없는 파일 삭제: %v", err)
	}
}

// deadPID 는 확실히 존재하지 않는 pid 를 찾는다. 자식 프로세스를 띄우고 회수하면
// 그 번호는 (재사용 전까지) 비어 있다.
func deadPID(t *testing.T) int {
	t.Helper()
	// 커널 pid 상한을 넘는 번호는 어떤 프로세스도 가질 수 없다.
	// Linux 기본 상한은 32768, macOS 는 99998 이다.
	const impossible = 4194305 // Linux pid_max 의 이론적 최대치보다 크다
	if (Info{PID: impossible}).ProcessAlive() {
		t.Skipf("pid %d 가 살아 있다고 나온다 — 이 환경에서는 죽은 pid 를 만들 수 없다", impossible)
	}
	return impossible
}
