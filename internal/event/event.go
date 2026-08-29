// Package event 는 OTLP 시그널을 정규화한 이벤트 타입과 중복 제거 키를 정의한다.
// 파이프라인 전체의 공용 어휘다 — otlpdecode 가 생산하고 session·rollup·store 가 소비한다.
//
// 표준 라이브러리만 import 하고 IO 를 하지 않는다. 여기 붙는 의존성은 뒤의 네 단계로 전부 번진다.
//
// 속성은 allowlist 다. catch-all map 을 두지 않으므로 여기 필드가 없는 속성은 디코더가 버린다.
// organization.id 와 모든 토큰은 이 패키지의 어떤 타입에도 담을 자리가 없다 (ADR 0003).
//
// 작업 경로·user.email·user.id·user.account_uuid 는 v3 스키마가 요구하는 컬럼이 있어
// **로컬 저장 전용 필드**로 받는다 (ADR 0010). 해시 필드(ProjectHash·ProjectName)와
// 원경로 필드(WorkspacePath)가 나란히 존재하며 용도가 다르다 — 각 필드의 주석을 따른다.
// 상위 전달 스크럽은 internal/forward 가 원본 바이트에 대해 따로 수행하므로 이 allowlist 를
// 넓혀도 전달 규칙은 달라지지 않는다.
package event

import "fmt"

// Signal 은 events.signal 값이다. 계획서 스키마가 metric | log 두 가지로 고정했다 —
// /v1/traces 로 들어온 페이로드는 상위로 전달만 하고 events 로 정규화하지 않는다.
type Signal string

const (
	SignalMetric Signal = "metric"
	SignalLog    Signal = "log"
)

func (s Signal) Valid() bool { return s == SignalMetric || s == SignalLog }

// Temporality 는 Sum.aggregation_temporality 를 그대로 옮긴 어휘다.
// 값 해석(delta 는 합산, cumulative 는 직전값과의 차, Unspecified 는 폐기)은 rollup 이 한다.
// 벤더 기본값을 가정하지 않는다 — 사용자가 OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE 를
// 덮어쓸 수 있어 cumulative 를 delta 로 오인하면 비용이 조용히 배로 잡힌다.
type Temporality uint8

const (
	TemporalityUnspecified Temporality = iota
	TemporalityDelta
	TemporalityCumulative
)

func (t Temporality) String() string {
	switch t {
	case TemporalityDelta:
		return "delta"
	case TemporalityCumulative:
		return "cumulative"
	default:
		return "unspecified"
	}
}

// Event 는 events 테이블 한 행에 대응하는 정규화 이벤트다.
// id 는 store 가, dedup_key 와 hour 는 DedupKey()·Hour() 가 만드는 파생값이라 필드로 두지 않는다.
type Event struct {
	// NOT NULL 컬럼
	Vendor         string   // claude_code | codex | gemini_cli | ... (스키마 제약 없음)
	InstallationID string   // 이 설치의 식별자. 한 DB 안에서는 사실상 상수다
	Signal         Signal   //
	Name           string   // claude_code.token.usage, claude_code.user_prompt 등
	TS             UnixNano //

	SessionID string // OTel session.id. 리소스·로그 속성에 없을 수 있다

	// TurnKey 는 벤더가 직접 준 턴 식별자다 (Claude Code prompt.id, Codex turn.id).
	// turns.turn_key 의 원천이고, 비어 있으면 session 패키지가 턴 경계를 추론한다.
	TurnKey string
	// CallKey 는 벤더가 직접 준 도구 호출 식별자다 (Claude Code tool_use_id, Codex call_id).
	// 결정 이벤트와 결과 이벤트를 tool_calls 한 행으로 합치는 근거다. 비어 있으면
	// session 패키지가 턴 안의 순번으로 합성한다 — store 는 절대 추측하지 않는다.
	CallKey string

	EventID string // 로그 레코드의 event.id. 메트릭에는 보통 없다
	TraceID string
	SpanID  string

	// Sequence 는 한 페이로드 안에서 (Name, TS, Attr) 까지 같은 데이터포인트를 구분하는 순번이다.
	// events 에 컬럼이 없고 DedupKey 입력으로만 쓴다 (§5.5 의 sequence).
	Sequence int

	Temporality Temporality // metric 시그널에만 의미가 있다

	// StartTS 는 OTLP NumberDataPoint.start_time_unix_nano 다 — 이 누적 계열이 값을 쌓기
	// 시작한 시각(UTC unix **나노초**). TS 와 같은 단위라 같은 타입으로 둔다.
	// 0 이하는 "모름" 이다(벤더 미전송·이상값). Validate 가 검사하지 않는 것도 그래서다 —
	// 저장되지 않는 메타데이터 하나 때문에 실제 수치 데이터포인트를 버릴 이유가 없다.
	//
	// Sequence 와 마찬가지로 events 테이블에 대응 컬럼이 없다. 저장하지 않고 파이프라인
	// 안에서만 쓰는 값이다 — rollup 이 cumulative 리셋과 콜드 스타트를 판정하는 데 쓴다.
	// 수집 구간이 새로 시작됐는지는 값의 증감이 아니라 이 값으로만 확실히 알 수 있다.
	//
	// DedupKey 에는 넣지 않는다. 같은 포인트가 재전송되면 이 값도 같으므로 변별력이 없고,
	// 상위가 배치를 다시 자르며 이 필드를 채우거나 비우면 같은 이벤트가 두 행이 된다.
	// Measures·Temporality 를 키에서 뺀 것과 같은 이유다 (dedup.go).
	StartTS UnixNano
	Attr    Attributes
	Measure Measures
}

