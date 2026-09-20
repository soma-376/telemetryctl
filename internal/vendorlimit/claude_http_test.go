package vendorlimit

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestGetJSON은실패종류를Reason으로가른다(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		status     int
		body       string
		dead       bool
		wantReason Reason
	}{
		{name: "200 은 성공", status: 200, body: `{"ok":true}`},
		{name: "401 은 인증 거부", status: 401, wantReason: ReasonAuthRejected},
		{name: "403 은 접근 거부", status: 403, wantReason: ReasonAccessDenied},
		{name: "429 는 요청 빈도 제한", status: 429, wantReason: ReasonRateLimited},
		{name: "500 은 상위 상태", status: 500, wantReason: ReasonUpstreamStatus},
		{name: "404 는 상위 상태 — 엔드포인트가 사라진 경우", status: 404, wantReason: ReasonUpstreamStatus},
		{name: "본문이 JSON 이 아니면 응답 미인식", status: 200, body: `<html>maintenance</html>`, wantReason: ReasonResponseUnrecognized},
		{name: "빈 본문도 응답 미인식", status: 200, body: ``, wantReason: ReasonResponseUnrecognized},
		{name: "연결 실패는 네트워크 오류", dead: true, wantReason: ReasonNetwork},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			endpoint := ""
			if tc.dead {
				endpoint = deadUpstream(t)
			} else {
				up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(tc.status)
					// 요청 헤더를 그대로 되비추는 상위를 흉내 낸다. 오류 문자열이 본문을
					// 싣는 순간 토큰이 새는 가장 현실적인 경로다.
					_, _ = fmt.Fprint(w, tc.body)
					if tc.status >= 400 {
						_, _ = fmt.Fprint(w, r.Header.Get("Authorization"))
					}
				})
				endpoint = up.srv.URL
			}

			var out map[string]any
			err := getJSON(context.Background(), newHTTPClient(), endpoint, newToken(claudeCanary), nil, &out)

			if tc.wantReason == ReasonNone {
				if err != nil {
					t.Fatalf("getJSON: %v", err)
				}
				if out["ok"] != true {
					t.Errorf("본문을 못 읽었다: %v", out)
				}
				return
			}
			if err == nil {
				t.Fatal("실패해야 하는데 성공했다")
			}
			if got := transportReason(err); got != tc.wantReason {
				t.Errorf("transportReason = %q, want %q", got, tc.wantReason)
			}
			assertNoSecret(t, "전송 오류 문자열", []string{err.Error()}, claudeCanary, "Bearer ")
		})
	}
}

func TestGetJSON은토큰과추가헤더를요청에싣는다(t *testing.T) {
	t.Parallel()
	up := jsonUpstream(t, `{"ok":true}`)

	var out map[string]any
	err := getJSON(context.Background(), newHTTPClient(), up.srv.URL,
		newToken(codexCanary), map[string]string{
			"chatgpt-account-id": accountCanary,
			"빈 헤더는 붙지 않는다":       "",
		}, &out)
	if err != nil {
		t.Fatalf("getJSON: %v", err)
	}

	// 이 단언이 없으면 "토큰이 안 샜다" 는 다른 테스트가 공허해진다 — 애초에 토큰을
	// 싣지 않았을 뿐일 수 있다.
	if up.auth() != "Bearer "+codexCanary {
		t.Fatalf("Authorization 헤더 = %q", up.auth())
	}
	if got := up.header("chatgpt-account-id"); got != accountCanary {
		t.Errorf("추가 헤더가 빠졌다: %q", got)
	}
	if up.hasHeader("빈 헤더는 붙지 않는다") {
		t.Error("빈 값 헤더가 붙었다")
	}
	if up.header("Accept") != "application/json" {
		t.Errorf("Accept = %q", up.header("Accept"))
	}
}

