package event

// ContentKind 는 event_content.kind 값이다. 계획서 스키마가 네 가지로 고정했다.
//
// 이 어휘가 event 패키지에 있는 이유는 파이프라인의 세 지점이 같은 값을 봐야 하기 때문이다 —
// otlpdecode 가 원문을 뽑을 때, session 이 첫 프롬프트에서 제목을 만들 때, store 가
// event_content.kind 컬럼에 쓸 때. 어휘가 패키지마다 따로 있으면 문자열은 우연히 같아도
// 타입이 달라 컴파일러가 어긋남을 잡아 주지 못한다.
//
// 값 문자열 자체가 스키마 계약이다. 바꾸면 event_content 의 기존 행과 어긋난다.
type ContentKind string

const (
	ContentPrompt     ContentKind = "prompt"
	ContentResponse   ContentKind = "response"
	ContentToolInput  ContentKind = "tool_input"
	ContentToolResult ContentKind = "tool_result"
)

// Content 는 event_content 한 행이다. Event 에는 원문 필드가 없다 —
// events 에는 길이·바이트 수만 남고 본문은 이 타입으로만 흐른다 (ADR 0003).
//
// event_content.event_id 는 store 가 events 를 INSERT 하며 정하므로 여기에 없다.
// 제로값(Kind == "")은 "원문 없음"이다. 게이트(OTEL_LOG_USER_PROMPTS·
// OTEL_LOG_TOOL_DETAILS)가 꺼져 있으면 대부분의 이벤트가 그 상태로 들어온다.
//
// 경로 정책이 여기서 갈린다. Attributes 에는 전체 경로가 들어갈 자리가 없지만 Body 에는
// tool_input 원문이 그대로 남는다 — 세션 조립기가 파일별 변경을 만들려면 그 경로가
// 필요하고, ADR 0003 이 16KB 캡·30일 보존·상위 미전달 조건으로 로컬에만 허용했다.
type Content struct {
	Kind      ContentKind
	Body      string
	Truncated bool
}
