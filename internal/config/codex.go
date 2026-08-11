package config

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/BurntSushi/toml"
	"github.com/your-org/pulsemetry/internal/contract"
)

var codexManagedOTelKeys = []string{
	"environment",
	"log_user_prompt",
	"exporter",
	"endpoint",
	"protocol",
	"headers",
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

	return map[string]any{
		"environment":     environment,
		"log_user_prompt": m.Privacy.CollectUserPrompts,
		"exporter": map[string]any{
			exporterID: exporter,
		},
	}, nil
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
