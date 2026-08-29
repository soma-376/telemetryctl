package pricing

import "strings"

// 모델 이름 정규화.
//
// # 왜 정규화가 필요한가
//
// 같은 모델이 벤더·경유지마다 다른 이름으로 온다. Claude Code 는 별칭
// (claude-opus-5)이나 날짜 붙은 아이디(claude-haiku-4-5-20251001)를 보내고, Bedrock 을
// 경유하면 리전·벤더 접두와 버전 꼬리가 붙으며(us.anthropic.claude-sonnet-4-5-20250929-v1:0),
// Vertex 는 @ 로 스냅샷을 표시한다(claude-opus-4-5@20251101). 이것들을 각각 표에 적으면
// 단가 하나를 고칠 때 여섯 줄을 고쳐야 하고, 하나를 빠뜨리면 화면의 같은 모델이 두 가격을 낸다.
//
// # 정규화가 하지 않는 것
//
// **모르는 모델을 아는 모델로 옮기지 않는다.** 여기서 하는 일은 "같은 모델의 다른 표기"를
// 한 표기로 모으는 것뿐이고, 그렇게 모은 이름이 표에 없으면 결과는 unavailable 이다.
// claude-opus-9 를 opus 계열 가격에 붙이는 식의 추측은 하지 않는다 — 틀린 비용은
// 비용을 모르는 것보다 나쁘다. 되짚을 근거가 남지 않기 때문이다.

// Model 은 한 호출의 모델 이름을 정규화한 결과다.
//
// Known 은 정규화 결과가 **가격표에 있는지**다. 정규화 자체는 표를 모르므로 Table 이 채운다.
type Model struct {
	// Raw 는 llm_calls.model 에 저장된 원래 값이다. 화면이 사용자에게 보여줄 이름이다.
	Raw string `json:"raw"`
	// Canonical 은 정규화한 이름이다. 비어 있으면 모델 이름 자체가 없었다는 뜻이다.
	Canonical string `json:"canonical"`
	// Known 은 이 모델의 단가가 표에 있는지다.
	Known bool `json:"known"`
}

// knownPrefixes 는 경유지가 붙이는 접두다. 리전(us·eu·apac·global)과 제공자 이름이다.
// 표기 그대로의 모델 이름에는 이 접두가 없다.
var knownPrefixes = map[string]bool{
	"us":        true,
	"eu":        true,
	"apac":      true,
	"global":    true,
	"anthropic": true,
	"openai":    true,
}

// Canonical 은 모델 이름을 표 조회용 한 가지 표기로 옮긴다. 표를 참조하지 않는 순수 함수라
// 표가 바뀌어도 정규화 규칙은 그대로다.
//
// 규칙은 순서가 있다. Bedrock 이름은 버전 꼬리(-v1)가 날짜 뒤에 붙으므로 버전을 먼저 뗀다.
func Canonical(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return ""
	}
	// publishers/anthropic/models/claude-... 같은 경로형 이름은 마지막 조각만 쓴다.
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		s = s[i+1:]
	}
	// Vertex 의 @스냅샷, Bedrock 의 :inference-profile 꼬리를 뗀다.
	if i := strings.IndexByte(s, '@'); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, ':'); i >= 0 {
		s = s[:i]
	}
	s = stripKnownPrefixes(s)
	s = strings.TrimSuffix(s, "-latest")
	s = stripVersionSuffix(s)
	s = stripDateSuffix(s)
	return strings.Trim(s, "-")
}

// stripKnownPrefixes 는 점으로 구분된 리전·제공자 접두를 뗀다.
//
// 표에 없는 접두를 만나면 멈춘다 — gpt-4.1 의 "gpt-4" 처럼 이름 자체에 점이 있는 경우를
// 잘라내지 않기 위해서다.
func stripKnownPrefixes(s string) string {
	for {
		i := strings.IndexByte(s, '.')
		if i < 0 || !knownPrefixes[s[:i]] {
			return s
		}
		s = s[i+1:]
	}
}

// stripVersionSuffix 는 Bedrock 이 붙이는 -v1 · -v2 꼬리를 뗀다.
func stripVersionSuffix(s string) string {
	i := strings.LastIndexByte(s, '-')
	if i < 0 {
		return s
	}
	last := s[i+1:]
	if len(last) < 2 || last[0] != 'v' || !allDigits(last[1:]) {
		return s
	}
	return s[:i]
}

// stripDateSuffix 는 스냅샷 날짜 꼬리를 뗀다. -20250929 와 -2025-09-29 두 표기를 다룬다.
//
// 8자리·4-2-2 자리 숫자만 날짜로 본다. claude-opus-4-5 의 "5" 처럼 짧은 숫자 조각은
// 모델 세대이지 날짜가 아니다.
func stripDateSuffix(s string) string {
	if head, last, ok := cutLastSegment(s); ok && len(last) == 8 && allDigits(last) {
		return head
	}
	rest, day, ok := cutLastSegment(s)
	if !ok || len(day) != 2 || !allDigits(day) {
		return s
	}
	rest, month, ok := cutLastSegment(rest)
	if !ok || len(month) != 2 || !allDigits(month) {
		return s
	}
	head, year, ok := cutLastSegment(rest)
	if !ok || len(year) != 4 || !allDigits(year) {
		return s
	}
	return head
}

func cutLastSegment(s string) (head, last string, ok bool) {
	i := strings.LastIndexByte(s, '-')
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+1:], true
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
