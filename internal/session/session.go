// Package session 은 정규화 이벤트를 세션으로 조립한다 (ADR 0005).
//
// 화면의 어휘가 "세션 = 하나의 작업"이라 sessions 가 스키마의 중심이고
// session_files · tool_events · mcp_session_usage 가 여기 매달린다. 그런데 OTel 로
// 들어오는 것은 개별 로그·메트릭이고 세션 경계를 알려주는 시그널이 없다 — 이 패키지가
// session.id 로 묶고 유휴 시간으로 마감해 그 경계를 추론한다.
//
// 표준 라이브러리와 internal/event 만 import 하고 파일·네트워크·DB·시계에 접근하지 않는다.
// "지금 시각"이 필요한 유휴 마감은 Advance(now) 로 시각을 주입받는다. 시계를 직접 읽으면
// 10분 경계 테스트가 벽시계에 의존해 재현 불가능해진다.
//
// 단위: Event.TS 는 unix 나노초, 이 패키지가 내보내는 시각은 전부 unix 초다.
// 변환은 UnixNano.Sec() 하나만 쓴다.
package session

import "github.com/your-org/pulsemetry/internal/event"

// Status 는 sessions.status 값이다.
type Status string

const (
	// StatusRunning 은 유휴 임계값 안에 이벤트가 있는 세션이다. 상단 바의 "3 agents active".
	StatusRunning Status = "running"
	// StatusCompleted 는 유휴 임계값을 넘겨 마감된 세션이다.
	StatusCompleted Status = "completed"
	// StatusAbandoned 는 마지막 툴 이벤트가 실패인 채로 마감된 세션이다.
	// 휴리스틱이라 오판할 수 있어 화면 필터에만 쓰고 지표로 쓰지 않는다 (ADR 0005).
	StatusAbandoned Status = "abandoned"
	// StatusHandoff 는 같은 프로젝트에서 다른 벤더 세션이 뒤이어 시작된 세션이다.
	StatusHandoff Status = "handoff"
)

// TitleSource 는 sessions.title_source 값이다. 출처를 남겨야 후속 티켓이 제목 생성기를
// 교체할 때 어느 행을 다시 만들지 고를 수 있다 (후속: llm).
type TitleSource string

const (
	TitleFromPrompt   TitleSource = "prompt_head"
	TitleFromFiles    TitleSource = "files"
	TitleFromFallback TitleSource = "fallback"
)

// Action 은 tool_events.action 값이다. 툴 이름에서 파생한다.
type Action string

const (
	ActionRead   Action = "read"
	ActionEdit   Action = "edit"
	ActionWrite  Action = "write"
	ActionRun    Action = "run"
	ActionSearch Action = "search"
)

// ContentKind 는 event_content.kind 다. 조립기는 prompt 만 소비하고 나머지는 무시한다.
type ContentKind string

const (
	ContentPrompt     ContentKind = "prompt"
	ContentResponse   ContentKind = "response"
	ContentToolInput  ContentKind = "tool_input"
	ContentToolResult ContentKind = "tool_result"
)

// Input 은 조립기 입력 한 건이다. Event 만으로는 세션을 만들 수 없어서 두 가지를 더 받는다.
//
//   - Body: events 스키마에는 길이만 있고 본문은 event_content 로 따로 간다. 제목·요약
//     휴리스틱은 본문이 있어야 하므로 디코더가 여기에 실어 준다. 조립기는 첫 프롬프트에서
//     제목·요약 문자열만 뽑고 본문은 즉시 버린다 — 원문을 들고 있는 것은 store 의 몫이다.
//   - Target: tool_result 의 tool_input 에서 얻은 파일 경로(또는 명령 첫 토큰)다.
//     Attributes 에 파일 자리가 없어 events 로는 전달되지 않는다.
//
// Target 은 **이미 NormalizePath 를 거친 값**만 받는다. 전체 경로 문자열이 이 패키지를
// 지나갈 일 자체를 없애 ADR 0003 위반이 구조적으로 불가능해진다.
//
// 게이트(OTEL_LOG_USER_PROMPTS·OTEL_LOG_TOOL_DETAILS)가 꺼져 있으면 Body 와 Target 이
// 비어서 들어온다. 그 경우 제목은 files·fallback 으로 떨어지고 파일 변경 목록은 비어 있다.
type Input struct {
	Event  event.Event
	Kind   ContentKind
	Body   string
	Target event.Path
}

