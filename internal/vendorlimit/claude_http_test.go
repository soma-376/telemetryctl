package vendorlimit

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
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
		wantErr    error
		wantReason Reason
	}{
		{name: "200 은 성공", status: 200, body: `{"ok":true}`},
		{name: "401 은 토큰 만료", status: 401, wantErr: errUnauthorized, wantReason: ReasonTokenExpired},
		{name: "403 도 토큰 만료", status: 403, wantErr: errUnauthorized, wantReason: ReasonTokenExpired},
		{name: "429 는 상위 상태", status: 429, wantErr: errStatus, wantReason: ReasonUpstreamStatus},
		{name: "500 은 상위 상태", status: 500, wantErr: errStatus, wantReason: ReasonUpstreamStatus},
		{name: "404 는 상위 상태 — 엔드포인트가 사라진 경우", status: 404, wantErr: errStatus, wantReason: ReasonUpstreamStatus},
		{name: "본문이 JSON 이 아니면 응답 미인식", status: 200, body: `<html>maintenance</html>`, wantErr: errUnrecognized, wantReason: ReasonResponseUnrecognized},
		{name: "빈 본문도 응답 미인식", status: 200, body: ``, wantErr: errUnrecognized, wantReason: ReasonResponseUnrecognized},
		{name: "연결 실패는 네트워크 오류", dead: true, wantErr: errNetwork, wantReason: ReasonNetwork},
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
					_, _ = fmt.Fprintf(w, "%s", tc.body+r.Header.Get("Authorization"))
				})
				endpoint = up.srv.URL
			}

			var out map[string]any
			err := getJSON(context.Background(), newHTTPClient(), endpoint, newToken(claudeCanary), nil, &out)

			if tc.wantErr == nil {
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
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("errors.Is(%v, %v) = false", err, tc.wantErr)
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
	if !errors.Is(err, errNetwork) {
		t.Fatalf("errors.Is(%v, errNetwork) = false", err)
	}
	assertNoSecret(t, "취소 오류 문자열", []string{err.Error()}, claudeCanary)
}

// *url.Error 는 요청 URL 을 통째로 담아 온다. 질의 문자열이나 userinfo 에 비밀이 실리면
// %v 한 번으로 그대로 로그에 남는다.
func TestSanitizeErr는URL을버린다(t *testing.T) {
	t.Parallel()
	cause := errors.New("connection refused")
	wrapped := &url.Error{
		Op:  "Get",
		URL: "https://user:LEAK-SECRET@api.example.com/usage?token=LEAK-SECRET",
		Err: cause,
	}
	got := sanitizeErr(wrapped)
	if strings.Contains(got.Error(), "LEAK-SECRET") {
		t.Fatalf("URL 이 남아 있다: %v", got)
	}
	if !errors.Is(got, cause) {
		t.Fatalf("원인이 사라졌다: %v", got)
	}
	plain := errors.New("그냥 오류")
	if sanitizeErr(plain) != plain {
		t.Error("url.Error 가 아닌 오류가 변형됐다")
	}
	if sanitizeErr(nil) != nil {
		t.Error("nil 이 변형됐다")
	}
}

func TestTransportReason은모르는오류를내부오류로본다(t *testing.T) {
	t.Parallel()
	if got := transportReason(errors.New("모르는 오류")); got != ReasonInternal {
		t.Errorf("transportReason = %q, want %q", got, ReasonInternal)
	}
}
