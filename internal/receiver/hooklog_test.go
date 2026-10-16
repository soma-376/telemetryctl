package receiver

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 인증 실패도 도착으로 관측하되, 본문·쿼리·자격증명을 로그에 복사하지 않는다.
func TestHookArrivalAndResponseLogs(t *testing.T) {
	for _, status := range []int{204, 400, 401, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			called := false
			rc, _, logs := newTestReceiver(t, func(opt *Options) {
				opt.LocalAPI = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					called = true
					w.WriteHeader(status)
				})
			})
			req := httptest.NewRequest(http.MethodPost, HookSessionEndPath+"?secret=query-secret", strings.NewReader("body-secret"))
			req.Header.Set("Authorization", "Bearer "+testToken)
			req.Header.Set(LocalHeader, LocalHeaderValue)
			if status == 401 {
				req.Header.Set("Authorization", "Bearer wrong-secret")
			}
			req = req.WithContext(context.WithValue(req.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 57968}))
			response := httptest.NewRecorder()
			rc.ServeHTTP(response, req)
			if response.Code != status || called != (status != 401) {
				t.Fatalf("status=%d called=%t", response.Code, called)
			}
			for _, want := range []string{"세션 훅 HTTP 도착:", "세션 훅 HTTP 응답:", "127.0.0.1:57968", fmt.Sprintf("status=%d", status), "pid="} {
				if !strings.Contains(logs.String(), want) {
					t.Errorf("로그에 %q 없음: %s", want, logs.String())
				}
			}
			for _, secret := range []string{testToken, "wrong-secret", "query-secret", "body-secret"} {
				if strings.Contains(logs.String(), secret) {
					t.Errorf("로그에 비밀 값 노출: %q", secret)
				}
			}
		})
	}
}
