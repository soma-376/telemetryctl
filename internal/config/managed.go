package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// ManagedEntry는 값 원문 없이 마지막 적용 항목을 식별한다. Path는 점을 포함한 키도 보존한다.
type ManagedEntry struct {
	Path   []string `json:"path,omitempty"`
	Event  string   `json:"event,omitempty"`
	Digest string   `json:"digest"`
}

type ManagedCheck struct {
	Key    string `json:"key"`
	Status string `json:"status"` // same, changed, missing, shared
}

// ManagedEdit는 dry-run과 실제 쓰기가 공유하는 검사 결과다. 원문은 메모리에만 둔다.
type ManagedEdit struct {
	Path    string
	Checks  []ManagedCheck
	Before  []byte
	After   []byte
	Existed bool
}

func fingerprint(v any) string {
	b, _ := json.Marshal(v) // 파서가 만든 JSON 호환 값만 사용한다.
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func managedArray(value any) []any {
	switch v := value.(type) {
	case []any:
		return v
	case []map[string]any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = item
		}
		return out
	default:
		return nil
	}
}

// captureManaged는 방금 생성한 관리 키만 기록한다. 사용자 추가 키를 사후 입양하지 않는다.
func captureManaged(root map[string]any, keys []string) []ManagedEntry {
	entries := make([]ManagedEntry, 0)
	var leaves func([]string, any)
	leaves = func(path []string, v any) {
		if m, ok := v.(map[string]any); ok && len(m) > 0 {
			for k, val := range m {
				leaves(append(append([]string{}, path...), k), val)
			}
			return
		}
		entries = append(entries, ManagedEntry{Path: path, Digest: fingerprint(v)})
	}
	for _, key := range keys {
		if strings.HasPrefix(key, "hooks.") {
			event, _, _ := strings.Cut(strings.TrimPrefix(key, "hooks."), ":")
			hooks, _ := root["hooks"].(map[string]any)
			groups := managedArray(hooks[event])
			// 병합기는 새 관리 handler를 마지막 그룹으로 추가한다.
			if len(groups) == 0 {
				continue
			}
			group, _ := groups[len(groups)-1].(map[string]any)
			handlers := managedArray(group["hooks"])
			if len(handlers) == 1 {
				entries = append(entries, ManagedEntry{Event: event, Digest: fingerprint(hookValue(group, handlers[0]))})
			}
			continue
		}
		path := strings.Split(key, ".")
		if v, found := managedValue(root, path); found {
			leaves(path, v)
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entryKey(entries[i]) < entryKey(entries[j]) })
	return entries
}

func hookValue(group map[string]any, handler any) any {
	meta := map[string]any{}
	for k, v := range group {
		if k != "hooks" {
			meta[k] = v
		}
	}
	return map[string]any{"group": meta, "handler": handler}
}

func managedValue(root map[string]any, path []string) (any, bool) {
	var v any = root
	for _, part := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		v, ok = m[part]
		if !ok {
			return nil, false
		}
	}
	return v, true
}

func deleteManaged(root map[string]any, path []string) {
	if len(path) == 0 {
		return
	}
	if len(path) == 1 {
		delete(root, path[0])
		return
	}
	m, ok := root[path[0]].(map[string]any)
	if !ok {
		return
	}
	deleteManaged(m, path[1:])
	if len(m) == 0 {
		delete(root, path[0])
	}
}

func entryKey(e ManagedEntry) string {
	if e.Event != "" {
		return "hooks." + e.Event
	}
	return strings.Join(e.Path, ".")
}

func validManaged(tool string, e ManagedEntry) bool {
	if len(e.Digest) != 64 {
		return false
	}
	if _, err := hex.DecodeString(e.Digest); err != nil {
		return false
	}
	if e.Event != "" {
		return len(e.Path) == 0 && (e.Event == "SessionStart" || e.Event == "SessionEnd")
	}
	if len(e.Path) < 2 {
		return false
	}
	if tool == "claude" && e.Path[0] == "env" && len(e.Path) == 2 {
		for _, k := range claudeManagedEnvKeys {
			if e.Path[1] == k {
				return true
			}
		}
	}
	if tool == "codex" {
		if len(e.Path) == 2 && e.Path[0] == "features" && e.Path[1] == "hooks" {
			return true
		}
		if e.Path[0] == "otel" {
			for _, k := range codexManagedOTelKeys {
				if e.Path[1] == k {
					return true
				}
			}
		}
	}
	return false
}

