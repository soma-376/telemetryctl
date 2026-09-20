package vendorlimit

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

const (
	// defaultTimeout 은 사용 한도 조회 하나에 허용하는 시간이다.
	//
	// 이 조회는 부가 정보다. 상대가 느리다고 화면 전체가 멈추면 안 되고, 데몬 안에서
	// 주기적으로 부를 때는 다음 주기와 겹치지 않아야 한다. 짧게 잡고 실패는 unavailable 로 둔다.
	defaultTimeout = 10 * time.Second

	// maxResponseBytes 는 응답 본문 상한이다. 상대가 무엇을 보내든 우리 메모리는 우리가 정한다.
	maxResponseBytes = 1 << 20
)

// requestFailure 는 원본 오류를 보존하지 않는다. URL·호스트·본문·토큰이 섞인 오류는
// 타입으로 분류한 뒤 버리고, 우리가 정한 분류값과 HTTP 상태 코드만 경계 밖으로 보낸다.
type requestFailure struct {
	reason Reason
	kind   string
	phase  string
	status int
}

func (e *requestFailure) Error() string {
	detail := fmt.Sprintf("사용 한도 조회 실패 (kind=%s phase=%s", e.kind, e.phase)
	if e.status != 0 {
		detail += fmt.Sprintf(" http_status=%d", e.status)
	}
	return detail + ")"
}

// newHTTPClient 는 이 패키지 기본 클라이언트다. **타임아웃 없는 클라이언트를 쓰지 않는다** —
// 상대가 응답하지 않으면 호출 고루틴이 영원히 잠긴다.
func newHTTPClient() *http.Client {
	return &http.Client{Timeout: defaultTimeout}
}

// transportReason 은 전송 오류를 화면이 분기할 Reason 으로 옮긴다.
func transportReason(err error) Reason {
	var failure *requestFailure
	if errors.As(err, &failure) {
		return failure.reason
	}
	return ReasonInternal
}

// transportFailure 는 문자열로 바꾸기 전에 감싸진 오류 타입까지 확인한다.
// phase 와 kind 에는 코드에서 정한 값만 넣는다. 원본 오류의 문자열을 사용하지 않는다.
func transportFailure(err error, phase string, status int) *requestFailure {
	failure := &requestFailure{reason: ReasonNetwork, kind: "transport", phase: phase, status: status}
	var dns *net.DNSError
	var network net.Error
	var verification *tls.CertificateVerificationError
	var authority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	var record tls.RecordHeaderError
	var operation *net.OpError
	switch {
	case errors.Is(err, context.Canceled):
		failure.reason, failure.kind = ReasonCanceled, "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		failure.reason, failure.kind = ReasonTimeout, "timeout"
	case errors.As(err, &dns):
		failure.reason, failure.kind = ReasonDNS, "dns"
		if dns.Timeout() {
			failure.reason, failure.kind = ReasonTimeout, "dns_timeout"
		}
	case errors.As(err, &network) && network.Timeout():
		failure.reason, failure.kind = ReasonTimeout, "timeout"
	case errors.As(err, &verification), errors.As(err, &authority), errors.As(err, &hostname),
		errors.As(err, &invalid), errors.As(err, &record):
		failure.reason, failure.kind = ReasonTLS, "tls"
	case errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
		failure.kind = "connection_closed"
	case errors.As(err, &operation):
		failure.kind = "connection"
	}
	return failure
}

// getJSON 은 HTTP 응답 상태, 본문 수신, JSON 해석을 각각 판정한다.
// 401 은 인증 거부이며 만료의 증거는 아니다. 403·429·5xx 도 통신 오류와 구분한다.
func getJSON(ctx context.Context, client *http.Client, endpoint string, tok Token, headers map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return &requestFailure{reason: ReasonInternal, kind: "invalid_request", phase: "request"}
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	req.Header.Set("Authorization", "Bearer "+tok.reveal())

	resp, err := client.Do(req)
	if err != nil {
		return transportFailure(err, "transport", 0)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		failure := &requestFailure{
			reason: ReasonUpstreamStatus, kind: "http_status", phase: "response_headers", status: resp.StatusCode,
		}
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			failure.reason, failure.kind = ReasonAuthRejected, "auth_rejected"
		case http.StatusForbidden:
			failure.reason, failure.kind = ReasonAccessDenied, "access_denied"
		case http.StatusTooManyRequests:
			failure.reason, failure.kind = ReasonRateLimited, "rate_limited"
		}
		// 오류 응답의 본문은 읽지 않는다. 인증 정보가 반사될 수 있고 읽기 자체가 지연될 수 있다.
		return failure
	}

	// JSON 해석 전에 수신을 끝낸다. 완전한 JSON 접두부 뒤에 연결이 끊겨도 성공으로 보지 않는다.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return transportFailure(err, "response_body", resp.StatusCode)
	}
	if len(body) > maxResponseBytes {
		return &requestFailure{
			reason: ReasonResponseUnrecognized, kind: "body_too_large", phase: "response_body", status: resp.StatusCode,
		}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return &requestFailure{
			reason: ReasonResponseUnrecognized, kind: "invalid_json", phase: "decode", status: resp.StatusCode,
		}
	}
	return nil
}
