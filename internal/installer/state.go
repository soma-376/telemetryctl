// Package installer 는 manifest 를 읽어 Codex·Claude Code 설정에 적용하고 그 결과를
// 로컬 상태로 남기는 설치 오케스트레이션이다. cmd/telemetryctl 은 이 패키지를 얇게 호출한다.
//
// 이 계층을 main 밖으로 분리한 이유: 설치 흐름을 단위 테스트하기 위해서다 (PROJ-11).
package installer

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/your-org/pulsemetry/internal/config"
	"github.com/your-org/pulsemetry/internal/contract"
)

// StateSchemaVersion 은 로컬 설치 상태 파일의 스키마 버전이다.
//
// 4 (PROJ-36): Local 블록 신설.
// 5 (PROJ-71): 사용자 지정 retention_days 제거. 모든 로컬 데이터는 400일 고정 보존한다.
//
// 이전 버전으로 기록된 파일은 LoadState 가 읽으면서 migrateState 로 올린다 — 이미 설치된
// 사용자가 바이너리만 갈아도 재enroll 없이 동작해야 한다.
const StateSchemaVersion = 5

// Local 은 로컬 파이프라인(수신기·저장소)의 **설정된 의도**다 (PROJ-36).
//
// 여기 있는 값과 데이터 디렉터리의 runtime.json 은 역할이 다르다. 이쪽은 "사용자가
// 이렇게 돌기를 원했다" 이고 runtime.json 은 "실제로 이렇게 돌고 있다" 다. 포트 폴백이
// 일어나면 둘이 갈리는데, 벤더 설정 재병합이 필요한지 판단하는 근거는 **이쪽**이다 —
// 벤더 설정에 적힌 주소가 이 값에서 나왔기 때문이다. 그래서 데몬은 실제 바인딩 포트를
// 여기에 덮어쓰지 않는다. 덮어쓰는 순간 "설정과 현실이 어긋났다"는 신호가 사라진다.
//
// 비밀은 들어가지 않는다 (§4.5). loopback ingest 토큰은 키링
// (credential.AccountLocalIngest)에만 있다.
type Local struct {
	// Enabled 는 벤더 설정을 로컬 수신기로 재배선했는지다. **신규 설치는 기본 ON, opt-out**
	// 이다 (PROJ-45, ADR 0006) — Apply 가 enroll 시점에 배선하고 이 값을 켠다.
	// `local enable`·`local disable` 은 그 뒤 이 값을 뒤집는다. 데몬은 읽기만 한다.
	Enabled bool `json:"enabled"`
	// ListenPort 는 수신기가 잡기를 원하는 포트다. 0 이면 receiver.DefaultPort.
	ListenPort int `json:"listen_port,omitempty"`
	// DataDir 는 SQLite·runtime.json 이 놓일 디렉터리다. 비우면 store.DefaultDataDir.
	DataDir string `json:"data_dir,omitempty"`
	// StoreContent 는 원문 로컬 보관 여부다. 기본 ON, opt-out 이다 (ADR 0003).
	// omitempty 를 붙이면 안 된다 — false 가 사라지면 다음 읽기에서 기본값 ON 으로
	// 되살아나 사용자가 끈 프라이버시 설정이 조용히 풀린다.
	StoreContent bool `json:"store_content"`
}

// DefaultLocal 은 **재배선하지 않은** Local 블록이다.
//
// 쓰이는 곳이 둘인데 의미가 다르다.
//
//	3→4 마이그레이션 — 기존 설치자는 그대로 둔다. 바이너리만 갈았는데 벤더 설정이
//	                   바뀌는 것은 사용자가 내린 적 없는 결정이고, 게다가 여기서
//	                   Enabled 만 켜면 상태는 "로컬" 인데 설정 파일은 회사 직결인
//	                   불일치가 생긴다 (마이그레이션은 메모리에서만 일어난다).
//	Apply 의 폴백    — ingest 토큰이 없거나 grpc 테넌트라 배선하지 못한 경우.
//
// 신규 설치의 기본값은 여기가 아니라 Apply 가 정한다 — 거기서 Enabled·ListenPort 를
// 켜서 opt-out 을 만든다 (PROJ-45, ADR 0006).
func DefaultLocal() Local {
	return Local{
		Enabled:      false,
		StoreContent: true, // 원문 보관 기본 ON (ADR 0003)
	}
}