func TestGetJSON은컨텍스트취소를따른다(t *testing.T) {
	t.Parallel()
	blocked := make(chan struct{})
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		<-blocked
	})
	t.Cleanup(func() { close(blocked) })

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	var out map[string]any
	err := getJSON(ctx, newHTTPClient(), up.srv.URL, newToken(claudeCanary), nil, &out)
	if err == nil {
		t.Fatal("취소됐는데 성공했다")
	}
	if got := transportReason(err); got != ReasonTimeout {
		t.Fatalf("reason = %q, want %q", got, ReasonTimeout)
	}
	assertNoSecret(t, "취소 오류 문자열", []string{err.Error()}, claudeCanary)
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGetJSON은감싸진통신오류를분류하고원문을버린다(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		err    error
		reason Reason
		kind   string
	}{
		{"DNS", &net.DNSError{Err: claudeCanary, Name: "private.example"}, ReasonDNS, "dns"},
		{"DNS 타임아웃", &net.DNSError{Err: claudeCanary, IsTimeout: true}, ReasonTimeout, "dns_timeout"},
		{"기한 초과", fmt.Errorf("%s: %w", claudeCanary, context.DeadlineExceeded), ReasonTimeout, "timeout"},
		{"소켓 타임아웃", &net.OpError{Op: "read", Err: os.ErrDeadlineExceeded}, ReasonTimeout, "timeout"},
		{"취소", fmt.Errorf("%s: %w", claudeCanary, context.Canceled), ReasonCanceled, "canceled"},
		{"연결 거부", &net.OpError{Op: "dial", Err: errors.New(claudeCanary)}, ReasonNetwork, "connection"},
		{"인증서 검증", &tls.CertificateVerificationError{Err: errors.New(claudeCanary)}, ReasonTLS, "tls"},
		{"인증서 발급자", x509.UnknownAuthorityError{}, ReasonTLS, "tls"},
		{"인증서 호스트", x509.HostnameError{Host: claudeCanary}, ReasonTLS, "tls"},
		{"인증서 만료", x509.CertificateInvalidError{Detail: claudeCanary}, ReasonTLS, "tls"},
		{"TLS 응답", tls.RecordHeaderError{Msg: claudeCanary}, ReasonTLS, "tls"},
		{"기타 통신 오류", errors.New(claudeCanary), ReasonNetwork, "transport"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return nil, &url.Error{Op: "Get", URL: "https://private.example/?token=" + claudeCanary, Err: tc.err}
			})}
			err := getJSON(context.Background(), client, "https://api.example/usage", newToken(claudeCanary), nil, new(map[string]any))
			assertRequestFailure(t, err, tc.reason, tc.kind, "transport", 0)
		})
	}
}

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

type trackedBody struct {
	io.Reader
	read   int
	closed bool
}

func (b *trackedBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.read += n
	return n, err
}

func (b *trackedBody) Close() error { b.closed = true; return nil }

func TestGetJSON은본문수신실패와해석실패를구분한다(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		body        io.Reader
		reason      Reason
		kind, phase string
	}{
		{"본문 타임아웃", io.MultiReader(strings.NewReader(`{"ok":`), errorReader{context.DeadlineExceeded}), ReasonTimeout, "timeout", "response_body"},
		{"본문 취소", errorReader{context.Canceled}, ReasonCanceled, "canceled", "response_body"},
		{"JSON 뒤 연결 끊김", io.MultiReader(strings.NewReader(`{"ok":true}`), errorReader{io.ErrUnexpectedEOF}), ReasonNetwork, "connection_closed", "response_body"},
		{"수신 오류 원문 제거", errorReader{errors.New(claudeCanary)}, ReasonNetwork, "transport", "response_body"},
		{"잘못된 JSON", strings.NewReader(claudeCanary), ReasonResponseUnrecognized, "invalid_json", "decode"},
		{"잘린 JSON", strings.NewReader(`{"ok":`), ReasonResponseUnrecognized, "invalid_json", "decode"},
		{"JSON 뒤 추가 원문", strings.NewReader(`{"ok":true}` + claudeCanary), ReasonResponseUnrecognized, "invalid_json", "decode"},
		{"본문 크기 초과", strings.NewReader(`{"ok":true}` + strings.Repeat(" ", maxResponseBytes)), ReasonResponseUnrecognized, "body_too_large", "response_body"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := &trackedBody{Reader: tc.body}
			client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
			})}
			err := getJSON(context.Background(), client, "https://api.example/usage", newToken(claudeCanary), nil, new(map[string]any))
			assertRequestFailure(t, err, tc.reason, tc.kind, tc.phase, http.StatusOK)
			if !body.closed || body.read > maxResponseBytes+1 {
				t.Fatalf("본문 자원 제한 위반: closed=%v read=%d", body.closed, body.read)
			}
		})
	}
}

func TestGetJSON은HTTP오류본문을읽지않는다(t *testing.T) {
	t.Parallel()
	body := &trackedBody{Reader: strings.NewReader(claudeCanary)}
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusForbidden, Body: body}, nil
	})}
	err := getJSON(context.Background(), client, "https://api.example/usage", newToken(claudeCanary), nil, new(map[string]any))
	assertRequestFailure(t, err, ReasonAccessDenied, "access_denied", "response_headers", http.StatusForbidden)
	if body.read != 0 || !body.closed {
		t.Fatalf("오류 본문을 읽었거나 닫지 않았다: read=%d closed=%v", body.read, body.closed)
	}
}

func assertRequestFailure(t *testing.T, err error, reason Reason, kind, phase string, status int) {
	t.Helper()
	var failure *requestFailure
	if !errors.As(err, &failure) {
		t.Fatalf("분류된 오류가 아니다: %v", err)
	}
	if transportReason(err) != reason || failure.kind != kind || failure.phase != phase || failure.status != status {
		t.Fatalf("failure=%+v reason=%s, want %s/%s/%s/%d", failure, transportReason(err), reason, kind, phase, status)
	}
	assertNoSecret(t, "오류 내부와 출력", append(allStrings(failure), err.Error(), fmt.Sprintf("%#v", err)), claudeCanary, "private.example", "Bearer ")
}

func TestTransportReason은모르는오류를내부오류로본다(t *testing.T) {
	t.Parallel()
	if got := transportReason(errors.New("모르는 오류")); got != ReasonInternal {
		t.Errorf("transportReason = %q, want %q", got, ReasonInternal)
	}
}
