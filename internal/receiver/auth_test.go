package receiver

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/your-org/pulsemetry/internal/credential"
)

// 토큰 비교가 상수 시간이어야 한다는 것은 동작으로 단언하기 어렵다 (타이밍 측정은
// CI 에서 필연적으로 불안정하다). 대신 **구현이 subtle 을 쓰는 사실 자체를 고정**한다.
// bytes.Equal 이나 == 로 되돌리는 변경이 이 테스트를 깨뜨린다.
func TestTokenComparisonUsesConstantTime(t *testing.T) {
	src, err := os.ReadFile("auth.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	if !strings.Contains(body, `"crypto/subtle"`) {
		t.Error("auth.go 가 crypto/subtle 을 import 하지 않는다")
	}
	if !strings.Contains(body, "subtle.ConstantTimeCompare([]byte(presented), []byte(rc.token))") {
		t.Error("토큰 비교가 subtle.ConstantTimeCompare 가 아니다")
	}
	for _, banned := range []string{"presented == rc.token", "bytes.Equal([]byte(presented)"} {
		if strings.Contains(body, banned) {
			t.Errorf("토큰을 조기 종료 비교로 다루고 있다: %s", banned)
		}
	}
}

func TestNewTokenEntropyAndShape(t *testing.T) {
	seen := map[string]struct{}{}
	for range 32 {
		token, err := NewToken()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := base64.RawURLEncoding.DecodeString(token)
		if err != nil {
			t.Fatalf("토큰이 base64url 이 아니다 %q: %v", token, err)
		}
		if len(raw) != TokenBytes {
			t.Fatalf("엔트로피 = %d바이트, want %d", len(raw), TokenBytes)
		}
		// 설정 파일(JSON·TOML)과 HTTP 헤더에 그대로 들어가므로 이스케이프가 필요한
		// 문자가 있으면 안 된다.
		if strings.ContainsAny(token, `"'\ /+=`) {
			t.Fatalf("토큰에 인용·이스케이프가 필요한 문자가 있다: %q", token)
		}
		if _, dup := seen[token]; dup {
			t.Fatalf("토큰이 중복 생성됐다: %q", token)
		}
		seen[token] = struct{}{}
	}
}

// keyring.MockInit 은 인메모리 provider 라 headless CI 와 개발기 키링을 건드리지 않는다.
// 전역 provider 를 갈아끼우므로 이 파일의 키링 테스트는 병렬 실행하지 않는다.
func TestEnsureTokenPersistsAndReuses(t *testing.T) {
	keyring.MockInit()

	first, err := EnsureToken()
	if err != nil {
		t.Fatal(err)
	}
	if first == "" {
		t.Fatal("빈 토큰이 반환됐다")
	}

	// 두 번째 호출은 같은 값을 줘야 한다. 매 기동마다 토큰이 바뀌면 데몬보다 먼저
	// 시작된 Claude Code 세션이 401 을 맞는다.
	second, err := EnsureToken()
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatalf("EnsureToken 이 토큰을 갈아치웠다: %q → %q", first, second)
	}

	// 키링에 실제로 저장돼 있어야 한다.
	stored, found, err := credential.Get(credential.AccountLocalIngest)
	if err != nil || !found {
		t.Fatalf("키링 조회 = (found=%v, err=%v)", found, err)
	}
	if stored != first {
		t.Fatalf("키링 값 = %q, want %q", stored, first)
	}
}

func TestResetAndClearToken(t *testing.T) {
	keyring.MockInit()

	first, err := EnsureToken()
	if err != nil {
		t.Fatal(err)
	}
	second, err := ResetToken()
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatal("ResetToken 이 같은 토큰을 돌려줬다")
	}
	if err := ClearToken(); err != nil {
		t.Fatal(err)
	}
	if _, found, err := credential.Get(credential.AccountLocalIngest); err != nil || found {
		t.Fatalf("ClearToken 후 조회 = (found=%v, err=%v), want (false, nil)", found, err)
	}
	// 없는 항목을 다시 지워도 uninstall 이 실패하면 안 된다.
	if err := ClearToken(); err != nil {
		t.Fatal(err)
	}
}

func TestBearerToken(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{name: "표준", header: "Bearer abc", want: "abc"},
		{name: "소문자 스킴", header: "bearer abc", want: "abc"},
		{name: "대문자 스킴", header: "BEARER abc", want: "abc"},
		{name: "앞뒤 공백", header: "  Bearer   abc  ", want: "abc"},
		{name: "스킴 없음", header: "abc", want: ""},
		{name: "다른 스킴", header: "Basic abc", want: ""},
		{name: "빈 헤더", header: "", want: ""},
		{name: "스킴만", header: "Bearer ", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := bearerToken(tt.header); got != tt.want {
				t.Errorf("bearerToken(%q) = %q, want %q", tt.header, got, tt.want)
			}
		})
	}
}

