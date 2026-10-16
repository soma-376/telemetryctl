// Package runtimeinfo 는 실행 중인 데몬의 좌표를 데이터 디렉터리의 runtime.json 으로
// 남긴다. status 명령과 GUI 가 "데몬이 지금 어디서 듣고 있는가" 를 알아내는 유일한 수단이다
// (계획서 「패키지 레이아웃」 — "runtime.json (비밀 없음: 주소·pid·데이터 경로)").
//
// # 비밀은 들어가지 않는다
//
// loopback ingest 토큰은 키링(credential.AccountLocalIngest)에만 있고 이 파일에는 담기지
// 않는다. state.json 이 비밀을 두지 않는 것과 같은 규칙이다 (§4.5). runtime.json 은
// state.json 과 달리 GUI 도 읽고 사용자도 열어 보는 파일이라 규칙이 더 강하게 걸린다 —
// 여기 필드를 추가할 때는 「그 값이 유출돼도 무해한가」를 먼저 물어야 하고,
// 그 물음을 자동화한 것이 runtimeinfo_test.go 의 필드 allowlist 테스트다.
//
// # 왜 원자적 쓰기인가
//
// 데몬이 기동 도중 죽으면 부분 기록된 runtime.json 이 남는다. status 가 그 파일을 읽고
// JSON 파싱에 실패하면 "데몬 없음" 으로 오판하거나, 더 나쁘게는 포트만 잘린 채 읽혀
// 엉뚱한 주소로 헬스체크를 보낸다. config.AtomicWriteFile(임시 파일 → fsync → rename)로
// 쓰면 독자는 항상 온전한 이전 버전이나 온전한 새 버전 중 하나만 본다.
package runtimeinfo

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/your-org/pulsemetry/internal/config"
)

// SchemaVersion 은 runtime.json 의 스키마 버전이다. GUI 가 telemetryctl 과 다른 속도로
// 배포되므로, 필드 의미가 바뀌면 여기를 올려 구버전 독자가 조용히 오해하지 않게 한다.
const SchemaVersion = 1

// FileName 은 데이터 디렉터리 안의 파일 이름이다.
const FileName = "runtime.json"

// PathIn 은 데이터 디렉터리 안의 runtime.json 경로다.
// 데이터 디렉터리는 store.PathIn 이 DB 를 두는 곳과 같다 (~/.pulsemetry).
func PathIn(dataDir string) string { return filepath.Join(dataDir, FileName) }

// Info 는 runtime.json 한 벌이다.
//
// 필드는 전부 "유출돼도 무해한 좌표" 다. 토큰·자격증명·프롬프트 원문을 담을 자리가 없고,
// 앞으로도 만들지 않는다.
type Info struct {
	SchemaVersion int `json:"schema_version"`

	// PID 는 이 파일을 쓴 데몬의 프로세스 ID 다. 낡은 파일 판별의 1차 근거다.
	PID int `json:"pid"`
	// StartedAt 은 데몬 기동 시각(RFC3339, UTC)이다. pid 재사용을 사람이 눈으로
	// 걸러낼 수 있게 남긴다 — 기동 시각이 부팅보다 오래됐다면 그 pid 는 남의 것이다.
	StartedAt string `json:"started_at"`

	// Endpoint 는 벤더 설정에 들어가는 주소다. 반드시 http://localhost:<port> 형태다 —
	// contract.validOTLPEndpoint 가 http:// 를 리터럴 호스트 localhost 에만 허용한다
	// (ADR 0001, internal/contract/manifest.go:88).
	Endpoint string `json:"endpoint"`
	// ListenPort 는 실제로 바인딩된 포트다. 4318 이 사용 중이면 임의 포트로 폴백하므로
	// 기본값을 가정하면 안 된다.
	ListenPort int `json:"listen_port"`
	// ListenAddrs 는 바인딩된 리스너 주소 전부다 (예: 127.0.0.1:4318, [::1]:4318).
	// 리스너가 하나뿐이면 IPv6 loopback 이 없는 환경이라는 뜻이고, status 가 이를 표시한다.
	ListenAddrs []string `json:"listen_addrs"`

	// DataDir 는 데몬이 쓰는 데이터 디렉터리다.
	DataDir string `json:"data_dir"`
	// DatabasePath 는 SQLite 파일 경로다. GUI 가 read-only 로 여는 대상이다.
	DatabasePath string `json:"database_path"`

	// Version 은 데몬 바이너리 버전이다. 비어 있을 수 있다.
	Version string `json:"version,omitempty"`
}

// Validate 는 쓰기 전에 필수 필드를 확인한다. 반쯤 채워진 runtime.json 은 파일이 아예
// 없는 것보다 나쁘다 — 독자가 데몬이 있다고 믿고 닿지 않는 주소로 요청을 보낸다.
func (i Info) Validate() error {
	switch {
	case i.PID <= 0:
		return errors.New("runtime.json: pid 는 양수여야 함")
	case i.ListenPort <= 0:
		return errors.New("runtime.json: listen_port 는 양수여야 함")
	case i.Endpoint == "":
		return errors.New("runtime.json: endpoint 는 비울 수 없음")
	case len(i.ListenAddrs) == 0:
		return errors.New("runtime.json: listen_addrs 는 비울 수 없음")
	case i.DataDir == "":
		return errors.New("runtime.json: data_dir 는 비울 수 없음")
	}
	return nil
}

