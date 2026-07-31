// Package manifest 는 enrollment 서버가 발급하는 설정 manifest 를 표현한다.
// 이 struct 는 contracts/enrollment-manifest.schema.json 과 1:1 로 대응해야 한다 —
// 스키마가 바뀌면 여기도 같이 바꾸고, manifest_test.go 의 계약 테스트로 드리프트를 잡는다.
package manifest

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SupportedSchemaVersion 은 이 installer 빌드가 이해하는 manifest 스키마 버전이다.
// 서버가 이보다 높은 버전을 주면 installer 를 업데이트해야 한다 (§5.10).
const SupportedSchemaVersion = 1

type Manifest struct {
	SchemaVersion      int               `json:"schema_version"`
	ConfigRevision     int               `json:"config_revision"`
	OTLP               OTLP              `json:"otlp"`
	Signals            Signals           `json:"signals"`
	Privacy            Privacy           `json:"privacy"`
	RepositoryAllowlist []string         `json:"repository_allowlist,omitempty"`
	ResourceAttributes map[string]string `json:"resource_attributes,omitempty"`
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
	CollectUserPrompts       bool `json:"collect_user_prompts"`
	CollectAssistantResponses bool `json:"collect_assistant_responses"`
	CollectToolDetails       bool `json:"collect_tool_details"`
	CollectToolContent       bool `json:"collect_tool_content"`
	CollectUserEmail         bool `json:"collect_user_email"`
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
	if !strings.HasPrefix(m.OTLP.Endpoint, "https://") {
		return fmt.Errorf("otlp.endpoint 는 https 여야 함 (got %q)", redactEndpoint(m.OTLP.Endpoint))
	}
	switch m.OTLP.Protocol {
	case "http/protobuf", "http/json", "grpc":
	default:
		return fmt.Errorf("지원하지 않는 otlp.protocol: %q", m.OTLP.Protocol)
	}
	return nil
}

func redactEndpoint(s string) string {
	if len(s) > 40 {
		return s[:40] + "…"
	}
	return s
}

// Enrollment 은 enroll 응답 봉투다. 순수 설정 manifest 에 이 설치의 정체성·토큰을 감싼다.
// installation_id·installation_token 은 "설정"이 아니라 이 설치의 자격이므로 manifest 밖에 둔다:
// 설정 재조회(GET /v1/manifest)에 secret 을 매번 싣지 않기 위함이다 (enrollment-server-spec §4.3·§5).
type Enrollment struct {
	InstallationID    string   `json:"installation_id"`
	InstallationToken string   `json:"installation_token"`
	Manifest          Manifest `json:"manifest"`
}

// ParseEnrollment 는 enroll 응답(JSON 봉투)을 디코드하고 검증한다.
// DisallowUnknownFields 가 중첩 manifest 까지 적용되므로, manifest 안에 installation_token 같은
// 필드가 잘못 들어오면 계약 위반으로 거부된다(봉투 분리 강제).
func ParseEnrollment(raw []byte) (*Enrollment, error) {
	var e Enrollment
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil {
		return nil, fmt.Errorf("enrollment decode: %w", err)
	}
	if err := e.Validate(); err != nil {
		return nil, err
	}
	return &e, nil
}

// Validate 는 봉투의 정체성 필드와 내부 manifest 를 검증한다.
func (e *Enrollment) Validate() error {
	if e.InstallationID == "" {
		return fmt.Errorf("enrollment installation_id 누락")
	}
	if e.InstallationToken == "" {
		return fmt.Errorf("enrollment installation_token 누락")
	}
	return e.Manifest.Validate()
}
