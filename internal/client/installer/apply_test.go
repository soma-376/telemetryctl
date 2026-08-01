package installer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/your-org/pulsemetry/internal/contract"
)

func testEnrollment() *contract.Enrollment {
	return &contract.Enrollment{
		InstallationID:    "ins_test",
		InstallationToken: "inst_secret",
		Manifest: contract.Manifest{
			SchemaVersion:  1,
			ConfigRevision: 12,
			OTLP:           contract.OTLP{Endpoint: "https://telemetry.company.com", Protocol: "http/protobuf"},
			Signals:        contract.Signals{Logs: true, Metrics: true, Traces: false},
			Privacy:        contract.Privacy{}, // 전부 false
		},
	}
}

func testOptions(dir string) Options {
	return Options{
		ClaudePath: filepath.Join(dir, ".claude", "settings.json"),
		CodexPath:  filepath.Join(dir, ".codex", "config.toml"),
		StatePath:  filepath.Join(dir, ".pulsemetry", "state.json"),
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestApply(t *testing.T) {
	dir := t.TempDir()
	opts := testOptions(dir)

	rep, err := Apply(testEnrollment(), opts)
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

	// 상태 저장 확인 + 토큰이 상태 파일에 새지 않았는지 (§4.5)
	st, err := LoadState(opts.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if st == nil || len(st.Targets) != 2 {
		t.Fatalf("상태 타깃 2개 기대")
	}
	rawState, _ := os.ReadFile(opts.StatePath)
	if strings.Contains(string(rawState), "inst_secret") {
		t.Error("상태 파일에 installation_token 이 노출됨 (§4.5 위반)")
	}
}

func TestApplyEndpointConflictAborts(t *testing.T) {
	dir := t.TempDir()
	opts := testOptions(dir)
	// Claude 에 이미 다른 endpoint (첫 대상에서 충돌)
	writeFile(t, opts.ClaudePath, `{"env":{"OTEL_EXPORTER_OTLP_ENDPOINT":"https://other.datadog.com"}}`)

	if _, err := Apply(testEnrollment(), opts); err == nil {
		t.Fatal("endpoint 충돌 시 실패해야 함 (§4.2)")
	}
	// 첫 대상에서 실패했으므로 적용된 게 없어 상태 파일이 없어야 한다.
	if st, _ := LoadState(opts.StatePath); st != nil {
		t.Error("아무것도 적용 안 됐는데 상태가 저장됨")
	}
}

func TestApplyForceReplacesConflict(t *testing.T) {
	dir := t.TempDir()
	opts := testOptions(dir)
	opts.Force = true
	writeFile(t, opts.ClaudePath, `{"env":{"OTEL_EXPORTER_OTLP_ENDPOINT":"https://other.datadog.com"}}`)

	if _, err := Apply(testEnrollment(), opts); err != nil {
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
