// Package heartbeat는 설치의 수집·전달 상태를 등록 서버에 보고한다.
package heartbeat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/your-org/pulsemetry/internal/credential"
)

const RequestTimeout = 10 * time.Second

// Report는 백엔드 설치 보고 API의 본문이다. 선택 시각도 null 키를 생략하지 않는다.
type Report struct {
	SentAt                time.Time  `json:"sent_at"`
	Daemon                Daemon     `json:"daemon"`
	AppliedConfigRevision int        `json:"applied_config_revision"`
	Collection            Collection `json:"collection"`
}

type Daemon struct {
	Version      string `json:"version"`
	Platform     string `json:"platform"`
	Architecture string `json:"architecture"`
	RunID        string `json:"run_id"`
}

type Collection struct {
	Mode            string     `json:"mode"`
	ReceivingSince  *time.Time `json:"receiving_since"`
	Forwarding      bool       `json:"forwarding"`
	Delivered       int64      `json:"delivered"`
	Lost            int64      `json:"lost"`
	Pending         int64      `json:"pending"`
	LastDeliveredAt *time.Time `json:"last_delivered_at"`
}

type Reporter interface {
	Send(context.Context, Report) (Response, error)
}

// Response는 서버가 확인한 정책 판이다. 적용 상태를 바꾸지 않는다.
type Response struct {
	ReceivedAt                 time.Time `json:"received_at"`
	ExpectedConfigRevision     *int      `json:"expected_config_revision"`
	AcknowledgedConfigRevision *int      `json:"acknowledged_config_revision"`
	ReportIntervalSeconds      int       `json:"report_interval_seconds"`
}

var (
	ErrInvalidResponse = errors.New("invalid_response")
	ErrInvalidEndpoint = errors.New("invalid_endpoint")
	ErrCredential      = errors.New("installation_credential_unavailable")
	ErrRequestFailed   = errors.New("request_failed")
	installationID     = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

type statusError int

func (e statusError) Error() string { return "http_status_" + strconv.Itoa(int(e)) }

// FailureReason은 외부 본문·URL·자격증명을 노출하지 않는 로그 분류만 반환한다.
func FailureReason(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, ErrInvalidResponse):
		return ErrInvalidResponse.Error()
	case errors.Is(err, ErrInvalidEndpoint):
		return ErrInvalidEndpoint.Error()
	case errors.Is(err, ErrCredential):
		return ErrCredential.Error()
	default:
		var status statusError
		if errors.As(err, &status) {
			return status.Error()
		}
		return ErrRequestFailed.Error()
	}
}

type Client struct {
	endpoint       string
	installationID string
	http           *http.Client
	loadCredential func() (*credential.Credential, error)
}

func NewClient(serverURL, id string) *Client {
	c := &Client{
		installationID: id,
		loadCredential: credential.LoadInstallation,
		http: &http.Client{
			Timeout:       RequestTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
	u, err := url.Parse(serverURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" || !installationID.MatchString(id) {
		return c
	}
	c.endpoint = u.JoinPath("api", "v1", "installations", id, "heartbeat").String()
	return c
}

// Send는 보고 한 번만 시도한다. 실패한 과거 보고를 쌓지 않고 다음 주기에 새 상태를 보낸다.
func (c *Client) Send(ctx context.Context, report Report) (Response, error) {
	if c.endpoint == "" {
		return Response{}, ErrInvalidEndpoint
	}
	ctx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	cred, err := c.loadCredential()
	if err != nil || cred == nil || cred.InstallationID != c.installationID || cred.InstallationToken == "" {
		return Response{}, ErrCredential
	}
	body, err := json.Marshal(report)
	if err != nil {
		return Response{}, ErrRequestFailed
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return Response{}, ErrRequestFailed
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cred.InstallationToken)
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
		return Response{}, ErrRequestFailed
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Response{}, statusError(resp.StatusCode)
	}
	// 응답 크기·필수 키를 검증한다. 오류에 서버 본문을 싣지 않는다.
	const maxBytes = 16 << 10
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if ctx.Err() != nil {
		return Response{}, ctx.Err()
	}
	if err != nil || len(data) > maxBytes {
		return Response{}, ErrInvalidResponse
	}
	var fields map[string]json.RawMessage
	var result Response
	if json.Unmarshal(data, &fields) != nil || json.Unmarshal(data, &result) != nil {
		return Response{}, ErrInvalidResponse
	}
	for _, key := range []string{"received_at", "expected_config_revision", "acknowledged_config_revision", "report_interval_seconds"} {
		if _, ok := fields[key]; !ok {
			return Response{}, ErrInvalidResponse
		}
	}
	if result.ReceivedAt.IsZero() || result.ReportIntervalSeconds <= 0 {
		return Response{}, ErrInvalidResponse
	}
	for _, revision := range []*int{result.ExpectedConfigRevision, result.AcknowledgedConfigRevision} {
		if revision != nil && *revision < 1 {
			return Response{}, ErrInvalidResponse
		}
	}
	return result, nil
}
