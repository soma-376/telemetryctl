package vendorlimit

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// 테스트 전역에서 쓰는 고정 시각. 결과의 observed_at 이 흔들리지 않아야 단언이 단순해진다.
var testNow = time.Date(2026, 8, 28, 3, 4, 5, 0, time.UTC)

func fixedNow() time.Time { return testNow }

// 카나리아 토큰. 이 문자열이 결과·오류·로그 어디에 나타나면 그 경로가 새는 것이다.
const (
	claudeCanary  = "sk-ant-oat01-CANARY-CLAUDE-must-never-appear"
	codexCanary   = "eyJhbGciOiJI.CANARY-CODEX-must-never-appear.sig"
	accountCanary = "acct-CANARY-0000"
)

// --- 자격증명 파일 픽스처 ---------------------------------------------------

// newHome 은 자격증명 파일을 심을 가짜 홈 디렉터리를 만든다.
func newHome(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// writeFileAt 은 중간 디렉터리를 만들고 파일을 쓴다. 자격증명 파일 권한(0600)을 흉내 낸다.
func writeFileAt(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func writeClaudeCredential(t *testing.T, home, body string) {
	t.Helper()
	writeFileAt(t, claudeCredentialPath(home), body, 0o600)
}

func writeCodexAuth(t *testing.T, home, body string) {
	t.Helper()
	writeFileAt(t, codexCredentialPath(home), body, 0o600)
}

// claudeCredentialJSON 은 관측된 모양의 Claude 자격증명 파일을 만든다.
func claudeCredentialJSON(token string, expiresAt time.Time, plan string) string {
	var ms int64
	if !expiresAt.IsZero() {
		ms = expiresAt.UnixMilli()
	}
	return fmt.Sprintf(`{
  "claudeAiOauth": {
    "accessToken": %q,
    "refreshToken": "refresh-token-value",
    "expiresAt": %d,
    "scopes": ["user:inference", "user:profile"],
    "subscriptionType": %q
  }
}`, token, ms, plan)
}

// codexAuthJSON 은 관측된 모양의 Codex auth.json 을 만든다.
func codexAuthJSON(token, accountID string) string {
	return fmt.Sprintf(`{
  "OPENAI_API_KEY": null,
  "tokens": {
    "id_token": "id-token-value",
    "access_token": %q,
    "refresh_token": "refresh-token-value",
    "account_id": %q
  },
  "last_refresh": "2026-08-28T00:00:00Z"
}`, token, accountID)
}

// makeUnreadable 은 파일에서 읽기 권한을 뺀다. 권한 개념이 다른 환경에서는 테스트를 건너뛴다 —
// root 는 0000 파일도 읽고, Windows 는 chmod 가 의미 없다.
func makeUnreadable(t *testing.T, path string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows 에서는 chmod 로 읽기 권한을 뺄 수 없다")
	}
	if os.Geteuid() == 0 {
		t.Skip("root 는 권한 0000 파일도 읽는다")
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
}

// --- 모의 상위 서버 ---------------------------------------------------------

// upstream 은 벤더 사용량 API 를 흉내 내는 httptest 서버다.
//
// **실제 벤더 엔드포인트로는 절대 요청하지 않는다.** 테스트는 어댑터가 조립한 요청과
// 응답 해석만 검증하고, 실제 계약은 어댑터 상단 주석의 「가정과 확인 방법」이 책임진다.
//
// Collect 가 벤더별 조회를 동시에 던지므로 핸들러도 동시에 돈다. 관측값은 뮤텍스로 지킨다 —
// 여기서 경합이 나면 -race 가 테스트를 죽이고, 그건 우리 코드가 아니라 도우미의 버그다.
type upstream struct {
	srv *httptest.Server

	mu sync.Mutex
	// lastAuth 는 마지막 요청의 Authorization 헤더다. 어댑터가 토큰을 제대로 실었는지
	// 확인하는 유일한 근거다 — 이 확인이 없으면 "토큰이 안 샜다" 는 단언이 공허해진다.
	lastAuth    string
	lastHeaders http.Header
	lastPath    string
	calls       int
}

// newUpstream 은 handler 로 응답하는 서버를 띄운다.
func newUpstream(t *testing.T, handler http.HandlerFunc) *upstream {
	t.Helper()
	up := &upstream{}
	up.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up.mu.Lock()
		up.calls++
		up.lastAuth = r.Header.Get("Authorization")
		up.lastHeaders = r.Header.Clone()
		up.lastPath = r.URL.Path
		up.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(up.srv.Close)
	return up
}

func (u *upstream) auth() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.lastAuth
}

func (u *upstream) path() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.lastPath
}

