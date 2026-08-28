package vendorlimit

import (
	"errors"
	"strings"
)

// redacted 는 토큰이 있어야 할 자리에 대신 나가는 값이다.
//
// 꺾쇠(<redacted>) 대신 대괄호를 쓰는 이유는 json.Marshal 이 <·> 를 \u003c·\u003e 로
// HTML 이스케이프하기 때문이다. 마스킹이 되긴 하지만 사람이 눈으로 확인하기 어려워지고,
// "마스킹됐는지" 를 검사하는 테스트도 인코딩을 알아야 한다.
const redacted = "[redacted]"

// Token 은 벤더 액세스 토큰이다. **값이 밖으로 새지 않는 것이 이 타입의 존재 이유다.**
//
// # 왜 string 이 아닌가
//
// 토큰을 string 으로 들고 다니면 유출은 규율의 문제가 된다 — 누군가 한 번 %v 로 찍거나
// 구조체에 담아 json.Marshal 하면 끝이다. 그리고 그런 실수는 성공 경로가 아니라 오류
// 경로에서 일어나서, 정상 동작하는 코드를 아무리 읽어도 보이지 않는다.
//
// 그래서 값을 비공개 필드에 가두고 String·GoString·MarshalJSON 을 전부 막았다.
// 실수로 %v · %s · %#v · json.Marshal 어디에 걸려도 나가는 것은 "<redacted>" 뿐이다.
// 원값을 꺼내는 reveal 은 비공개 메서드라 이 패키지 밖에서는 아예 호출할 수 없다.
//
// 값 리시버로 구현한 이유: Token 과 *Token 이 모두 fmt.Stringer·json.Marshaler 를
// 만족해야 한다. 포인터 리시버면 값으로 담긴 Token 이 그대로 필드째 찍힌다.
type Token struct {
	v string
}

// newToken 은 원값으로 Token 을 만든다. 이 패키지 안에서만 부른다.
func newToken(v string) Token { return Token{v: strings.TrimSpace(v)} }

// reveal 은 원값을 돌려준다. Authorization 헤더를 조립하는 단 한 곳에서만 쓴다.
func (t Token) reveal() string { return t.v }

// empty 는 값이 비었는지 본다.
func (t Token) empty() bool { return t.v == "" }

func (Token) String() string { return redacted }

func (Token) GoString() string { return redacted }

// MarshalJSON 은 Token 이 어떤 구조체에 담겨 직렬화되더라도 원값이 나가지 않게 한다.
func (Token) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }

// stripSecret 은 오류 메시지에서 비밀 문자열을 지운다.
//
// 우리가 만드는 오류에는 토큰을 넣지 않지만, 남의 오류를 감쌀 때는 안에 무엇이 들어 있는지
// 보장할 수 없다 — URL 에 토큰이 섞인 요청의 *url.Error, 요청을 되비추는 상위 응답 본문이
// 그렇다. 마지막 그물이라 비용이 싸고(대개 Contains 가 false 로 끝난다) 확실하다.
// internal/forward 의 같은 이름 함수와 같은 역할이며, 패키지 경계를 넘겨 공유하기보다
// 각자 두는 편이 의존을 만들지 않아 낫다.
func stripSecret(err error, secret string) error {
	if err == nil || secret == "" {
		return err
	}
	msg := err.Error()
	if !strings.Contains(msg, secret) {
		return err
	}
	return errors.New(strings.ReplaceAll(msg, secret, redacted))
}
