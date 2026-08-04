// Package installer 는 manifest 를 읽어 Codex·Claude Code 설정에 적용하고 그 결과를
// 로컬 상태로 남기는 설치 오케스트레이션이다. cmd/pulsemetry 은 이 패키지를 얇게 호출한다.
//
// 이 계층을 main 밖으로 분리한 이유: 설치 흐름을 단위 테스트하기 위해서다 (PROJ-11).
package installer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// StateSchemaVersion 은 로컬 설치 상태 파일의 스키마 버전이다.
const StateSchemaVersion = 2

// State 는 이 장치에 적용된 설치 상태다. uninstall/status/drift 감지의 근거가 된다 (§5.2, §5.3).
//
// 주의: installation_token 같은 비밀은 절대 여기 저장하지 않는다 (§4.5).
// 설치 식별에 필요한 installation_id 만 남긴다.
type State struct {
	StateSchemaVersion int      `json:"state_schema_version"`
	InstallationID     string   `json:"installation_id"`
	ConfigRevision     int      `json:"config_revision"`
	InstallerVersion   string   `json:"installer_version"`
	InstalledAt        string   `json:"installed_at"` // RFC3339 (UTC)
	Targets            []Target `json:"targets"`
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
func SaveState(path string, s *State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("상태 저장 실패 %s: %w", path, err)
	}
	return nil
}

// LoadState 는 상태 파일을 읽는다. 파일이 없으면 (nil, nil) 을 반환한다(미설치 상태).
func LoadState(path string) (*State, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("상태 파싱 실패 %s: %w", path, err)
	}
	return &s, nil
}
