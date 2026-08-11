package config

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/your-org/pulsemetry/internal/contract"
)

var codexManagedOTelKeys = []string{
	"environment",
	"log_user_prompt",
	"exporter",
	// metrics_exporter·trace_exporter 는 로컬 배선에서만 쓰지만 목록에는 언제나 있어야
	// 한다. 빠지면 local disable 이 두 테이블을 지우지 못해 회사 직결 상태에서도 Codex 가
	// localhost 로 메트릭·트레이스를 계속 보낸다 (PROJ-45).
	"metrics_exporter",
	"trace_exporter",
	"endpoint",
	"protocol",
	"headers",
}

// codex 로컬 배선이 쓰는 고정값 (PROJ-45 참고 자료).
const (
	// codexLocalProtocol 은 OTLP/HTTP protobuf 를 가리키는 Codex 표기다.
	codexLocalProtocol = "binary"
	// codexLogsExporterKey 는 로그 exporter 테이블 이름이다. Codex 는 로그만 exporter 로
	// 부르고 나머지는 시그널 이름을 앞에 붙인다.
	codexLogsExporterKey    = "exporter"
	codexMetricsExporterKey = "metrics_exporter"
	codexTracesExporterKey  = "trace_exporter"
)

// codexSignalPaths 는 로컬 배선에서 시그널별 exporter 가 가리킬 경로다.
//
// 값은 otlpdecode.PayloadKind.Path() 와 같아야 한다. 그 상수를 import 하지 않는 이유는
// config 가 otlpdecode 를 끌어들이면 enroll·status 경로까지 protobuf 디코더가 딸려오기
// 때문이다 — LocalIngestHeader·DefaultLocalPort 와 같은 의존성 방향 규칙이다.
// 두 값이 어긋나면 codex_test.go 가 잡는다 (테스트에서만 otlpdecode 를 import 한다).
var codexSignalPaths = map[string]string{
	codexLogsExporterKey:    "/v1/logs",
	codexMetricsExporterKey: "/v1/metrics",
	codexTracesExporterKey:  "/v1/traces",
}

func codexOTelTable(m *contract.Manifest, token string) (map[string]any, error) {
	environment := "production"
	if value := m.ResourceAttributes["deployment.environment"]; value != "" {
		environment = value
	}

	// 로컬 수신기는 bearer 토큰만으로 통과시키지 않는다 — localheader.go 참고.
	// TOML 은 헤더가 원래 표라서 claude 쪽처럼 문자열을 조립할 필요가 없다.
	headers := map[string]any{
		"Authorization": "Bearer " + token,
	}
	if isLocalEndpoint(m.OTLP.Endpoint) {
		headers[LocalIngestHeader] = LocalIngestHeaderValue
	}

	table := map[string]any{
		"environment":     environment,
		"log_user_prompt": m.Privacy.CollectUserPrompts,
	}

	// 로컬 배선은 시그널마다 exporter 를 따로 둔다.
	//
	// Claude 와 달리 Codex 는 base endpoint 를 주면 경로를 스스로 붙이지 않는다. 하나의
	// exporter 만 두면 메트릭·트레이스가 /v1/logs 로 가고 수신기가 전부 거부한다.
	// 회사 직결은 예전 그대로 exporter 하나만 쓴다 — 그 경로에서 무엇이 나가는지는
	// PROJ-45 의 범위가 아니고, 바꾸면 회사가 받는 데이터가 달라진다 (불변식 1).
	if isLocalEndpoint(m.OTLP.Endpoint) {
		base := strings.TrimRight(m.OTLP.Endpoint, "/")
		for key, path := range codexSignalPaths {
			// 헤더 맵은 exporter 마다 복사한다. 같은 맵을 세 테이블이 공유하면 나중에
			// 한 exporter 의 헤더만 고치려는 코드가 세 개를 한꺼번에 바꾼다 —
			// installer.cloneManifest 가 막는 것과 같은 종류의 사고다.
			table[key] = map[string]any{
				"otlp-http": map[string]any{
					"endpoint": base + path,
					"protocol": codexLocalProtocol,
					"headers":  cloneHeaders(headers),
				},
			}
		}
		return table, nil
	}

	exporterID := ""
	exporter := map[string]any{
		"endpoint": m.OTLP.Endpoint,
		"headers":  headers,
	}
	switch m.OTLP.Protocol {
	case "http/protobuf":
		exporterID = "otlp-http"
		exporter["protocol"] = "binary"
	case "http/json":
		exporterID = "otlp-http"
		exporter["protocol"] = "json"
	case "grpc":
		exporterID = "otlp-grpc"
	default:
		return nil, fmt.Errorf("unsupported Codex OTLP protocol %q", m.OTLP.Protocol)
	}

	table[codexLogsExporterKey] = map[string]any{exporterID: exporter}
	return table, nil
}

func cloneHeaders(h map[string]any) map[string]any {
	out := make(map[string]any, len(h))
	for k, v := range h {
		out[k] = v
	}
	return out
}

// MergeCodex authoritatively synchronizes only Pulsemetry-managed [otel] keys.
func MergeCodex(path string, m *contract.Manifest, token string, _ bool) (Result, error) {
	raw, existed, err := readFileIfExists(path)
	if err != nil {
		return Result{}, err
	}
	root := map[string]any{}
	if existed && len(raw) > 0 {
		if err := toml.Unmarshal(raw, &root); err != nil {
			return Result{}, fmt.Errorf("parse Codex config %s: %w", path, err)
		}
	}
	otel, _ := root["otel"].(map[string]any)
	if otel == nil {
		otel = map[string]any{}
	}
	for _, key := range codexManagedOTelKeys {
		delete(otel, key)
	}
	desired, err := codexOTelTable(m, token)
	if err != nil {
		return Result{}, err
	}
	managed := make([]string, 0, len(desired))
	for key, value := range desired {
		otel[key] = value
		managed = append(managed, "otel."+key)
	}
	sort.Strings(managed)
	root["otel"] = otel
	var out bytes.Buffer
	if err := toml.NewEncoder(&out).Encode(root); err != nil {
		return Result{}, err
	}
	if err := AtomicWriteFile(path, out.Bytes(), 0o600); err != nil {
		return Result{}, err
	}
	return Result{Path: path, ManagedKeys: managed, Created: !existed}, nil
}
