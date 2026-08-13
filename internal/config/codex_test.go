package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"

	// 테스트에서만 import 한다. 프로덕션 config 가 otlpdecode 를 끌어들이면 enroll·status
	// 경로까지 protobuf 디코더가 딸려 온다 (codex.go 의 codexSignalPaths 주석).
	"github.com/your-org/pulsemetry/internal/otlpdecode"
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

// TestCodex경로가PayloadKind와일치한다 는 복제된 경로 문자열이 진짜와 어긋나지 않게 한다.
//
// config 는 otlpdecode 를 import 하지 않는다 — 그러면 enroll·status 경로까지 protobuf
// 디코더가 딸려 온다 (codex.go 의 codexSignalPaths 주석). 그래서 값을 복제하고, 대신
// 여기서만 otlpdecode 를 불러 두 값이 같은지 본다. LocalIngestHeader 와 같은 방식이다.
//
// 어긋나면 Codex 가 존재하지 않는 경로로 보내고 수신기가 404 로 전부 버린다.
func TestCodex경로가PayloadKind와일치한다(t *testing.T) {
	want := map[string]string{
		codexLogsExporterKey:    otlpdecode.PayloadLogs.Path(),
		codexMetricsExporterKey: otlpdecode.PayloadMetrics.Path(),
		codexTracesExporterKey:  otlpdecode.PayloadTraces.Path(),
	}
	if len(codexSignalPaths) != len(want) {
		t.Fatalf("codexSignalPaths 항목 수 = %d, want %d", len(codexSignalPaths), len(want))
	}
	for key, wantPath := range want {
		if got := codexSignalPaths[key]; got != wantPath {
			t.Errorf("codexSignalPaths[%s] = %q, want %q (otlpdecode.PayloadKind.Path())", key, got, wantPath)
		}
	}
}

// TestCodex회사직결출력은변하지않는다 는 PROJ-45 가 회사 직결 경로를 건드리지 않았음을
// 못박는다.
//
// 로컬에만 exporter 3종을 추가하는 것이 이 티켓의 설계다. 회사 직결에서도 추가하면
// 회사가 받는 데이터가 늘어난다 — installer/local.go 의 불변식 1 위반이다.
func TestCodex회사직결출력은변하지않는다(t *testing.T) {
	m := companyManifest() // endpoint 는 https://collector.example.com
	table, err := codexOTelTable(m, "company-token")
	if err != nil {
		t.Fatalf("codexOTelTable: %v", err)
	}

	if _, ok := table[codexMetricsExporterKey]; ok {
		t.Error("회사 직결 설정에 metrics_exporter 가 생겼다 — 회사가 받는 데이터가 늘어난다")
	}
	if _, ok := table[codexTracesExporterKey]; ok {
		t.Error("회사 직결 설정에 trace_exporter 가 생겼다 — 회사가 받는 데이터가 늘어난다")
	}

	exporter, _ := table[codexLogsExporterKey].(map[string]any)
	inner, _ := exporter["otlp-http"].(map[string]any)
	if inner == nil {
		t.Fatalf("회사 직결 exporter 표가 없다: %v", table)
	}
	// 경로를 붙이지 않은 base endpoint 그대로여야 한다.
	if got := inner["endpoint"]; got != m.OTLP.Endpoint {
		t.Errorf("endpoint = %v, want %v (경로를 붙이면 안 된다)", got, m.OTLP.Endpoint)
	}
	headers, _ := inner["headers"].(map[string]any)
	if _, ok := headers[LocalIngestHeader]; ok {
		t.Error("회사 직결 설정에 로컬 헤더가 붙었다")
	}
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