// Session 은 sessions 한 행과 거기 매달린 종속 테이블 내용을 합친 조립 결과다.
//
// phase_json 과 work_type 은 후속 티켓 소관이라 필드를 두지 않는다. store 는 두 컬럼을
// NULL 로 남긴다 — 여기에 필드가 없으면 실수로 채울 방법도 없다.
type Session struct {
	SessionID   string
	Vendor      string
	StartedAt   event.UnixSec
	LastEventAt event.UnixSec
	EndedAt     event.Opt[event.UnixSec] // 미설정 = 진행 중 (ended_at IS NULL)
	Status      Status

	Title       string
	TitleSource TitleSource
	Summary     string
	ProjectHash string
	ProjectName string

	DurationMS    int64
	ActiveSeconds float64

	InputTokens         int64
	OutputTokens        int64
	CacheReadTokens     int64
	CacheCreationTokens int64
	CostUSD             float64

	ToolCalls   int64
	ToolErrors  int64
	ToolRejects int64

	APIRequests int64
	APIErrors   int64
	Retries     int64

	Prompts   int64
	Responses int64

	LinesAdded   int64
	LinesRemoved int64

	Files []File      // 변경량 내림차순
	Tools []ToolEvent // 도착 순서
	MCP   []MCPUsage  // 서버 이름 오름차순

	Diag Diagnostics
}

// File 은 session_files 한 행이다.
type File struct {
	PathHash string // file_path_hash
	Name     string // basename 만
	Ext      string
	// 세션 합계와 달리 파일별 라인 수는 근사다. lines_of_code 메트릭에 파일명이 없어
	// 같은 구간의 편집 대상에 나눠 귀속시킨다 (lines.go).
	LinesAdded   int64
	LinesRemoved int64
	Edits        int64
	LastTS       event.UnixSec
}

// ToolEvent 는 tool_events 한 행이다. id 는 store 가 매긴다.
type ToolEvent struct {
	TS         event.UnixSec
	ToolName   string
	Action     Action
	TargetName string
	TargetHash string
	Success    event.Opt[bool] // 미설정 = 성공 여부 미상. 실패와 다르다
	DurationMS event.Opt[int64]
	ErrorType  string
	Decision   string // accept | reject
	MCPServer  string
}

// MCPUsage 는 mcp_session_usage 한 행이다.
type MCPUsage struct {
	ServerName      string
	Connected       bool
	ConnectFailures int64
	ToolCalls       int64
	Tokens          int64
}

// Diagnostics 는 스키마 컬럼이 아니다. 데몬이 로그로 남길 값만 담는다.
//
// ADR 0005 가 abandoned 판정 근거를 남기라고 요구했고(오판 가능한 휴리스틱이라
// 사후에 근거 없이는 검증할 수 없다), 라인 배분이 근사임을 화면 툴팁에 표기하려면
// 어디에도 귀속되지 못한 잔량을 알아야 한다.
type Diagnostics struct {
	// StatusReason 은 completed·abandoned·handoff 판정의 근거 문장이다. running 이면 빈 값.
	StatusReason string
	// Reopens 는 마감된 뒤 같은 session.id 로 이벤트가 다시 들어온 횟수다.
	Reopens int
	// UnattributedLines* 는 세션 합계에는 들어갔지만 어느 파일에도 귀속되지 못한 라인 수다.
	// 합계 - 파일별 합 = 이 값이 항상 성립한다 (lines.go 의 불변식).
	UnattributedLinesAdded   int64
	UnattributedLinesRemoved int64
	// DiscardedPoints 는 aggregation_temporality 가 UNSPECIFIED 라 폐기한 데이터포인트 수다.
	// 조용히 2배 집계하느니 버리고 센다 (계획서 「반드시 알아야 할 제약」 4).
	DiscardedPoints int64
}
