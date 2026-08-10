package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/your-org/pulsemetry/internal/credential"
	"github.com/your-org/pulsemetry/internal/installer"
	"github.com/your-org/pulsemetry/internal/runtimeinfo"
)

// status 는 진단 명령이다. 미설치·DB 없음·데몬 꺼짐에서 죽으면 정작 진단이 필요한
// 순간에 쓸 수 없다.
func TestStatusWithoutAnything(t *testing.T) {
	keyring.MockInit()
	_, args := tempTarget(t)

	res := runCmd(t, runStatus, args...)
	if res.code != 0 {
		t.Fatalf("code = %d, want 0 (stderr: %s)", res.code, res.stderr)
	}
	res.mustContain(t,
		"미설치 (상태 파일 없음",
		"로컬 파이프라인:",
		"DB: 설정 안 됨",
		"데몬: 실행 안 됨 (runtime.json 없음)",
		"ingest 토큰: 없음",
	)
}

// 계획서가 지정한 규칙: 존재 여부만, 값은 절대 출력하지 않는다.
// 실제로 키링에 토큰을 넣고 출력 전체를 훑어 확인한다.
func TestStatusNeverPrintsSecrets(t *testing.T) {
	keyring.MockInit()

	const (
		ingestToken       = "loopback-ingest-토큰-DEADBEEF0123456789"
		installationToken = "installation-토큰-CAFEBABE9876543210"
	)
	if err := credential.Set(credential.AccountLocalIngest, ingestToken); err != nil {
		t.Fatalf("credential.Set: %v", err)
	}
	if err := credential.SaveInstallation(&credential.Credential{
		InstallationID:    "inst-1",
		InstallationToken: installationToken,
	}); err != nil {
		t.Fatalf("SaveInstallation: %v", err)
	}

	dir, args := tempTarget(t)
	seed(t, dir, time.Now().Add(-30*time.Minute))
	writeState(t, statePathOf(dir), dir)
	writeRuntime(t, dir)

	res := runCmd(t, runStatus, args...)
	if res.code != 0 {
		t.Fatalf("code = %d, want 0 (stderr: %s)", res.code, res.stderr)
	}
	whole := res.stdout + res.stderr
	for _, secret := range []string{ingestToken, installationToken, "DEADBEEF", "CAFEBABE"} {
		if strings.Contains(whole, secret) {
			t.Errorf("출력에 비밀이 들어 있다 (%q):\n%s", secret, whole)
		}
	}
	// 존재 여부는 반드시 알려야 한다 — 없으면 "왜 수신이 안 되나" 를 진단할 수 없다.
	res.mustContain(t, "ingest 토큰: 있음 (OS 키링)", "자격증명: 정상 (OS 키링)")
}

// 설치·데몬·데이터가 모두 있는 상태의 블록. 여기서 나오는 값들이 Settings 화면의 근거다.
func TestStatusLocalBlockWithData(t *testing.T) {
	keyring.MockInit()
	dir, args := tempTarget(t)
	seed(t, dir, time.Now().Add(-30*time.Minute))
	writeState(t, statePathOf(dir), dir)
	writeRuntime(t, dir)

	res := runCmd(t, runStatus, args...)
	if res.code != 0 {
		t.Fatalf("code = %d, want 0 (stderr: %s)", res.code, res.stderr)
	}
	res.mustContain(t,
		"재배선: 꺼짐",
		"수신 포트(설정): 4318",
		"보존: 30일",
		"원문 보관: 켬",
		"데이터 디렉터리: "+dir,
		"스키마 v",
		"데이터: 세션 2개(진행 중 1)",
		"활성 벤더:",
	)
	// runtime.json 의 pid 는 살아 있지 않은 값이라 낡은 파일로 판정돼야 한다.
	res.mustContain(t, "낡은 runtime.json")
}

// 상태 파일이 깨졌으면 조용히 넘기지 않는다 — 설치가 망가졌다는 뜻이다.
func TestStatusRejectsBrokenState(t *testing.T) {
	keyring.MockInit()
	dir, args := tempTarget(t)
	if err := os.WriteFile(statePathOf(dir), []byte("{ 이건 JSON 이 아니다"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	res := runCmd(t, runStatus, args...)
	if res.code != 1 {
		t.Fatalf("code = %d, want 1 (stderr: %s)", res.code, res.stderr)
	}
}

// writeState 는 설치 상태 파일을 만든다. Local 블록은 기본값(재배선 OFF)이다.
func writeState(t *testing.T, path, dataDir string) {
	t.Helper()
	st := &installer.State{
		StateSchemaVersion: installer.StateSchemaVersion,
		InstallationID:     "inst-1",
		ConfigRevision:     3,
		InstallerVersion:   installer.Version,
		InstalledAt:        time.Now().UTC().Format(time.RFC3339),
		Local:              installer.DefaultLocal(),
	}
	st.Local.DataDir = dataDir
	if err := installer.SaveState(path, st); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
}

// writeRuntime 은 runtime.json 을 만든다. pid 는 존재할 수 없는 값이라 status 가
// "낡은 파일" 로 판정해야 한다 — 살아 있는 pid 를 쓰면 테스트가 /healthz 를 향해
// 실제 네트워크 요청을 던진다.
func writeRuntime(t *testing.T, dataDir string) {
	t.Helper()
	err := runtimeinfo.Write(runtimeinfo.PathIn(dataDir), runtimeinfo.Info{
		PID:          maxPID,
		Endpoint:     "http://localhost:4318",
		ListenPort:   4318,
		ListenAddrs:  []string{"127.0.0.1:4318", "[::1]:4318"},
		DataDir:      dataDir,
		DatabasePath: filepath.Join(dataDir, "pulsemetry.db"),
	})
	if err != nil {
		t.Fatalf("runtimeinfo.Write: %v", err)
	}
}

// maxPID 는 어떤 OS 에서도 할당되지 않는 pid 다.
const maxPID = 1 << 30
