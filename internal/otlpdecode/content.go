package otlpdecode

import (
	"strings"
	"unicode/utf8"

	"github.com/your-org/pulsemetry/internal/event"
)

// DefaultMaxContentBytes 는 원문 한 항목의 저장 상한이다 (계획서 리스크 표의 항목당 16KB 캡).
// 캡을 여기서 적용하는 이유는 store 까지 큰 문자열을 들고 가지 않기 위해서다 —
// 4 MiB 페이로드 하나에 100 KB 짜리 tool_result 가 수십 개 들어올 수 있다.
const DefaultMaxContentBytes = 16 << 10

// 원문 어휘(event.ContentKind·event.Content)는 event 패키지가 소유한다. store 가
// event_content.kind 에 쓸 값과 session 이 프롬프트를 알아보는 값이 같아야 하기 때문이다.
// 이 파일에는 그 어휘를 **디코드하는 방법**만 남는다 — 배열 인덱스, 캡, 이름 기반 판별.

const contentKindCount = 4

// contentOrdinal 은 carrier 의 고정 크기 배열 인덱스다. map 을 쓰지 않으므로 같은 페이로드를
// 두 번 디코드해도 Contents 의 순서가 같다.
func contentOrdinal(k event.ContentKind) int {
	switch k {
	case event.ContentResponse:
		return 1
	case event.ContentToolInput:
		return 2
	case event.ContentToolResult:
		return 3
	default:
		return 0
	}
}

var contentKindByOrdinal = [contentKindCount]event.ContentKind{
	event.ContentPrompt, event.ContentResponse, event.ContentToolInput, event.ContentToolResult,
}

// Content 는 event_content 한 행(event.Content)에 배치 안의 연결 고리를 붙인 것이다.
//
// events.id 는 store 가 INSERT 하며 정하므로 여기서는 알 수 없다. 대신 두 가지를 준다.
//   - EventIndex: 같은 Result.Events 안의 인덱스. 한 배치를 그대로 저장하는 경로에서 쓴다.
//   - DedupKey: 그 이벤트의 events.dedup_key. UPSERT 후 id 를 되찾아야 하는 경로에서 쓴다.
//
// 둘 다 **한 번의 디코드 결과 안에서만 뜻이 있는 값**이라 event 패키지로 올리지 않았다.
// 저장된 행에도, session 조립 입력에도 자리가 없다.
type Content struct {
	EventIndex int
	DedupKey   string

	event.Content
}

// capContent 는 본문을 max 바이트로 자른다. UTF-8 경계를 지켜 자르므로 잘린 문자열도
// 그대로 SQLite TEXT 에 넣을 수 있다. 잘렸으면 truncated=true 다.
func capContent(s string, max int) (string, bool) {
	if max <= 0 || len(s) <= max {
		return s, false
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], true
}

// bodyContentKind 는 로그 레코드의 body 를 원문으로 볼지 판단한다.
//
// 벤더가 원문을 속성이 아니라 body 에 실어 보내는 경우가 있어 이름으로 판별한다.
// 이름 접미로 맞추는 이유는 벤더 접두(claude_code.*/codex.*)가 다르기 때문이다.
// 같은 종류의 속성이 이미 있으면 속성이 이긴다 — 속성 쪽이 형식이 명확하다.
func bodyContentKind(name string) (event.ContentKind, bool) {
	switch {
	case strings.HasSuffix(name, "user_prompt"):
		return event.ContentPrompt, true
	case strings.HasSuffix(name, "assistant_response"):
		return event.ContentResponse, true
	case strings.HasSuffix(name, "tool_result"):
		return event.ContentToolResult, true
	}
	return "", false
}

// applyContentMeasures 는 원문에서 파생되는 수치를 채운다.
// 벤더가 준 값이 이미 있으면 덮어쓰지 않는다 — prompt_length 는 벤더가 문자 수로 세고
// 우리는 캡 이전 원문만 볼 수 있어 우리 쪽 계산이 항상 열등하다.
func applyContentMeasures(m *event.Measures, c *carrier) {
	for i := range c.content {
		if !c.content[i].set {
			continue
		}
		body := c.content[i].body
		switch contentKindByOrdinal[i] {
		case event.ContentPrompt:
			if !m.PromptLength.Valid() {
				m.PromptLength = event.Some(int64(utf8.RuneCountInString(body)))
			}
		case event.ContentResponse:
			if !m.ResponseLength.Valid() {
				m.ResponseLength = event.Some(int64(utf8.RuneCountInString(body)))
			}
		case event.ContentToolInput:
			if !m.ToolInputBytes.Valid() {
				m.ToolInputBytes = event.Some(int64(len(body)))
			}
		case event.ContentToolResult:
			if !m.ToolResultBytes.Valid() {
				m.ToolResultBytes = event.Some(int64(len(body)))
			}
		}
	}
}
