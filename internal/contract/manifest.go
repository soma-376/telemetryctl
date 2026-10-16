// Package contract 는 서버와 클라이언트가 네트워크 경계를 넘어 공유하는 계약 타입이다:
// enroll 요청/응답과 설정 manifest. manifest 는 contracts/enrollment-manifest.schema.json 과
// 1:1 로 대응한다 — 스키마가 바뀌면 여기도 같이 바꾼다. 서버·클라이언트 구현 세부는 여기 두지 않는다.
package contract

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// SupportedSchemaVersion 은 이 installer 빌드가 이해하는 manifest 스키마 버전이다.
// 서버가 이보다 높은 버전을 주면 installer 를 업데이트해야 한다 (§5.10).
const SupportedSchemaVersion = 1

type Manifest struct {
	SchemaVersion       int               `json:"schema_version"`
	ConfigRevision      int               `json:"config_revision"`
	OTLP                OTLP              `json:"otlp"`
	Signals             Signals           `json:"signals"`
	Privacy             Privacy           `json:"privacy"`
	RepositoryAllowlist []string          `json:"repository_allowlist,omitempty"`
	ResourceAttributes  map[string]string `json:"resource_attributes,omitempty"`
}

type OTLP struct {
	Endpoint    string `json:"endpoint"`
	Protocol    string `json:"protocol"`
	Compression string `json:"compression,omitempty"`
	TimeoutMS   int    `json:"timeout_ms,omitempty"`
}

type Signals struct {
	Logs    bool `json:"logs"`
	Metrics bool `json:"metrics"`
	Traces  bool `json:"traces"`
}

// Privacy 기본값은 전부 false 여야 한다 (§4.6). installer 는 서버가 준 값을 그대로 적용하되,
// 클라이언트 설정만 믿지 않고 Collector redaction 과 이중으로 방어한다.
type Privacy struct {
	CollectUserPrompts        bool `json:"collect_user_prompts"`
	CollectAssistantResponses bool `json:"collect_assistant_responses"`
	CollectToolDetails        bool `json:"collect_tool_details"`
	CollectToolContent        bool `json:"collect_tool_content"`
	CollectUserEmail          bool `json:"collect_user_email"`
	CollectRawAPIBodies       bool `json:"collect_raw_api_bodies"`
}

// Parse 는 enrollment 응답(JSON)을 Manifest 로 디코드하고 최소 검증을 수행한다.
func Parse(raw []byte) (*Manifest, error) {
	var m Manifest
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields() // 스키마에 없는 필드는 계약 위반으로 간주
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("manifest decode: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

func (m *Manifest) Validate() error {
	if m.SchemaVersion > SupportedSchemaVersion {
		return fmt.Errorf("manifest schema_version %d > supported %d: installer 업데이트 필요", m.SchemaVersion, SupportedSchemaVersion)
	}
	if m.SchemaVersion < 1 {
		return fmt.Errorf("manifest schema_version 은 1 이상이어야 함 (got %d)", m.SchemaVersion)
	}
	if !validOTLPEndpoint(m.OTLP.Endpoint) {
		return fmt.Errorf("otlp.endpoint must use https (http is allowed only for localhost; got %q)", redactEndpoint(m.OTLP.Endpoint))
	}
	switch m.OTLP.Protocol {
	case "http/protobuf", "http/json", "grpc":
	default:
		return fmt.Errorf("지원하지 않는 otlp.protocol: %q", m.OTLP.Protocol)
	}
	return nil
}

func validOTLPEndpoint(endpoint string) bool {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		return false
	}
	return parsed.Scheme == "https" || (parsed.Scheme == "http" && parsed.Hostname() == "localhost")
}

func redactEndpoint(s string) string {
	if len(s) > 40 {
		return s[:40] + "…"
	}
	return s
}