// Attributes 는 events 테이블의 속성 allowlist 컬럼이다. 필드를 늘리려면 스키마를 같이 늘린다.
// 빈 문자열이 부재를 뜻하고 store 가 NULL 로 쓴다 — 문자열에는 "없음"과 "0"을 구분할 문제가 없다.
type Attributes struct {
	Model          string
	Type           string
	ToolName       string
	Decision       string
	DecisionSource string
	Language       string
	QuerySource    string
	Speed          string
	Effort         string
	AgentName      string
	SkillName      string
	PluginName     string
	MCPServer      string
	MCPTool        string
	StartType      string
	TerminalType   string
	AppVersion     string
	Entrypoint     string
	Environment    string

	// ProjectHash·ProjectName 은 NormalizePath 의 결과다. **상위 전달과 관련된 코드는
	// 이 두 필드만 쓴다** — 전체 경로를 되돌릴 수 없는 형태이기 때문이다 (ADR 0003).
	ProjectHash string
	ProjectName string // basename 만

	// ── 로컬 저장 전용 (ADR 0010) ───────────────────────────────────────────
	// 아래 세 필드는 v3 스키마의 sessions.workspace_path · user_email · user_account_id
	// 를 채우기 위한 값이다. **로컬 SQLite 에만 저장하고 상위로 전달하지 않는다.**
	// 상위 전달 페이로드의 스크럽은 internal/forward 가 원본 바이트에 대해 따로 수행하므로
	// 이 필드를 늘려도 전달 규칙은 달라지지 않는다 (ADR 0010 "근거" 절).

	// WorkspacePath 는 정규화하지 않은 작업공간 원경로다. 해시가 필요하면 ProjectHash 를 쓴다.
	WorkspacePath string
	// UserEmail 은 관측된 사용자 이메일이다 (user.email).
	UserEmail string
	// UserAccountID 는 벤더 사용자 계정 ID 다 (user.id · user.account_uuid).
	UserAccountID string
}

// WithProject 는 정규화된 경로를 project_hash·project_name 에 채운 사본을 돌려준다.
// 원본을 고치지 않으므로 디코더가 같은 Attributes 를 여러 이벤트에 나눠 써도 안전하다.
func (a Attributes) WithProject(p Path) Attributes {
	a.ProjectHash = p.Hash
	a.ProjectName = p.Name
	return a
}