func (u *upstream) header(name string) string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.lastHeaders.Get(name)
}

// hasHeader 는 헤더가 아예 붙지 않았는지 본다. 빈 값과 없음을 구분해야 한다.
func (u *upstream) hasHeader(name string) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	_, ok := u.lastHeaders[http.CanonicalHeaderKey(name)]
	return ok
}

// jsonUpstream 은 고정 본문을 200 으로 돌려주는 서버다.
func jsonUpstream(t *testing.T, body string) *upstream {
	t.Helper()
	return newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
}

// statusUpstream 은 상태 코드만 돌려주는 서버다. 본문에 카나리아를 실어 두어,
// 오류 문자열이 본문을 되싣지 않는지도 같이 검증한다.
func statusUpstream(t *testing.T, code int) *upstream {
	t.Helper()
	return newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
		_, _ = fmt.Fprintf(w, `{"error":"요청 거부","echo":%q}`, r.Header.Get("Authorization"))
	})
}

// deadUpstream 은 이미 닫힌 서버의 주소다. 연결 자체가 실패하는 경로를 태운다.
func deadUpstream(t *testing.T) string {
	t.Helper()
	up := newUpstream(t, func(http.ResponseWriter, *http.Request) {})
	addr := up.srv.URL
	up.srv.Close()
	return addr
}

// --- 단언 도우미 ------------------------------------------------------------

// collectStrings 는 값 안의 모든 문자열을 재귀로 모은다. 비공개 필드도 Kind 만 보고 읽으므로
// Token 처럼 값을 숨긴 타입의 **내부까지** 훑는다 — 프라이버시 단언이 "출력 어디에도" 를
// 문자 그대로 검사한다 (internal/session/helper_test.go 와 같은 도구).
func collectStrings(v reflect.Value, out *[]string) {
	switch v.Kind() {
	case reflect.String:
		*out = append(*out, v.String())
	case reflect.Struct:
		for i := range v.NumField() {
			collectStrings(v.Field(i), out)
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			collectStrings(v.Index(i), out)
		}
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			collectStrings(v.Elem(), out)
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			collectStrings(iter.Key(), out)
			collectStrings(iter.Value(), out)
		}
	}
}

func allStrings(v any) []string {
	var out []string
	collectStrings(reflect.ValueOf(v), &out)
	return out
}

// assertNoSecret 은 문자열 무리 어디에도 비밀 조각이 없음을 단언한다.
func assertNoSecret(t *testing.T, label string, values []string, secrets ...string) {
	t.Helper()
	for _, s := range values {
		for _, secret := range secrets {
			if secret == "" {
				continue
			}
			if strings.Contains(s, secret) {
				t.Errorf("%s 에 비밀 %q 가 들어 있다: %q", label, secret, s)
			}
		}
	}
}

// assertSerializable 은 결과가 실제로 JSON 으로 나갈 수 있고, 그 JSON 에도 비밀이 없음을 본다.
// Wails 바인딩이 이 경로를 그대로 탄다 (ADR 0004).
func assertSerializable(t *testing.T, v any, secrets ...string) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal 실패 — 화면이 결과를 통째로 못 받는다: %v", err)
	}
	assertNoSecret(t, "직렬화 결과", []string{string(b)}, secrets...)
	return string(b)
}