// State 는 이 장치에 적용된 설치 상태다. uninstall/status/drift 감지의 근거가 된다 (§5.2, §5.3).
//
// 주의: installation_token·telemetry_token 같은 비밀은 절대 여기 저장하지 않는다 (§4.5).
// 재연결에 필요한 서버 URL, manifest, 설정 대상만 비밀이 아닌 상태로 남긴다.
type State struct {
	StateSchemaVersion int               `json:"state_schema_version"`
	InstallationID     string            `json:"installation_id"`
	ServerURL          string            `json:"server_url"`
	ConfigRevision     int               `json:"config_revision"`
	InstallerVersion   string            `json:"installer_version"`
	InstalledAt        string            `json:"installed_at"` // RFC3339 (UTC)
	Manifest           contract.Manifest `json:"manifest"`
	Targets            []Target          `json:"targets"`
	// Local 은 로컬 파이프라인 설정이다 (스키마 4부터).
	Local Local `json:"local"`
}

// Target 은 설정 파일 한 개에 대해 installer 가 한 일이다. ManagedKeys 로 uninstall 이
// 우리가 넣은 키만 제거할 수 있다 (§5.2).
type Target struct {
	Tool           string   `json:"tool"` // "claude" | "codex"
	Path           string   `json:"path"`
	BackupPath     string   `json:"backup_path,omitempty"`
	OriginalSHA256 string   `json:"original_sha256,omitempty"`
	ManagedKeys    []string `json:"managed_keys"`
	Created        bool     `json:"created"`
}

// SaveState 는 상태를 path 에 0600 으로 기록한다. 상위 디렉터리는 0700 으로 생성한다.
//
// 쓰기는 config.AtomicWriteFile 에 맡긴다(임시 파일 → fsync → rename). 데몬이 상태를
// 자주 갱신하게 되므로, 중간에 죽어도 부분 기록된 state.json 이 남아 설치가 깨지는 일이
// 없어야 한다. 상위 디렉터리 0700 생성도 AtomicWriteFile 이 함께 처리한다.
func SaveState(path string, s *State) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := config.AtomicWriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("상태 저장 실패 %s: %w", path, err)
	}
	return nil
}

// LoadState 는 상태 파일을 읽는다. 파일이 없으면 (nil, nil) 을 반환한다(미설치 상태).
//
// 구버전 스키마는 현재 스키마로 올려서 돌려주므로 호출자는 항상 최신 모양만 본다.
// 올림은 메모리 상에서만 일어난다 — 읽기가 디스크를 건드리면 status 같은 조회 명령이
// 파일을 바꾸게 된다. 디스크에 굳히는 것은 LoadStateMigrated 를 쓰는 데몬의 몫이다.
func LoadState(path string) (*State, error) {
	s, _, err := LoadStateMigrated(path)
	return s, err
}

// LoadStateMigrated 는 LoadState 와 같지만 마이그레이션이 실제로 일어났는지도 알려준다.
// 데몬은 migrated 가 true 일 때 한 번만 SaveState 해서 올림을 디스크에 굳힌다.
func LoadStateMigrated(path string) (s *State, migrated bool, err error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, false, fmt.Errorf("상태 파싱 실패 %s: %w", path, err)
	}
	return &st, migrateState(&st), nil
}

// migrateState 는 구버전 상태를 현재 스키마로 올린다. 올렸으면 true 를 반환한다.
//
// 미래 버전(이 바이너리보다 큰 값)은 건드리지 않는다. 신버전 telemetryctl 이 쓴 파일을
// 구버전이 열었을 때 필드를 지우고 되쓰는 것이 가장 나쁜 결과다.
func migrateState(s *State) bool {
	if s == nil || s.StateSchemaVersion >= StateSchemaVersion {
		return false
	}

	// 3 → 4: Local 블록 신설.
	//
	// 제로값을 그대로 두면 안 된다. Local.StoreContent 의 제로값은 false 이고, 그것은
	// 계획서가 "기본 ON, opt-out" 으로 못박은 원문 보관을 업그레이드만으로 꺼 버린다는
	// 뜻이다 (ADR 0003). 그래서 없던 블록은 반드시 명시적 기본값으로 채운다.
	if s.StateSchemaVersion < 4 {
		s.Local = DefaultLocal()
	}
	// 4 → 5: Local.RetentionDays 를 제거했다. encoding/json 은 구버전의 retention_days 를
	// 읽을 때 무시하고, 데몬이 마이그레이션 결과를 저장하면 그 키가 디스크에서도 사라진다.

	s.StateSchemaVersion = StateSchemaVersion
	return true
}
