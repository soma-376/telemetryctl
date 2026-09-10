package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/your-org/pulsemetry/internal/config"
)

type ManagedSettings struct {
	Version        int             `json:"version"`
	InstallationID string          `json:"installation_id"`
	Targets        []ManagedTarget `json:"targets"`
}

type ManagedTarget struct {
	Tool    string                `json:"tool"`
	Path    string                `json:"path"`
	Entries []config.ManagedEntry `json:"entries"`
}

func ManagedPath(statePath string) string {
	return filepath.Join(filepath.Dir(statePath), "managed-settings.json")
}

func LoadManaged(statePath string, state *State) (*ManagedSettings, error) {
	b, err := os.ReadFile(ManagedPath(statePath))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m ManagedSettings
	if json.Unmarshal(b, &m) != nil || m.Version != 1 || m.InstallationID != state.InstallationID {
		return nil, errors.New("관리 기록 형식·버전·설치 ID 불일치")
	}
	seen := map[string]bool{}
	for _, target := range m.Targets {
		key := target.Tool + "\x00" + target.Path
		if seen[key] {
			return nil, errors.New("중복된 관리 기록 대상")
		}
		seen[key] = true
	}
	return &m, nil
}

func (m *ManagedSettings) entries(t Target) []config.ManagedEntry {
	if m != nil {
		for _, v := range m.Targets {
			if v.Tool == t.Tool && v.Path == t.Path {
				return v.Entries
			}
		}
	}
	return nil
}

// saveManaged는 이번 배선이 생성한 항목만 갱신한다. 상태 저장 실패 시 원래 기록으로 돌린다.
func saveManaged(statePath string, state *State) (func() error, error) {
	noop := func() error { return nil }
	needed := false
	for _, t := range state.Targets {
		if t.pendingManaged != nil {
			needed = true
		}
	}
	if !needed {
		return noop, nil
	}
	old, err := LoadManaged(statePath, state)
	if err != nil {
		return nil, err
	}
	m := ManagedSettings{Version: 1, InstallationID: state.InstallationID}
	for _, t := range state.Targets {
		entries := old.entries(t)
		if t.pendingManaged != nil {
			entries = append([]config.ManagedEntry{}, t.pendingManaged...)
			if t.preserveManagedHooks {
				for _, entry := range old.entries(t) {
					if entry.Event != "" || (len(entry.Path) > 0 && entry.Path[0] != "otel") {
						entries = append(entries, entry)
					}
				}
			}
		}
		if entries != nil {
			m.Targets = append(m.Targets, ManagedTarget{t.Tool, t.Path, entries})
		}
	}
	path := ManagedPath(statePath)
	before, readErr := os.ReadFile(path)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return nil, readErr
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	if err = config.AtomicWriteFile(path, append(b, '\n'), 0o600); err != nil {
		return nil, err
	}
	return func() error {
		if errors.Is(readErr, os.ErrNotExist) {
			return os.Remove(path)
		}
		return config.AtomicWriteFile(path, before, 0o600)
	}, nil
}

type Drift struct{ Tool, Path, Key, Status string }

func DriftRepairCommand(state *State) string {
	if state != nil && state.Local.Enabled {
		return "pulsemetry local enable"
	}
	return "pulsemetry reconnect"
}

// InspectManaged는 읽기 전용이다. 관리 기록 누락은 오류로 알리고 현재 설정을 자동 등록하지 않는다.
func InspectManaged(statePath string, state *State) ([]Drift, error) {
	if state == nil {
		return nil, nil
	}
	m, err := LoadManaged(statePath, state)
	if err != nil {
		return nil, err
	}
	var out []Drift
	for _, t := range state.Targets {
		e, err := config.PlanManagedRemoval(t.Tool, t.Path, m.entries(t))
		if err != nil {
			return nil, fmt.Errorf("%s 설정 검사 실패: %w", t.Tool, err)
		}
		for _, c := range e.Checks {
			if c.Status != "same" && c.Status != "shared" {
				out = append(out, Drift{t.Tool, t.Path, c.Key, c.Status})
			}
		}
	}
	return out, nil
}
