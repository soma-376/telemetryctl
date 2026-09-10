package config

import (
	"bytes"
	"encoding/json"
	"github.com/BurntSushi/toml"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedClaudePreservesUserChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	m := companyManifest()
	m.OTLP.Endpoint = "http://localhost:4318"
	r, err := MergeClaude(path, m, "private-token", false)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	var root map[string]any
	_ = json.Unmarshal(b, &root)
	root["model"] = "user-model"
	env := root["env"].(map[string]any)
	env["OTEL_EXPORTER_OTLP_ENDPOINT"] = "https://user.example"
	env["USER_VAR"] = "keep"
	hooks := root["hooks"].(map[string]any)
	groups := hooks["SessionEnd"].([]any)
	user := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "user-hook"}}}
	hooks["SessionEnd"] = append([]any{user}, groups...)
	b, _ = json.Marshal(root)
	_ = os.WriteFile(path, b, 0o600)
	e, err := PlanManagedRemoval("claude", path, r.ManagedEntries)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(e.After, []byte("private-token")) || !bytes.Contains(e.After, []byte("user-hook")) || !bytes.Contains(e.After, []byte("https://user.example")) {
		t.Fatalf("제거·보존 결과가 잘못됨: %s", e.After)
	}
	if err = e.Apply(); err != nil {
		t.Fatal(err)
	}
	second, err := PlanManagedRemoval("claude", path, r.ManagedEntries)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(second.Before, second.After) {
		t.Fatal("재실행이 설정을 다시 변경했다")
	}
}

func TestManagedPreservesUnrelatedTOMLTypesAndLargeJSONNumber(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	m := companyManifest()
	r, err := MergeCodex(path, m, "token", false)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	b = append([]byte("user_date = 1979-05-27T07:32:00Z\nuser_number = 9223372036854775807\n"), b...)
	_ = os.WriteFile(path, b, 0o600)
	e, err := PlanManagedRemoval("codex", path, r.ManagedEntries)
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]any
	_, _ = toml.Decode(string(b), &before)
	_, err = toml.Decode(string(e.After), &after)
	if err != nil {
		t.Fatal(err)
	}
	if fingerprint(before["user_date"]) != fingerprint(after["user_date"]) || fingerprint(before["user_number"]) != fingerprint(after["user_number"]) {
		t.Fatal("사용자 TOML 타입·값 변형")
	}
	path = filepath.Join(t.TempDir(), "settings.json")
	r, err = MergeClaude(path, m, "token", false)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	b = bytes.Replace(b, []byte("{"), []byte("{\"large\":9223372036854775807,"), 1)
	_ = os.WriteFile(path, b, 0o600)
	e, err = PlanManagedRemoval("claude", path, r.ManagedEntries)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(e.After, []byte("9223372036854775807")) {
		t.Fatal("JSON 정밀도 유실")
	}
}

func TestManagedCodexPreservesNestedFieldsAndSharedToggle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	m := companyManifest()
	m.OTLP.Endpoint = "http://localhost:4318"
	r, err := MergeCodex(path, m, "secret", false)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	b = append(b, []byte("\n[[hooks.UserEvent]]\n[[hooks.UserEvent.hooks]]\ntype = 'command'\ncommand = 'user-hook'\n")...)
	// 리프 키로 기록해야 사용자가 exporter에 추가한 필드도 살아남는다.
	b = bytes.Replace(b, []byte("[otel.exporter.otlp-http]"), []byte("[otel.exporter.otlp-http]\nuser_option = 'keep'"), 1)
	_ = os.WriteFile(path, b, 0o600)
	e, err := PlanManagedRemoval("codex", path, r.ManagedEntries)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(e.After, []byte("user_option")) || !bytes.Contains(e.After, []byte("user-hook")) || !bytes.Contains(e.After, []byte("hooks = true")) {
		t.Fatalf("사용자 항목 유실: %s", e.After)
	}
	if bytes.Contains(e.After, []byte("hook codex")) || bytes.Contains(e.After, []byte("secret")) {
		t.Fatal("관리 항목이 남았다")
	}
}

func TestManagedModifiedHookAndGroupArePreserved(t *testing.T) {
	for _, change := range []string{"handler", "group"} {
		t.Run(change, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			m := companyManifest()
			m.OTLP.Endpoint = "http://localhost:4318"
			r, err := MergeClaude(path, m, "secret", false)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := os.ReadFile(path)
			var root map[string]any
			_ = json.Unmarshal(b, &root)
			g := root["hooks"].(map[string]any)["SessionEnd"].([]any)[0].(map[string]any)
			if change == "group" {
				g["matcher"] = "user"
			} else {
				g["hooks"].([]any)[0].(map[string]any)["timeout"] = 99
			}
			b, _ = json.Marshal(root)
			_ = os.WriteFile(path, b, 0o600)
			e, err := PlanManagedRemoval("claude", path, r.ManagedEntries)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(e.After, []byte("SessionEnd")) {
				t.Fatal("수정된 훅을 삭제했다")
			}
		})
	}
}

func TestManagedMissingRecordAndConcurrentEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	r, err := MergeClaude(path, companyManifest(), "secret", false)
	if err != nil {
		t.Fatal(err)
	}
	e, err := PlanManagedRemoval("claude", path, nil)
	if err == nil || e != nil {
		t.Fatal("관리 기록 없이 제거 계획이 생성됨")
	}
	e, err = PlanManagedRemoval("claude", path, r.ManagedEntries)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(path, []byte(`{"model":"new"}`), 0o600)
	if e.Apply() == nil {
		t.Fatal("동시 편집을 덮어썼다")
	}
	_ = os.WriteFile(path, []byte(`{"token":"SECRET", invalid`), 0o600)
	_, err = PlanManagedRemoval("claude", path, r.ManagedEntries)
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatal("파싱 오류에 비밀이 노출됐다")
	}
}

func TestManagedRecordHasNoPlaintextAndRejectsArbitraryKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	r, err := MergeClaude(path, companyManifest(), "SECRET", false)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(r.ManagedEntries)
	if bytes.Contains(b, []byte("SECRET")) {
		t.Fatal("지문에 원문이 포함됐다")
	}
	_, err = PlanManagedRemoval("claude", path, []ManagedEntry{{Path: []string{"model", "name"}, Digest: fingerprint("x")}})
	if err == nil {
		t.Fatal("임의 키 삭제 기록을 허용했다")
	}
}