// TestAuthorizeReportsReason 은 401 의 사유가 두 검사를 각각 반영하는지 본다.
//
// 사유는 **데몬 로그 전용**이다. HTTP 응답은 여전히 어느 검사에서 걸렸는지 말하지 않는다
// (그 불변식은 TestUnauthorizedResponseStaysOpaque 가 지킨다). 로그의 청중은 이 기계의
// 주인이고, "토큰이 낡았다" 와 "헤더가 아예 없다" 는 고치는 방법이 다르다.
func TestAuthorizeReportsReason(t *testing.T) {
	rc, _, _ := newTestReceiver(t, nil)

	tests := []struct {
		name       string
		auth       string
		localValue string
		setLocal   bool
		wantOK     bool
		wantReason string
	}{
		{name: "둘 다 정상", auth: "Bearer " + testToken, localValue: LocalHeaderValue,
			setLocal: true, wantOK: true, wantReason: ""},
		{name: "헤더만 없음", auth: "Bearer " + testToken,
			wantOK: false, wantReason: reasonMissingLocalHeader},
		{name: "토큰만 틀림", auth: "Bearer nope", localValue: LocalHeaderValue,
			setLocal: true, wantOK: false, wantReason: reasonBadToken},
		{name: "헤더 값이 다름", auth: "Bearer " + testToken, localValue: "0",
			setLocal: true, wantOK: false, wantReason: reasonMissingLocalHeader},
		{name: "둘 다 틀림", auth: "",
			wantOK: false, wantReason: reasonBadToken + " + " + reasonMissingLocalHeader},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/logs", nil)
			if tt.auth != "" {
				req.Header.Set("Authorization", tt.auth)
			}
			if tt.setLocal {
				req.Header.Set(LocalHeader, tt.localValue)
			}
			ok, reason := rc.authorize(req)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if reason != tt.wantReason {
				t.Errorf("reason = %q, want %q", reason, tt.wantReason)
			}
		})
	}
}

// TestUnauthorizedResponseStaysOpaque 는 사유가 응답 본문·헤더로 새지 않음을 고정한다.
// 사유를 알려 주면 헤더를 하나씩 맞춰 보는 탐색을 도와주게 된다.
func TestUnauthorizedResponseStaysOpaque(t *testing.T) {
	rc, _, _ := newTestReceiver(t, nil)

	req := httptest.NewRequest(http.MethodPost, "/v1/logs", nil)
	req.Header.Set("Authorization", "Bearer "+testToken) // 로컬 헤더만 빠뜨린다
	rec := do(rc, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "unauthorized" {
		t.Errorf("body = %q, want unauthorized — 사유가 응답으로 샜다", got)
	}
	for _, banned := range []string{reasonBadToken, reasonMissingLocalHeader, LocalHeader} {
		if strings.Contains(rec.Body.String(), banned) {
			t.Errorf("응답에 %q 가 있다: %q", banned, rec.Body.String())
		}
	}
}

// TestUnauthorizedLogsAreThrottled 는 401 이 로그에 남되 폭주하지 않는지 본다.
//
// 잘못 설정된 벤더는 metrics 60초·logs 5초 주기로 계속 401 을 만든다. 매 건 찍으면
// 로그가 그것만으로 채워지고, 안 찍으면 이번 사건처럼 아무도 모른 채 텔레메트리가
// 사라진다. 첫 건은 반드시 남기고 이후는 분당 1회로 접되, 접힌 수를 함께 보고한다.
func TestUnauthorizedLogsAreThrottled(t *testing.T) {
	now := time.Date(2026, 8, 11, 3, 0, 0, 0, time.UTC)
	rc, _, logs := newTestReceiver(t, func(o *Options) {
		o.Now = func() time.Time { return now }
	})

	send := func() {
		req := httptest.NewRequest(http.MethodPost, "/v1/logs", nil)
		req.Header.Set("Authorization", "Bearer "+testToken)
		if rec := do(rc, req); rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	}

	for range 3 {
		send()
	}
	if got := strings.Count(logs.String(), "인증 실패"); got != 1 {
		t.Fatalf("로그 %d줄, want 1 (첫 건만) — 전체:\n%s", got, logs.String())
	}
	if !strings.Contains(logs.String(), reasonMissingLocalHeader) {
		t.Errorf("첫 로그에 사유가 없다:\n%s", logs.String())
	}

	// 1분이 지나면 다시 한 줄 남기고, 그 사이 접힌 2건을 보고해야 한다.
	now = now.Add(time.Minute)
	send()

	out := logs.String()
	if got := strings.Count(out, "인증 실패"); got != 2 {
		t.Fatalf("로그 %d줄, want 2 — 전체:\n%s", got, out)
	}
	if !strings.Contains(out, "2건 생략") {
		t.Errorf("접힌 건수를 보고하지 않는다:\n%s", out)
	}
	if got := rc.Stats().Unauthorized; got != 4 {
		t.Errorf("Unauthorized = %d, want 4 — 로그를 접어도 카운터는 전부 세야 한다", got)
	}
}

// 토큰이 로그에 새면 안 된다. 잘못된 토큰도 마찬가지다 — 오타 하나 차이의 값이
// 로그에 남으면 진짜 토큰을 유추할 수 있다.
func TestTokenNeverReachesLogs(t *testing.T) {
	rc, _, logs := newTestReceiver(t, nil)

	const wrong = "wrong-token-should-not-be-logged"
	req := authedRequest(http.MethodPost, "/v1/logs", "application/json", minimalLogsJSON())
	req.Header.Set("Authorization", "Bearer "+wrong)
	if rec := do(rc, req); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}

	// 로컬 헤더가 빠진 경로도 같이 태운다. 사유를 찍기 시작한 이상 그 경로에서도
	// 토큰이 새지 않아야 한다.
	noHeader := httptest.NewRequest(http.MethodPost, "/v1/logs", nil)
	noHeader.Header.Set("Authorization", "Bearer "+wrong)
	if rec := do(rc, noHeader); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}

	// 정상 요청도 한 번 흘려 보낸다 (성공 경로에서 헤더를 찍는 실수를 잡는다).
	if rec := do(rc, authedRequest(http.MethodPost, "/v1/logs", "application/json", minimalLogsJSON())); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if err := rc.Close(); err != nil {
		t.Fatal(err)
	}

	out := logs.String()
	for _, secret := range []string{testToken, wrong} {
		if strings.Contains(out, secret) {
			t.Fatalf("로그에 토큰이 남았다: %q", out)
		}
	}
}
