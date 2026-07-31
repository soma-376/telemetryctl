package configmerge

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/your-org/telemetryctl/internal/manifest"
)

// testToken 은 enroll 봉투의 installation_token 을 대신하는 테스트 값이다.
// 봉투 분리 후 토큰은 manifest 가 아니라 Merge 함수 인자로 들어간다.
const testToken = "inst_secret"

func testManifest() *manifest.Manifest {
	return &manifest.Manifest{
		SchemaVersion:  1,
		ConfigRevision: 12,
		OTLP: manifest.OTLP{
			Endpoint: "https://telemetry.company.com",
			Protocol: "http/protobuf",
		},
		Signals: manifest.Signals{Logs: true, Metrics: true, Traces: false},
		Privacy: manifest.Privacy{}, // 전부 false
	}
}

func readEnv(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	env, _ := root["env"].(map[string]any)
	return env
}

func TestMergeClaude(t *testing.T) {
	t.Run("새 파일 생성", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "settings.json")
		res, err := MergeClaude(path, testManifest(), testToken, false)
		if err != nil {
			t.Fatal(err)
		}
		if !res.Created {
			t.Error("Created 여야 함")
		}
		env := readEnv(t, path)
		if env["OTEL_EXPORTER_OTLP_ENDPOINT"] != "https://telemetry.company.com" {
			t.Errorf("endpoint 미설정: %v", env["OTEL_EXPORTER_OTLP_ENDPOINT"])
		}
		if env["OTEL_LOG_USER_PROMPTS"] != "0" {
			t.Errorf("프롬프트 수집은 기본 OFF(0) 여야 함: %v", env["OTEL_LOG_USER_PROMPTS"])
		}
	})

	t.Run("기존 사용자 키 보존", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "settings.json")
		orig := `{
  "model": "my-model",
  "env": { "EXISTING_USER_VARIABLE": "keep" }
}`
		if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
			t.Fatal(err)
		}
		res, err := MergeClaude(path, testManifest(), testToken, false)
		if err != nil {
			t.Fatal(err)
		}
		if res.BackupPath == "" {
			t.Error("기존 파일이 있으면 백업이 있어야 함")
		}
		env := readEnv(t, path)
		if env["EXISTING_USER_VARIABLE"] != "keep" {
			t.Error("사용자 env 키가 사라짐")
		}
		if env["CLAUDE_CODE_ENABLE_TELEMETRY"] != "1" {
			t.Error("텔레메트리 키가 병합되지 않음")
		}
		// 최상위 model 키도 보존되어야 함
		b, _ := os.ReadFile(path)
		var root map[string]any
		_ = json.Unmarshal(b, &root)
		if root["model"] != "my-model" {
			t.Error("최상위 model 키가 사라짐")
		}
	})

	t.Run("다른 endpoint 충돌 시 거부", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "settings.json")
		orig := `{"env":{"OTEL_EXPORTER_OTLP_ENDPOINT":"https://other.datadog.com"}}`
		if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := MergeClaude(path, testManifest(), testToken, false)
		if !errors.Is(err, ErrEndpointConflict) {
			t.Fatalf("충돌 에러 기대, got %v", err)
		}
	})

	t.Run("force 로 충돌 교체", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "settings.json")
		orig := `{"env":{"OTEL_EXPORTER_OTLP_ENDPOINT":"https://other.datadog.com"}}`
		if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := MergeClaude(path, testManifest(), testToken, true); err != nil {
			t.Fatalf("force 교체 실패: %v", err)
		}
		env := readEnv(t, path)
		if env["OTEL_EXPORTER_OTLP_ENDPOINT"] != "https://telemetry.company.com" {
			t.Error("force 인데 endpoint 교체 안됨")
		}
	})
}
