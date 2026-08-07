// Package enrollment 는 pulsemetry CLI 가 enrollment 서버의 /v1/enroll 을 호출하는
// 클라이언트다. 요청/응답 모두 공유 계약(contract)을 사용한다.
package enrollment

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/your-org/pulsemetry/internal/contract"
)

// Enroll 은 초대 코드로 서버에 등록하고 검증된 봉투를 반환한다.
func Enroll(serverURL string, req contract.EnrollRequest) (*contract.Enrollment, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	url := strings.TrimRight(serverURL, "/") + "/v1/enroll"
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))

	if err != nil {
		return nil, fmt.Errorf("enroll 요청 실패 (%s): %w", url, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("enroll 거부 (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}

	// 서버가 준 봉투를 클라이언트와 동일한 계약으로 검증(https·enum 등)까지 한다.
	return contract.ParseEnrollment(data)
}

// RefreshTelemetryToken exchanges the installation credential kept in the OS
// keyring for the replaceable bearer token used by OTLP exporters.
func RefreshTelemetryToken(serverURL, installationToken string) (*contract.TelemetryTokenResponse, error) {
	url := strings.TrimRight(serverURL, "/") + "/v1/installations/telemetry-token"
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+installationToken)
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("telemetry token 재발급 요청 실패 (%s): %w", url, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("telemetry token 재발급 거부 (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return contract.ParseTelemetryTokenResponse(data)
}