// Write 는 info 를 path 에 0600 으로 원자적으로 기록한다.
// SchemaVersion 과 StartedAt 은 비어 있으면 채워 준다.
func Write(path string, info Info) error {
	info.SchemaVersion = SchemaVersion
	if info.StartedAt == "" {
		info.StartedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if err := info.Validate(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := config.AtomicWriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("runtime.json 저장 실패 %s: %w", path, err)
	}
	return nil
}

// Read 는 runtime.json 을 읽는다. 파일이 없으면 (Info{}, false, nil) 이다 — 데몬 미실행은
// 오류가 아니라 정상 상태다 (계획서 「GUI 연동 형태」의 "미설치 상태는 error 가 아니다").
func Read(path string) (Info, bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Info{}, false, nil
	}
	if err != nil {
		return Info{}, false, err
	}
	var info Info
	if err := json.Unmarshal(b, &info); err != nil {
		return Info{}, false, fmt.Errorf("runtime.json 파싱 실패 %s: %w", path, err)
	}
	return info, true, nil
}

// Remove 는 runtime.json 을 지운다. graceful shutdown 경로에서 부른다.
// 이미 없으면 성공으로 본다.
func Remove(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Status 는 runtime.json 을 읽고 데몬 생존까지 판정한 결과다.
type Status struct {
	Info  Info
	Found bool
	// Stale 은 파일은 있으나 기록된 pid 가 살아 있지 않다는 뜻이다. 데몬이 크래시하거나
	// SIGKILL 로 죽으면 Remove 가 실행되지 않아 파일이 남는다.
	Stale bool
}

// Load 는 Read 에 생존 판정을 더한다.
//
// # 낡은 파일 판별을 pid 로 하는 이유와 그 한계
//
// 후보는 셋이었다.
//
//	(a) 파일 잠금 — 잠금 의미가 OS 마다 다르고 Windows 에서는 데몬이 파일을 연 채로 두어야
//	    해서 GUI 의 읽기와 부딪힌다 (계획서 리스크 「Windows 에서 GUI 가 파일을 연 채 prune」).
//	(b) 타임스탬프 하트비트 — 데몬이 주기적으로 파일을 다시 써야 하고, 그 주기보다 짧은
//	    다운타임은 못 잡는다. 주기적 쓰기가 원자적 rename 을 계속 유발하는 것도 낭비다.
//	(c) pid 생존 확인 — 쓰기는 기동 시 한 번이면 되고 판정은 독자가 한다.
//
// (c) 를 골랐다. 다만 **pid 는 "확실히 죽었다" 만 신뢰할 수 있는 근거다.** pid 는 재사용되므로
// 살아 있다는 판정은 "그 번호의 프로세스가 존재한다" 이상을 뜻하지 못한다. 따라서
// Stale=true 는 확정이고, Stale=false 는 "아마 살아 있음" 이다. 확정이 필요한 호출자
// (status 명령)는 Endpoint 에 GET /healthz(인증 없음)를 한 번 더 던져 확인한다.
// StartedAt 을 함께 남기는 것도 이 때문이다 — 재사용된 pid 는 기동 시각이 어긋난다.
func Load(path string) (Status, error) {
	info, found, err := Read(path)
	if err != nil || !found {
		return Status{Info: info, Found: found}, err
	}
	return Status{Info: info, Found: true, Stale: !info.ProcessAlive()}, nil
}

// ProcessAlive 는 기록된 pid 의 프로세스가 존재하는지 본다.
//
// 플랫폼별 파일을 나누지 않고 표준 라이브러리의 이식성에 기댄다.
//   - Unix: FindProcess 는 항상 성공하므로 시그널 0 이 실제 판정이다.
//     ESRCH 는 없음, EPERM 은 "있는데 남의 것" 이므로 살아 있는 것으로 본다.
//   - Windows: FindProcess 가 핸들을 열어 보므로 없는 pid 면 여기서 실패한다.
//     핸들이 열렸다면 시그널 0 은 지원되지 않아 오류를 주지만 프로세스는 존재한다.
func (i Info) ProcessAlive() bool {
	if i.PID <= 0 {
		return false
	}
	p, err := os.FindProcess(i.PID)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	switch {
	case err == nil:
		return true
	case errors.Is(err, os.ErrProcessDone), errors.Is(err, syscall.ESRCH):
		return false
	default:
		// EPERM(다른 사용자 소유) 과 Windows 의 "지원하지 않음" 이 여기 온다.
		// 둘 다 프로세스가 존재한다는 뜻이다.
		return true
	}
}
