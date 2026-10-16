package contract

import (
	"encoding/json"
	"fmt"
	"strings"
)

// EnrollRequest 는 POST /v1/enroll 요청 본문이다. 서버와 클라이언트가 공유하는 유일한 요청 계약이다
// (각자 따로 선언하지 않는다 — 필드 드리프트 방지).
type EnrollRequest struct {
	Code          string `json:"code,omitempty"`
	Platform      string `json:"platform,omitempty"`
	Architecture  string `json:"architecture,omitempty"`
	Hostname      string `json:"hostname,omitempty"`
	ClientVersion string `json:"client_version,omitempty"`
	// Deprecated compatibility fields.
	Invite               string   `json:"invite"`
	InstallerVersion     string   `json:"installer_version,omitempty"`
	OperatingEnvironment string   `json:"operating_environment,omitempty"`
	DeviceID             string   `json:"device_id,omitempty"`
	ToolsDetected        []string `json:"tools_detected,omitempty"`
}

// Enrollment 은 enroll 응답 봉투다. 순수 설정 manifest 에 이 설치의 정체성·토큰을 감싼다.
// installation_id·installation_token 은 "설정"이 아니라 이 설치의 자격이므로 manifest 밖에 둔다:
// 설정 재조회(GET /v1/manifest)에 secret 을 매번 싣지 않기 위함이다 (enrollment-server-spec §4.3·§5).
type Enrollment struct {
	InstallationID    string   `json:"installation_id"`
	InstallationToken string   `json:"installation_token"`
	TelemetryToken    string   `json:"telemetry_token"`
	Manifest          Manifest `json:"manifest"`
}

// TelemetryTokenResponse is returned when an installed client exchanges its
// OS-keyring installation credential for a replaceable telemetry credential.
type TelemetryTokenResponse struct {
	InstallationID string `json:"installation_id"`
	TelemetryToken string `json:"telemetry_token"`
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
	if e.TelemetryToken == "" {
		return fmt.Errorf("enrollment telemetry_token 누락")
	}
	return e.Manifest.Validate()
}

func ParseTelemetryTokenResponse(raw []byte) (*TelemetryTokenResponse, error) {
	var response TelemetryTokenResponse
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&response); err != nil {
		return nil, fmt.Errorf("telemetry token decode: %w", err)
	}
	if response.InstallationID == "" {
		return nil, fmt.Errorf("telemetry token installation_id 누락")
	}
	if response.TelemetryToken == "" {
		return nil, fmt.Errorf("telemetry_token 누락")
	}
	return &response, nil
}
