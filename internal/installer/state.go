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
// 4 (PROJ-36): Local 블록 신설. 3 이하로 기록된 파일은 LoadState 가 읽으면서
// migrateState 로 올린다 — 이미 설치된 사용자가 바이너리만 갈아도 재enroll 없이
// 동작해야 한다.
const StateSchemaVersion = 4

// DefaultRetentionDays 는 Local.RetentionDays 기본값이다 (Settings 「데이터 보존 기간」).
//
// store.DefaultEventRetentionDays 와 같은 값이지만 여기에 따로 둔다. installer 가
// store 를 import 하면 enroll·status 경로까지 SQLite 드라이버를 끌고 들어오기 때문이다.
// 두 값이 어긋나면 daemon 패키지의 테스트가 잡는다.
const DefaultRetentionDays = 30

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
	// Enabled 는 벤더 설정을 로컬 수신기로 재배선했는지다. 기본 OFF, opt-in 이다.
	// 이 값을 켜는 것은 12단계 `local enable` 의 몫이고, 데몬은 읽기만 한다.
	Enabled bool `json:"enabled"`
	// ListenPort 는 수신기가 잡기를 원하는 포트다. 0 이면 receiver.DefaultPort.
	ListenPort int `json:"listen_port,omitempty"`
	// DataDir 는 SQLite·runtime.json 이 놓일 디렉터리다. 비우면 store.DefaultDataDir.
	DataDir string `json:"data_dir,omitempty"`
	// RetentionDays 는 events·event_content·tool_events 보존일이다.
	RetentionDays int `json:"retention_days,omitempty"`
	// StoreContent 는 원문 로컬 보관 여부다. 기본 ON, opt-out 이다 (ADR 0003).
	// omitempty 를 붙이면 안 된다 — false 가 사라지면 다음 읽기에서 기본값 ON 으로
	// 되살아나 사용자가 끈 프라이버시 설정이 조용히 풀린다.
	StoreContent bool `json:"store_content"`
}

// DefaultLocal 은 새 설치와 3→4 마이그레이션이 쓰는 기본값이다.
func DefaultLocal() Local {
	return Local{
		Enabled:       false, // 재배선은 opt-in, 기본 OFF (계획서 「확정된 결정」)
		RetentionDays: DefaultRetentionDays,
		StoreContent:  true, // 원문 보관 기본 ON (ADR 0003)
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
	// 뜻이다 (ADR 0003). RetentionDays 도 0 이면 보존 기간이 0일로 읽힐 수 있다.
	// 그래서 없던 블록은 반드시 명시적 기본값으로 채운다.
	if s.StateSchemaVersion < 4 {
		s.Local = DefaultLocal()
	}

	s.StateSchemaVersion = StateSchemaVersion
	return true
}
