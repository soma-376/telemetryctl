package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
)

func readOtel(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var root map[string]any
	if err := toml.Unmarshal(b, &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	otel, _ := root["otel"].(map[string]any)
	return otel
}

func TestMergeCodex(t *testing.T) {
	t.Run("새 파일 생성 + [otel] 적용", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.toml")
		res, err := MergeCodex(path, testManifest(), testToken, false)
		if err != nil {
			t.Fatal(err)
		}
		if !res.Created {
			t.Error("Created 여야 함")
		}
		otel := readOtel(t, path)
		if otel["endpoint"] != "https://telemetry.company.com" {
			t.Errorf("endpoint 미설정: %v", otel["endpoint"])
		}
		if otel["log_user_prompt"] != false {
			t.Errorf("log_user_prompt 는 기본 false 여야 함: %v", otel["log_user_prompt"])
		}
	})

	t.Run("기존 최상위 키 보존", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(path, []byte("model = \"my-model\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		res, err := MergeCodex(path, testManifest(), testToken, false)
		if err != nil {
			t.Fatal(err)
		}
		if res.BackupPath == "" {
			t.Error("기존 파일이 있으면 백업이 있어야 함")
		}
		var root map[string]any
		b, _ := os.ReadFile(path)
		if err := toml.Unmarshal(b, &root); err != nil {
			t.Fatal(err)
		}
		if root["model"] != "my-model" {
			t.Error("최상위 model 키가 사라짐")
		}
	})

	t.Run("다른 endpoint 충돌 시 거부", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.toml")
		orig := "[otel]\nendpoint = \"https://other.datadog.com\"\n"
		if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := MergeCodex(path, testManifest(), testToken, false)
		if !errors.Is(err, ErrEndpointConflict) {
			t.Fatalf("충돌 에러 기대, got %v", err)
		}
	})

	t.Run("force 로 충돌 교체", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.toml")
		orig := "[otel]\nendpoint = \"https://other.datadog.com\"\n"
		if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := MergeCodex(path, testManifest(), testToken, true); err != nil {
			t.Fatalf("force 교체 실패: %v", err)
		}
		otel := readOtel(t, path)
		if otel["endpoint"] != "https://telemetry.company.com" {
			t.Error("force 인데 endpoint 교체 안됨")
		}
	})
}
