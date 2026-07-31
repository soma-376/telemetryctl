package installer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleEnrollment = `{
  "installation_id": "ins_test",
  "installation_token": "inst_secret",
  "manifest": {
    "schema_version": 1,
    "config_revision": 12,
    "otlp": { "endpoint": "https://telemetry.company.com", "protocol": "http/protobuf" },
    "signals": { "logs": true, "metrics": true, "traces": false },
    "privacy": {
      "collect_user_prompts": false,
      "collect_assistant_responses": false,
      "collect_tool_details": false,
      "collect_tool_content": false,
      "collect_user_email": false
    }
  }
}`

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func testOptions(dir string) Options {
	return Options{
		ManifestPath: filepath.Join(dir, "manifest.json"),
		ClaudePath:   filepath.Join(dir, ".claude", "settings.json"),
		CodexPath:    filepath.Join(dir, ".codex", "config.toml"),
		StatePath:    filepath.Join(dir, ".telemetryctl", "state.json"),
	}
}

func TestInstall(t *testing.T) {
	dir := t.TempDir()
	opts := testOptions(dir)
	writeFile(t, opts.ManifestPath, sampleEnrollment)

	rep, err := Install(opts)
	if err != nil {
		t.Fatal(err)
	}
	if rep.InstallationID != "ins_test" {
		t.Errorf("installation_id=%q", rep.InstallationID)
	}
	if len(rep.Targets) != 2 {
		t.Fatalf("타깃 2개(claude, codex) 기대, got %d", len(rep.Targets))
	}

	// Claude settings.json 에 endpoint 가 실제로 적용됐는지
	b, err := os.ReadFile(opts.ClaudePath)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatal(err)
	}
	env, _ := root["env"].(map[string]any)
	if env["OTEL_EXPORTER_OTLP_ENDPOINT"] != "https://telemetry.company.com" {
		t.Errorf("claude endpoint 미적용: %v", env["OTEL_EXPORTER_OTLP_ENDPOINT"])
	}

	// 상태 저장 확인
	st, err := LoadState(opts.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if st == nil {
		t.Fatal("상태 파일이 저장되지 않음")
	}
	if len(st.Targets) != 2 {
		t.Errorf("상태 타깃 2개 기대, got %d", len(st.Targets))
	}

	// 토큰이 상태 파일에 새지 않아야 한다 (§4.5)
	rawState, _ := os.ReadFile(opts.StatePath)
	if strings.Contains(string(rawState), "inst_secret") {
		t.Error("상태 파일에 installation_token 이 노출됨 (§4.5 위반)")
	}
}

func TestInstallMissingManifest(t *testing.T) {
	dir := t.TempDir()
	opts := testOptions(dir)
	// manifest 파일을 만들지 않음
	if _, err := Install(opts); err == nil {
		t.Fatal("없는 manifest 파일이면 에러여야 함")
	}
}

func TestInstallEndpointConflictAborts(t *testing.T) {
	dir := t.TempDir()
	opts := testOptions(dir)
	writeFile(t, opts.ManifestPath, sampleEnrollment)
	// Claude 에 이미 다른 endpoint 가 있는 상태 (첫 대상에서 충돌)
	writeFile(t, opts.ClaudePath, `{"env":{"OTEL_EXPORTER_OTLP_ENDPOINT":"https://other.datadog.com"}}`)

	if _, err := Install(opts); err == nil {
		t.Fatal("endpoint 충돌 시 설치가 실패해야 함 (§4.2)")
	}
	// 첫 대상에서 실패했으므로 적용된 게 없어 상태 파일이 없어야 한다.
	if st, _ := LoadState(opts.StatePath); st != nil {
		t.Error("아무것도 적용 안 됐는데 상태가 저장됨")
	}
}

func TestInstallForceReplacesConflict(t *testing.T) {
	dir := t.TempDir()
	opts := testOptions(dir)
	opts.Force = true
	writeFile(t, opts.ManifestPath, sampleEnrollment)
	writeFile(t, opts.ClaudePath, `{"env":{"OTEL_EXPORTER_OTLP_ENDPOINT":"https://other.datadog.com"}}`)

	if _, err := Install(opts); err != nil {
		t.Fatalf("--force 면 충돌을 교체해야 함: %v", err)
	}
	b, _ := os.ReadFile(opts.ClaudePath)
	var root map[string]any
	_ = json.Unmarshal(b, &root)
	env, _ := root["env"].(map[string]any)
	if env["OTEL_EXPORTER_OTLP_ENDPOINT"] != "https://telemetry.company.com" {
		t.Error("force 인데 endpoint 교체 안됨")
	}
}
