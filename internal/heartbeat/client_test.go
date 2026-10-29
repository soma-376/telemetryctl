package heartbeat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/credential"
)

const testID = "613bf911-4761-4328-9f59-7040f309e531"

func testClient(base string) *Client {
	c := NewClient(base, testID)
	c.loadCredential = func() (*credential.Credential, error) {
		return &credential.Credential{InstallationID: testID, InstallationToken: "pit_test-only"}, nil
	}
	return c
}

func TestSendMatchesServerContract(t *testing.T) {
	const expected = `{"sent_at":"2026-10-29T01:00:00Z","daemon":{"version":"1.2.3","platform":"darwin","architecture":"arm64","run_id":"0123456789abcdef"},"applied_config_revision":3,"collection":{"mode":"local","receiving_since":null,"forwarding":false,"delivered":0,"lost":0,"pending":0,"last_delivered_at":null}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/enrollment/api/v1/installations/"+testID+"/heartbeat" {
			t.Errorf("요청 = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer pit_test-only" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("설치 자격증명 또는 Content-Type이 다름")
		}
		var got, want any
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		_ = json.Unmarshal([]byte(expected), &want)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("본문 = %#v, want %#v", got, want)
		}
		// 응답의 주기·정책 판을 클라이언트 설정으로 적용하지 않는다.
		_, _ = io.WriteString(w, `{"received_at":"2026-10-29T01:00:00Z","expected_config_revision":8,"acknowledged_config_revision":3,"report_interval_seconds":300}`)
	}))
	defer srv.Close()
	r := Report{SentAt: time.Date(2026, 10, 29, 1, 0, 0, 0, time.UTC),
		Daemon:                Daemon{Version: "1.2.3", Platform: "darwin", Architecture: "arm64", RunID: "0123456789abcdef"},
		AppliedConfigRevision: 3, Collection: Collection{Mode: "local"}}
	if _, err := testClient(srv.URL+"/enrollment/").Send(context.Background(), r); err != nil {
		t.Fatal(err)
	}
}

func TestCredentialMustMatchInstallation(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1) }))
	defer srv.Close()
	for _, cred := range []*credential.Credential{nil, {}, {InstallationID: "other", InstallationToken: "pit_secret"}, {InstallationID: testID}} {
		c := testClient(srv.URL)
		c.loadCredential = func() (*credential.Credential, error) { return cred, nil }
		if _, err := c.Send(context.Background(), Report{}); !errors.Is(err, ErrCredential) {
			t.Fatalf("자격증명 불일치를 거부하지 않음: %v", err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("자격증명 없는 보고가 전송됨")
	}
}

func TestSendDoesNotFollowRedirectOrExposeResponse(t *testing.T) {
	for _, status := range []int{301, 302, 307, 308, 400, 401, 403, 404, 429, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Location", "/leak")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, "pit_do-not-log")
			}))
			defer srv.Close()
			_, err := testClient(srv.URL).Send(context.Background(), Report{})
			if !errors.Is(err, statusError(status)) || FailureReason(err) != statusError(status).Error() {
				t.Fatalf("실패 분류 = %v", err)
			}
			if calls.Load() != 1 || strings.Contains(err.Error(), "pit_") {
				t.Fatal("리다이렉트를 따르거나 응답을 노출함")
			}
		})
	}
}

func TestSendTimesOutAndCancelsHTTP(t *testing.T) {
	entered, ended := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(entered)
		<-r.Context().Done()
		close(ended)
	}))
	defer srv.Close()
	c := testClient(srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := c.Send(ctx, Report{}); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("요청이 시작되지 않음")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("취소 = %v", err)
	}
	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("서버 요청이 취소되지 않음")
	}
	ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if _, err := c.Send(ctx, Report{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("만료 = %v", err)
	}
}

func TestInvalidEndpointAndExternalErrorsStayPrivate(t *testing.T) {
	for _, base := range []string{"", "ftp://example.test", "https://user:secret@example.test", "https://example.test?token=secret", "https://example.test#secret"} {
		if _, err := testClient(base).Send(context.Background(), Report{}); !errors.Is(err, ErrInvalidEndpoint) {
			t.Errorf("잘못된 주소 거부 실패: %v", err)
		}
	}
	c := NewClient("https://example.test", "../other")
	if _, err := c.Send(context.Background(), Report{}); !errors.Is(err, ErrInvalidEndpoint) {
		t.Fatal("설치 ID 경로 이탈을 허용함")
	}
	c = testClient("https://example.test")
	c.loadCredential = func() (*credential.Credential, error) { return nil, errors.New("pit_private") }
	if _, err := c.Send(context.Background(), Report{}); FailureReason(err) != ErrCredential.Error() {
		t.Fatalf("키링 오류를 그대로 노출함: %v", err)
	}
	if FailureReason(errors.New("pit_private")) != "request_failed" {
		t.Fatal("알 수 없는 오류를 그대로 노출함")
	}
}

func TestPolicyResponseValidation(t *testing.T) {
	valid := `{"received_at":"2026-10-29T01:00:00Z","expected_config_revision":4,"acknowledged_config_revision":3,"report_interval_seconds":300}`
	for _, body := range []string{"", `{}`, `null`, valid + `{}`, strings.Replace(valid, `:4`, `:0`, 1), strings.Replace(valid, `:4`, `:"secret"`, 1), strings.Repeat("x", 16385)} {
		t.Run(body[:min(len(body), 20)], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }))
			defer server.Close()
			if _, err := testClient(server.URL).Send(context.Background(), Report{}); !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("응답 검증 실패: %v", err)
			}
		})
	}
	for _, nullable := range []bool{false, true} {
		body := valid
		if nullable {
			body = strings.Replace(body, `:4`, `:null`, 1)
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }))
		result, err := testClient(server.URL).Send(context.Background(), Report{})
		server.Close()
		if err != nil || result.ReportIntervalSeconds != 300 || (result.ExpectedConfigRevision == nil) != nullable {
			t.Fatalf("서버 정책 읽기 실패: %+v, %v", result, err)
		}
	}
}
