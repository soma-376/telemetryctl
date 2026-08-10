package receiver

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"github.com/your-org/pulsemetry/internal/credential"
)

// 로컬 인증은 3중이다 (계획서 「수신기 설계」, ADR 0001 Negative).
//
//  1. bearer 토큰 — crypto/rand 32B, subtle.ConstantTimeCompare, 키링 보관
//  2. X-Pulsemetry-Local: 1 헤더 요구
//  3. OPTIONS 는 405, CORS 헤더 없음 (receiver.go ServeHTTP)
//
// 하나로 충분하지 않은 이유가 각각 다르다. 토큰만 두면 브라우저에서 온 요청도 토큰만
// 맞으면 통과하는데, 로컬 토큰은 벤더 설정 파일에 평문으로 들어가므로 (계획서 「리스크」)
// 같은 PC 에서 도는 코드가 읽을 수 있다. 커스텀 헤더 요구는 단순 요청(simple request)
// 으로는 만들 수 없어 preflight 를 강제하고, preflight 는 3 번에서 끊긴다.
// 반대로 헤더만 두면 아무 로컬 프로세스나 텔레메트리를 위조해 넣을 수 있다.

const (
	// LocalHeader 는 로컬 수신기임을 확인하는 헤더다. 값이 비밀은 아니다 —
	// 브라우저의 simple request 를 배제하는 것이 목적이다.
	LocalHeader = "X-Pulsemetry-Local"
	// LocalHeaderValue 는 LocalHeader 의 유일한 허용 값이다.
	LocalHeaderValue = "1"

	// TokenBytes 는 ingest 토큰의 엔트로피다 (계획서 「수신기 설계」의 32B).
	TokenBytes = 32

	bearerPrefix = "bearer "
)

// NewToken 은 crypto/rand 로 32 바이트를 뽑아 base64url(패딩 없음)로 만든다.
// 패딩 없는 base64url 을 쓰는 이유는 이 값이 HTTP 헤더와 벤더 설정 파일(JSON·TOML)에
// 그대로 들어가기 때문이다 — 인용이나 이스케이프가 필요한 문자가 없어야 한다.
func NewToken() (string, error) {
	buf := make([]byte, TokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("ingest 토큰 생성 실패: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// EnsureToken 은 키링의 로컬 ingest 토큰을 읽고, 없으면 새로 만들어 저장한 뒤 돌려준다.
//
// 매 기동마다 새 토큰을 만들지 않는 이유: 토큰이 바뀌면 벤더 설정 재병합이 필요하고,
// 데몬이 뜨기 전에 시작된 Claude Code 세션은 낡은 토큰으로 401 을 맞는다.
// 토큰 교체가 필요할 때는 ResetToken 을 명시적으로 부른다.
func EnsureToken() (string, error) {
	token, found, err := credential.Get(credential.AccountLocalIngest)
	if err != nil {
		return "", err
	}
	if found && token != "" {
		return token, nil
	}
	return ResetToken()
}

// ResetToken 은 새 토큰을 만들어 키링에 저장한다. 기존 토큰은 무효가 되므로
// 호출자는 벤더 설정을 재병합해야 한다.
func ResetToken() (string, error) {
	token, err := NewToken()
	if err != nil {
		return "", err
	}
	if err := credential.Set(credential.AccountLocalIngest, token); err != nil {
		return "", err
	}
	return token, nil
}

// ClearToken 은 키링에서 ingest 토큰을 지운다. local disable·uninstall 이 부른다.
func ClearToken() error {
	return credential.Delete(credential.AccountLocalIngest)
}

// 401 사유 문구. **데몬 로그에만** 쓰인다 — HTTP 응답은 언제나 불투명한 "unauthorized" 다
// (receiver.go serveIngest). 두 청중을 구분하는 것이 요점이다: 원격 호출자에게 어느
// 검사에서 걸렸는지 알려 주면 헤더를 하나씩 맞춰 보는 탐색을 돕지만, 이 기계의 주인에게는
// 그것이 진단의 전부다. "토큰 불일치" 는 낡은 토큰(→ local disable && local enable)을,
// "로컬 헤더 없음" 은 벤더 설정이 헤더를 안 적었음을 가리킨다.
const (
	reasonBadToken           = "토큰 불일치"
	reasonMissingLocalHeader = "로컬 헤더 없음"
)

// authorize 는 3중 인증 중 헤더 두 가지를 확인하고, 실패했다면 사유를 함께 돌려준다.
//
// 두 검사를 모두 수행한 뒤 결합한다. 헤더가 없다고 토큰 비교를 건너뛰면 응답 시간이
// "토큰 비교까지 갔는가" 를 알려 준다. 사유를 만들 때도 조기 반환하지 않는 이유가 같다.
// 토큰 비교는 subtle.ConstantTimeCompare 다 — bytes.Equal 은 첫 불일치에서 멈춰
// 앞자리부터 한 글자씩 맞춰 나가는 공격을 허용한다.
func (rc *Receiver) authorize(r *http.Request) (bool, string) {
	presented := bearerToken(r.Header.Get("Authorization"))
	tokenOK := subtle.ConstantTimeCompare([]byte(presented), []byte(rc.token)) == 1
	localOK := subtle.ConstantTimeCompare(
		[]byte(strings.TrimSpace(r.Header.Get(LocalHeader))), []byte(LocalHeaderValue)) == 1
	if tokenOK && localOK {
		return true, ""
	}

	// 사유 문자열에는 제시된 값을 담지 않는다. 틀린 토큰이라도 로그에 남으면 오타 하나
	// 차이의 진짜 토큰을 유추할 수 있다 (auth_test.go TestTokenNeverReachesLogs).
	reasons := make([]string, 0, 2)
	if !tokenOK {
		reasons = append(reasons, reasonBadToken)
	}
	if !localOK {
		reasons = append(reasons, reasonMissingLocalHeader)
	}
	return false, strings.Join(reasons, " + ")
}

// bearerToken 은 Authorization 헤더에서 토큰만 떼어 낸다. 스킴은 RFC 7235 대로
// 대소문자를 가리지 않는다. 형식이 아니면 빈 문자열이라 비교에서 자연히 실패한다.
func bearerToken(header string) string {
	h := strings.TrimSpace(header)
	if len(h) < len(bearerPrefix) || !strings.EqualFold(h[:len(bearerPrefix)], bearerPrefix) {
		return ""
	}
	return strings.TrimSpace(h[len(bearerPrefix):])
}