// PlanManagedRemoval은 설정을 쓰지 않는다. 파싱 오류에 원문이나 비밀을 붙이지 않는다.
func PlanManagedRemoval(tool, path string, entries []ManagedEntry) (*ManagedEdit, error) {
	if tool != "claude" && tool != "codex" {
		return nil, errors.New("지원하지 않는 설정 대상")
	}
	b, existed, err := readFileIfExists(path)
	if err != nil {
		return nil, err
	}
	root := map[string]any{}
	if existed && len(b) > 0 {
		switch tool {
		case "claude":
			decoder := json.NewDecoder(bytes.NewReader(b))
			decoder.UseNumber()
			err = decoder.Decode(&root)
			if !json.Valid(b) {
				err = errors.New("JSON 형식 오류")
			}
		case "codex":
			err = toml.Unmarshal(b, &root)
		default:
			return nil, errors.New("지원하지 않는 설정 대상")
		}
		if err != nil || root == nil {
			return nil, fmt.Errorf("%s 설정 파싱 실패 (원문 생략)", tool)
		}
	}
	edit := &ManagedEdit{Path: path, Before: b, After: b, Existed: existed}
	if entries == nil {
		return nil, errors.New("관리 항목 기록이 없다. 명시적 재배선이 필요하다")
	}

	// 훅 제거를 먼저 수행해야 기능 토글의 공유 여부를 판단할 수 있다.
	sorted := append([]ManagedEntry{}, entries...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Event != "" && sorted[j].Event == "" })
	changed := false
	seenEntries := map[string]bool{}
	for _, e := range sorted {
		if !validManaged(tool, e) {
			return nil, errors.New("관리 기록에 허용되지 않은 항목이 있다")
		}
		identityBytes, _ := json.Marshal(struct {
			Path  []string
			Event string
		}{e.Path, e.Event})
		identity := string(identityBytes)
		if seenEntries[identity] {
			return nil, errors.New("중복된 관리 항목 기록")
		}
		seenEntries[identity] = true
		status := "missing"
		if e.Event != "" {
			hooks, _ := root["hooks"].(map[string]any)
			groups := managedArray(hooks[e.Event])
			matched := false
			for gi, g := range groups {
				group, ok := g.(map[string]any)
				if !ok {
					continue
				}
				handlers := managedArray(group["hooks"])
				for hi, h := range handlers {
					if fingerprint(hookValue(group, h)) == e.Digest {
						handlers = append(handlers[:hi], handlers[hi+1:]...)
						if len(handlers) == 0 && len(group) == 1 {
							groups = append(groups[:gi], groups[gi+1:]...)
						} else {
							group["hooks"] = handlers
						}
						matched = true
						changed = true
						status = "same"
						break
					}
					// 식별이 불가능해진 수정도 이벤트에 다른 handler가 남아 있으면 보수적으로 알린다.
					status = "changed"
				}
				if matched {
					break
				}
			}
			if matched {
				if len(groups) == 0 {
					delete(hooks, e.Event)
				} else {
					hooks[e.Event] = groups
				}
				if len(hooks) == 0 {
					delete(root, "hooks")
				}
			}
		} else if v, found := managedValue(root, e.Path); found {
			status = "changed"
			if fingerprint(v) == e.Digest {
				status = "same"
				if entryKey(e) == "features.hooks" && root["hooks"] != nil {
					status = "shared"
				} else {
					deleteManaged(root, e.Path)
					changed = true
				}
			}
		}
		edit.Checks = append(edit.Checks, ManagedCheck{entryKey(e), status})
	}
	if !changed {
		return edit, nil
	}
	if tool == "claude" {
		edit.After, err = json.MarshalIndent(root, "", "  ")
		edit.After = append(edit.After, '\n')
	} else {
		var out bytes.Buffer
		err = toml.NewEncoder(&out).Encode(root)
		edit.After = out.Bytes()
	}
	return edit, err
}

// Verify는 dry-run/확인 이후 변경된 파일을 덮어쓰지 않도록 직전에 검사한다.
func (e *ManagedEdit) Verify() error {
	b, existed, err := readFileIfExists(e.Path)
	if err != nil {
		return err
	}
	if existed != e.Existed || !bytes.Equal(b, e.Before) {
		return fmt.Errorf("설정이 검사 후 변경됐다: %s", e.Path)
	}
	return nil
}

func (e *ManagedEdit) Apply() error {
	if err := e.Verify(); err != nil {
		return err
	}
	if bytes.Equal(e.Before, e.After) {
		return nil
	}
	return AtomicWriteFile(e.Path, e.After, 0o600)
}

// Restore는 우리가 쓴 결과가 그대로일 때만 직전 원문을 복구한다.
func (e *ManagedEdit) Restore() error {
	b, err := os.ReadFile(e.Path)
	if err != nil {
		return err
	}
	if !bytes.Equal(b, e.After) {
		return errors.New("복구 전에 설정이 변경되어 자동 복구를 중단했다")
	}
	return AtomicWriteFile(e.Path, e.Before, 0o600)
}
