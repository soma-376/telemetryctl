package updatecheck

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientPreservesBasePathAndUsesServerDecision(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/enrollment/api/v1/check-updates" {
			t.Errorf("요청 = %s %s", r.Method, r.URL.Path)
		}
		q := r.URL.Query()
		if len(q) != 3 || q.Get("current_version") != "1.0.0+dev & local" || q.Get("platform") != "darwin" || q.Get("architecture") != "arm64" {
			t.Errorf("query = %v", q)
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.ContentLength > 0 {
			t.Error("업데이트 조회에 인증이나 본문이 붙었다")
		}
		_, _ = fmt.Fprint(w, `{"latest_version":"0.9.0","update_available":true,"future_field":1}`)
	}))
	defer srv.Close()
	client := NewClient(srv.URL+"/enrollment/", "1.0.0+dev & local", "darwin", "arm64")
	got, err := client.Check(context.Background())
	if err != nil || got.LatestVersion != "0.9.0" || !got.UpdateAvailable {
		t.Fatalf("서버 판정을 그대로 사용해야 함: result=%+v err=%v", got, err)
	}
}

func TestClientResponseContract(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"current", 200, `{"latest_version":"1.0.0","update_available":false}`, ""},
		{"whitespace", 200, "{\"latest_version\":\"1.0.0\",\"update_available\":false}\n\t", ""},
		{"missing_version", 200, `{"update_available":false}`, "invalid_response"},
		{"empty_version", 200, `{"latest_version":" ","update_available":false}`, "invalid_response"},
		{"missing_available", 200, `{"latest_version":"1.0.0"}`, "invalid_response"},
		{"null_available", 200, `{"latest_version":"1.0.0","update_available":null}`, "invalid_response"},
		{"string_available", 200, `{"latest_version":"1.0.0","update_available":"false"}`, "invalid_response"},
		{"null", 200, `null`, "invalid_response"},
		{"malformed", 200, `{`, "invalid_response"},
		{"trailing_json", 200, `{"latest_version":"1.0.0","update_available":false}{}`, "invalid_response"},
		{"trailing_text", 200, `{"latest_version":"1.0.0","update_available":false} garbage`, "invalid_response"},
		{"oversized", 200, `{"latest_version":"` + strings.Repeat("v", maxResponseBytes) + `","update_available":false}`, "invalid_response"},
		{"unsupported", 404, "private response body", "unsupported"},
		{"server_error", 503, "private response body", "http_status_503"},
		{"empty_success", 204, "", "http_status_204"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			got, err := NewClient(srv.URL, "1.0.0", "linux", "amd64").Check(context.Background())
			if tc.want == "" {
				if err != nil || got.LatestVersion != "1.0.0" || got.UpdateAvailable {
					t.Fatalf("result=%+v err=%v", got, err)
				}
			} else if err == nil || FailureReason(err) != tc.want {
				t.Fatalf("실패 분류=%v, want %s", err, tc.want)
			}
		})
	}
}

func TestClientRejectsRedirectAndInvalidEndpoints(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirected.Add(1)
		_, _ = fmt.Fprint(w, `{"latest_version":"1.0.0","update_available":false}`)
	}))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer origin.Close()
	_, err := NewClient(origin.URL, "1", "linux", "amd64").Check(context.Background())
	if err == nil || FailureReason(err) != "http_status_302" || redirected.Load() != 0 {
		t.Fatalf("redirect=%d err=%v", redirected.Load(), err)
	}
	for _, endpoint := range []string{"", "invalid", "file:///tmp/update", "https://name:password@example.com", "https://example.com?secret=value", "https://example.com/#fragment"} {
		_, err := NewClient(endpoint, "1", "linux", "amd64").Check(context.Background())
		if !errors.Is(err, ErrInvalidEndpoint) {
			t.Errorf("잘못된 endpoint의 분류 = %v", err)
		}
	}
}

func TestClientHonorsCancellationAndDeadline(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(fmt.Sprint("deadline=", deadline), func(t *testing.T) {
			entered := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				close(entered)
				<-r.Context().Done()
			}))
			defer srv.Close()
			ctx, cancel := context.WithCancel(context.Background())
			if deadline {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
			}
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := NewClient(srv.URL, "1", "linux", "amd64").Check(ctx)
				done <- err
			}()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("HTTP 요청이 시작되지 않았다")
			}
			if !deadline {
				cancel()
			}
			select {
			case err := <-done:
				want := "canceled"
				if deadline {
					want = "timeout"
				}
				if FailureReason(err) != want {
					t.Fatalf("분류 = %s, want %s", FailureReason(err), want)
				}
			case <-time.After(time.Second):
				t.Fatal("취소 후 HTTP 조회가 반환하지 않았다")
			}
		})
	}
}

func TestFailureReasonNeverIncludesRawError(t *testing.T) {
	if got := FailureReason(errors.New("GET https://private.example.com/?secret=value: private response")); got != "request_failed" {
		t.Fatalf("분류가 원본 오류를 노출함: %q", got)
	}
}
