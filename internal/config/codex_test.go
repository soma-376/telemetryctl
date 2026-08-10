package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
)

func readTOML(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var root map[string]any
	if err := toml.Unmarshal(b, &root); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	return root
}

// codexHeaders 는 병합된 config.toml 에서 exporter 헤더 표를 꺼낸다.
// exporter id 는 프로토콜에 따라 다르므로 ReadCodexToken 과 같은 방식으로 순회한다.
func codexHeaders(t *testing.T, path string) map[string]any {
	t.Helper()
	root := readTOML(t, path)
	otel, _ := root["otel"].(map[string]any)
	exporters, _ := otel["exporter"].(map[string]any)
	for _, entry := range exporters {
		exporter, _ := entry.(map[string]any)
		if headers, ok := exporter["headers"].(map[string]any); ok {
			return headers
		}
	}
	t.Fatalf("headers 표를 찾지 못했다: %v", root)
	return nil
}

// TestMergeCodexLocalEndpointAddsLocalHeader 는 재배선된 Codex 설정이 수신기의 3중 인증을
// 통과할 수 있는 형태인지 본다. Authorization 만 있으면 전량 401 이다.
func TestMergeCodexLocalEndpointAddsLocalHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	m := companyManifest()
	m.OTLP.Endpoint = "http://localhost:4318"

	if _, err := MergeCodex(path, m, "local-ingest-token", false); err != nil {
		t.Fatalf("MergeCodex: %v", err)
	}

	headers := codexHeaders(t, path)
	if got := headers["Authorization"]; got != "Bearer local-ingest-token" {
		t.Errorf("Authorization = %v, want Bearer local-ingest-token", got)
	}
	if got := headers[LocalIngestHeader]; got != LocalIngestHeaderValue {
		t.Errorf("%s = %v, want %q", LocalIngestHeader, got, LocalIngestHeaderValue)
	}

	// 두 번째 헤더가 생겨도 회사 토큰 복구 경로가 살아 있어야 `local disable` 이 된다.
	token, err := ReadCodexToken(path)
	if err != nil {
		t.Fatalf("ReadCodexToken: %v", err)
	}
	if token != "local-ingest-token" {
		t.Errorf("token = %q, want local-ingest-token", token)
	}
}

// TestMergeCodexCompanyEndpointOmitsLocalHeader 는 회사 Collector 설정에 로컬 헤더가
// 붙지 않음을 고정한다.
func TestMergeCodexCompanyEndpointOmitsLocalHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if _, err := MergeCodex(path, companyManifest(), "company-telemetry-token", false); err != nil {
		t.Fatalf("MergeCodex: %v", err)
	}

	headers := codexHeaders(t, path)
	if got := headers["Authorization"]; got != "Bearer company-telemetry-token" {
		t.Errorf("Authorization = %v, want Bearer company-telemetry-token", got)
	}
	if _, ok := headers[LocalIngestHeader]; ok {
		t.Errorf("회사 endpoint 설정에 %s 가 들어갔다: %v", LocalIngestHeader, headers)
	}
}

// TestMergeCodexLocalHeaderIsManaged 는 재배선을 되돌리면 로컬 헤더도 함께 사라지는지 본다.
//
// headers 표 전체가 otel.headers 라는 하나의 관리 키라, `local disable` 이 표를 통째로
// 지우고 다시 쓴다 — 로컬 헤더만 남는 잔재가 생기면 안 된다 (§5.2).
func TestMergeCodexLocalHeaderIsManaged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")

	local := companyManifest()
	local.OTLP.Endpoint = "http://localhost:4318"
	if _, err := MergeCodex(path, local, "local-ingest-token", false); err != nil {
		t.Fatalf("MergeCodex(로컬): %v", err)
	}
	if _, ok := codexHeaders(t, path)[LocalIngestHeader]; !ok {
		t.Fatalf("로컬 병합인데 %s 가 없다", LocalIngestHeader)
	}

	// 같은 파일 위에 회사 manifest 로 다시 병합 = `local disable` 이 하는 일.
	if _, err := MergeCodex(path, companyManifest(), "company-telemetry-token", false); err != nil {
		t.Fatalf("MergeCodex(회사): %v", err)
	}
	if _, ok := codexHeaders(t, path)[LocalIngestHeader]; ok {
		t.Errorf("disable 후에도 %s 가 남았다 — 회사 Collector 로 잔재가 나간다", LocalIngestHeader)
	}
}
