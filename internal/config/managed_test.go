package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
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

// exporter 테이블은 endpoint·protocol이 한 벌이라 반쪽만 남으면 안 된다.
func TestManagedCodexExporterGroupSurvivesFieldEdit(t *testing.T) {
	for _, field := range []string{"protocol", "endpoint"} {
		t.Run(field, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			m := companyManifest()
			m.OTLP.Endpoint = "http://localhost:4318"
			r, err := MergeCodex(path, m, "secret", false)
			if err != nil {
				t.Fatal(err)
			}
			var root map[string]any
			if _, err = toml.DecodeFile(path, &root); err != nil {
				t.Fatal(err)
			}
			inner := root["otel"].(map[string]any)["exporter"].(map[string]any)["otlp-http"].(map[string]any)
			if field == "protocol" {
				inner["protocol"] = "json"
			} else {
				inner["endpoint"] = "http://localhost:9999"
			}
			var out bytes.Buffer
			if err = toml.NewEncoder(&out).Encode(root); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(path, out.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}

			e, err := PlanManagedRemoval("codex", path, r.ManagedEntries)
			if err != nil {
				t.Fatal(err)
			}
			var after map[string]any
			if _, err = toml.Decode(string(e.After), &after); err != nil {
				t.Fatal(err)
			}
			otel, _ := after["otel"].(map[string]any)
			exporter, _ := otel["exporter"].(map[string]any)
			left, ok := exporter["otlp-http"].(map[string]any)
			if !ok {
				t.Fatalf("수정된 exporter를 통째로 지웠다: %s", e.After)
			}
			for _, key := range []string{"endpoint", "protocol"} {
				if _, ok = left[key]; !ok {
					t.Fatalf("%s 없는 반쪽 exporter가 남았다: %s", key, e.After)
				}
			}
			for _, c := range e.Checks {
				if c.Key == "otel.exporter" && c.Status != "changed" {
					t.Fatalf("그룹 보존을 알리지 않았다: %v", c)
				}
			}
			if _, ok = otel["metrics_exporter"]; ok {
				t.Fatalf("무관한 관리 그룹이 남았다: %s", e.After)
			}
		})
	}
}

func TestManagedCodexExporterMissingLeafAndUserField(t *testing.T) {
	for _, scenario := range []string{"deleted_endpoint", "added_field"} {
		t.Run(scenario, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			m := companyManifest()
			m.OTLP.Endpoint = "http://localhost:4318"
			r, err := MergeCodex(path, m, "secret", false)
			if err != nil {
				t.Fatal(err)
			}
			var root map[string]any
			if _, err = toml.DecodeFile(path, &root); err != nil {
				t.Fatal(err)
			}
			inner := root["otel"].(map[string]any)["exporter"].(map[string]any)["otlp-http"].(map[string]any)
			if scenario == "deleted_endpoint" {
				delete(inner, "endpoint")
			} else {
				inner["user_option"] = "keep-me"
			}
			var out bytes.Buffer
			if err = toml.NewEncoder(&out).Encode(root); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(path, out.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
			e, err := PlanManagedRemoval("codex", path, r.ManagedEntries)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, check := range e.Checks {
				if check.Key == "otel.exporter" {
					found = true
					if check.Status != "same" {
						t.Fatalf("그룹 판정 = %s, 기대값 = same", check.Status)
					}
				}
			}
			if !found {
				t.Fatal("exporter 그룹 판정 누락")
			}
			if err = e.Apply(); err != nil {
				t.Fatal(err)
			}
			var after map[string]any
			if _, err = toml.DecodeFile(path, &after); err != nil {
				t.Fatal(err)
			}
			for _, entry := range r.ManagedEntries {
				if entry.Event == "" {
					if _, exists := managedValue(after, entry.Path); exists {
						t.Fatalf("관리 설정 잔존: %s", entryKey(entry))
					}
				}
			}
			if scenario == "deleted_endpoint" {
				if _, exists := after["otel"]; exists {
					t.Fatal("빈 OTel 테이블이 남았다")
				}
			} else {
				value, exists := managedValue(after, []string{"otel", "exporter", "otlp-http", "user_option"})
				if !exists || value != "keep-me" {
					t.Fatal("사용자가 추가한 필드 유실")
				}
			}
		})
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