// dedupFields 는 DedupKey 가 소비할 속성 값을 구조체 선언 순서 그대로 돌려준다.
// map 을 거치지 않으므로 순회 순서가 결정론적이다 — 이 슬라이스가 곧 키의 일부다.
func (a Attributes) dedupFields() []string {
	return []string{
		a.Model, a.Type, a.ToolName, a.Decision, a.DecisionSource,
		a.Language, a.QuerySource, a.Speed, a.Effort,
		a.AgentName, a.SkillName, a.PluginName,
		a.MCPServer, a.MCPTool,
		a.StartType, a.TerminalType, a.AppVersion, a.Entrypoint, a.Environment,
		a.ProjectHash, a.ProjectName,
		a.WorkspacePath, a.UserEmail, a.UserAccountID,
	}
}

// Measures 는 events 테이블의 수치 컬럼이다.
//
// SQLite 에서 NULL 을 허용하는 수치 컬럼은 전부 Opt 로 받아 "값이 0" 과 "값이 오지 않음" 을
// 구분한다. 비용 0 인 캐시 히트와 비용 개념이 없는 이벤트는 다르고, 실패한 툴 호출(success=false)과
// 성공 여부를 알 수 없는 이벤트도 다르다. 이 구분을 잃으면 rollup 의 분모가 조용히 부풀고
// session 의 tool_errors 가 과대 집계된다.
//
// 문자열 컬럼은 빈 문자열이 곧 NULL 이라 Opt 를 쓰지 않는다.
type Measures struct {
	Value Opt[float64] // 메트릭 데이터포인트 값. 로그 이벤트에는 없다
	Unit  string

	CostUSD             Opt[float64]
	InputTokens         Opt[int64]
	OutputTokens        Opt[int64]
	CacheReadTokens     Opt[int64]
	CacheCreationTokens Opt[int64]

	DurationMS Opt[int64]
	StatusCode Opt[int64]
	Attempt    Opt[int64]
	Success    Opt[bool]
	ErrorType  string
	// ErrorMessage 는 벤더가 준 오류 문자열 원문이다. ErrorType 과 달리 정제하지 않으므로
	// 경로·명령이 섞여 들어올 수 있다 — tool_calls.error_message 로만 가는 **로컬 저장 전용**
	// 값이다 (ADR 0010). ErrorType 의 정제 규칙(sanitizeErrorType)은 이 필드와 무관하다.
	ErrorMessage string

	// 길이·바이트 수만 담는다. 본문은 event_content 로 따로 가고 이 타입을 지나가지 않는다.
	PromptLength    Opt[int64]
	ResponseLength  Opt[int64]
	ToolInputBytes  Opt[int64]
	ToolResultBytes Opt[int64]
}

// Hour 는 events.hour 값이다. HourOf 와 같은 결과를 준다.
func (e Event) Hour() Hour { return HourOf(e.TS) }

// Validate 는 events 의 NOT NULL 계약을 경계에서 확인한다. 디코더가 만든 이벤트를 store 로
// 넘기기 전에 부른다 — 여기서 걸러야 UNIQUE 위반이나 NOT NULL 위반이 SQL 에러로 뒤늦게 터지지 않는다.
func (e Event) Validate() error {
	switch {
	case e.Vendor == "":
		return fmt.Errorf("event: vendor 가 비어 있음 (name=%q)", e.Name)
	case e.InstallationID == "":
		return fmt.Errorf("event: installation_id 가 비어 있음 (name=%q)", e.Name)
	case !e.Signal.Valid():
		return fmt.Errorf("event: 알 수 없는 signal %q (name=%q)", e.Signal, e.Name)
	case e.Name == "":
		return fmt.Errorf("event: name 이 비어 있음 (vendor=%q)", e.Vendor)
	case e.TS <= 0:
		// 벤더가 타임스탬프를 못 채운 경우다. 0 을 그대로 받으면 1970 시간 버킷에 쌓여
		// 보존 정책이 즉시 지우고 화면에는 영원히 안 보이는 이벤트가 된다.
		return fmt.Errorf("event: ts 가 양수가 아님 (%d, name=%q)", int64(e.TS), e.Name)
	case e.Sequence < 0:
		return fmt.Errorf("event: sequence 가 음수임 (%d, name=%q)", e.Sequence, e.Name)
	}
	return nil
}
