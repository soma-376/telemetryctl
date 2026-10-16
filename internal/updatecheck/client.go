package updatecheck

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// RequestTimeout 은 업데이트 확인 한 번의 전체 제한이다.
const RequestTimeout = 10 * time.Second

const maxResponseBytes = 64 << 10

var (
	ErrUnsupported     = errors.New("unsupported")
	ErrInvalidEndpoint = errors.New("invalid_endpoint")
	ErrInvalidResponse = errors.New("invalid_response")
	ErrRequestFailed   = errors.New("request_failed")
)

type httpStatusError int

func (e httpStatusError) Error() string { return "http_status_" + strconv.Itoa(int(e)) }

// FailureReason 은 URL·응답 본문·원본 네트워크 오류를 노출하지 않는 로그 분류다.
func FailureReason(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, ErrUnsupported):
		return "unsupported"
	case errors.Is(err, ErrInvalidEndpoint):
		return "invalid_endpoint"
	case errors.Is(err, ErrInvalidResponse):
		return "invalid_response"
	default:
		var status httpStatusError
		if errors.As(err, &status) {
			return status.Error()
		}
		return "request_failed"
	}
}

// Client 는 인증 없이 등록 서버의 업데이트 확인 endpoint를 조회한다.
type Client struct {
	endpoint string
	http     *http.Client
	valid    bool
}

// NewClient 는 등록 서버의 base path를 유지한다. 잘못된 주소는 조회 실패로 처리하여
// 업데이트 설정 문제 때문에 데몬 수집까지 중단하지 않는다.
func NewClient(serverURL, version, platform, architecture string) *Client {
	c := &Client{http: &http.Client{
		Timeout: RequestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
	u, err := url.Parse(serverURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return c
	}
	u = u.JoinPath("api", "v1", "check-updates")
	q := url.Values{}
	q.Set("current_version", version)
	q.Set("platform", platform)
	q.Set("architecture", architecture)
	u.RawQuery = q.Encode()
	c.endpoint, c.valid = u.String(), true
	return c
}

func (c *Client) Check(ctx context.Context) (Result, error) {
	if !c.valid {
		return Result{}, ErrInvalidEndpoint
	}
	ctx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint, nil)
	if err != nil {
		return Result{}, ErrInvalidEndpoint
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		return Result{}, ErrRequestFailed
	}
	defer resp.Body.Close() //nolint:errcheck // 응답을 읽은 뒤 닫기 실패는 조회 결과를 바꾸지 않는다.
	if resp.StatusCode == http.StatusNotFound {
		return Result{}, ErrUnsupported
	}
	if resp.StatusCode != http.StatusOK {
		return Result{}, httpStatusError(resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		return Result{}, ErrRequestFailed
	}
	if len(body) > maxResponseBytes {
		return Result{}, ErrInvalidResponse
	}
	var wire struct {
		LatestVersion   string `json:"latest_version"`
		UpdateAvailable *bool  `json:"update_available"`
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(&wire); err != nil || strings.TrimSpace(wire.LatestVersion) == "" || wire.UpdateAvailable == nil {
		return Result{}, ErrInvalidResponse
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return Result{}, ErrInvalidResponse
	}
	return Result{LatestVersion: wire.LatestVersion, UpdateAvailable: *wire.UpdateAvailable}, nil
}
