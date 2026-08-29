package session

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"

	"github.com/your-org/pulsemetry/internal/event"
)

// 턴 경계 산출. v3 스키마의 turns · tool_calls · file_changes 가 요구하는 값을 만든다.
//
// 이 파일은 패키지의 나머지와 같은 규율을 따른다 — 표준 라이브러리와 internal/event 만
// 쓰고 파일·네트워크·DB·시계에 접근하지 않는다. 같은 입력을 같은 순서로 넣으면 항상 같은
// 결과가 나온다.
//
// # 왜 store 가 아니라 여기인가
//
// tool_calls.call_key 는 **전역 UNIQUE** 다. 벤더가 tool_use_id 를 주지 않으면 결정
// 이벤트와 결과 이벤트를 같은 호출로 묶을 근거가 도착 순서밖에 없는데, 그 순서를 아는 것은
// 이벤트 스트림을 순서대로 보는 이 패키지뿐이다. store 가 추측하면 서로 다른 호출이 한 행으로
// 합쳐지거나(UNIQUE 충돌로 배치 전체가 실패) 같은 호출이 두 행으로 갈린다.

// Turn 은 이벤트 하나가 귀속될 턴과, 그 이벤트가 만드는 도구 호출의 식별자다.
type Turn struct {
	// Key 는 turns.turn_key 다. 빈 값이면 세션 수준 가상 턴이다 — 실제 센티널 문자열은
	// store 가 정한다. 여기서 정하면 저장 계층의 어휘가 이 패키지로 새어 나온다.
	Key string
	// PromptText 는 turns.prompt_text 다. 턴을 여는 사용자 프롬프트에서만 채워진다.
	PromptText string
	// CallKey 는 tool_calls.call_key 다. 도구 결정·결과 이벤트에서만 채워진다.
	// 벤더 값이 있으면 "vendor:값", 없으면 "vendor:sha256(...)" 이다 — 둘 다 벤더 접두가
	// 붙으므로 벤더가 늘어도 전역 UNIQUE 가 깨지지 않는다.
	CallKey string
}

// TurnTracker 는 도착 순서대로 턴 경계를 매긴다. 동시 사용에 안전하지 않다 —
// Assembler 와 같은 고루틴이 소유한다.
type TurnTracker struct {
	sessions map[string]*turnCursor
}

// NewTurnTracker 는 빈 추적기를 만든다.
func NewTurnTracker() *TurnTracker {
	return &TurnTracker{sessions: make(map[string]*turnCursor)}
}

// turnCursor 는 세션 하나의 열린 턴과 턴별 도구 호출 원장이다.
type turnCursor struct {
	// open 은 현재 열린 턴 키다. 빈 값이면 아직 첫 프롬프트를 보지 못했다 —
	// 그 앞의 이벤트는 전부 가상 턴으로 간다.
	open    string
	ledgers map[string]*callLedger
}

// callLedger 는 턴 하나 안의 도구 호출 진행 상태다.
type callLedger struct {
	// opened 는 툴 이름별로 지금까지 연 호출 수다. 합성 키의 n 이 여기서 나온다.
	opened map[string]int
	// pending 은 결정만 보고 결과를 기다리는 호출 키의 도착 순서 큐다.
	// 결과 이벤트가 큐 앞에서 하나를 꺼내 같은 호출로 합쳐진다.
	pending map[string][]string
}

func newCallLedger() *callLedger {
	return &callLedger{opened: map[string]int{}, pending: map[string][]string{}}
}

// Assign 은 이벤트 하나의 턴과 도구 호출 식별자를 정한다.
//
// 규칙:
//   - *.user_prompt 가 턴을 연다. turn_key 는 벤더가 준 값(prompt.id)이 있으면 그것,
//     없으면 그 프롬프트 이벤트의 DedupKey 다 — 세션 안에서 유일하고 재전송에도 안정적이다.
//   - 그 뒤의 이벤트는 열린 턴에 붙는다.
//   - 첫 프롬프트 앞의 이벤트, 세션 수준 시그널(session.count · mcp.connection ·
//     active_time.total), 누적 데이터포인트는 가상 턴으로 간다. 누적 포인트는 턴 하나의
//     사건이 아니라 계열의 누계라 특정 턴에 귀속시키면 그 턴만 값이 부푼다.
func (t *TurnTracker) Assign(in Input) Turn {
	e := in.Event
	if e.SessionID == "" {
		return Turn{}
	}
	c := t.sessions[e.SessionID]
	if c == nil {
		c = &turnCursor{ledgers: map[string]*callLedger{}}
		t.sessions[e.SessionID] = c
	}

	k := classify(e.Name)
	out := Turn{Key: c.turnKeyFor(e, k)}
	if k == kindUserPrompt && in.Content.Kind == event.ContentPrompt {
		out.PromptText = in.Content.Body
	}
	if k == kindToolResult || k == kindToolDecision {
		out.CallKey = c.callKeyFor(e, k, out.Key)
	}
	return out
}

// Forget 은 세션 하나의 턴 상태를 지운다. 데몬은 오래 살고 세션은 계속 쌓이므로
// Assembler.Prune 과 같은 시점에 불러야 맵이 무한히 자라지 않는다.
func (t *TurnTracker) Forget(sessionID string) { delete(t.sessions, sessionID) }

func (c *turnCursor) turnKeyFor(e event.Event, k kind) string {
	if sessionLevelKind(k) || e.Temporality == event.TemporalityCumulative {
		return ""
	}
	if k == kindUserPrompt {
		key := e.TurnKey
		if key == "" {
			key = e.DedupKey()
		}
		c.open = key
		return key
	}
	if e.TurnKey != "" {
		// 벤더가 턴을 직접 알려 줬다. 추론보다 우선한다.
		c.open = e.TurnKey
		return e.TurnKey
	}
	return c.open
}

// sessionLevelKind 는 턴이 아니라 세션에 매달리는 시그널인지 본다.
func sessionLevelKind(k kind) bool {
	switch k {
	case kindSessionCount, kindMCPConnection, kindActiveTime:
		return true
	}
	return false
}

// callKeyFor 는 tool_calls.call_key 를 정한다.
//
// 벤더가 tool_use_id·call_id 를 주면 그것이 정답이다. 안 주면 턴 안에서 같은 툴의 몇 번째
// 호출인지로 합성한다 — 결정 이벤트가 번호를 하나 열고, 뒤따르는 결과 이벤트가 그 번호를
// 이어받는다. 결과만 오는 자동 승인 호출은 그 자리에서 새 번호를 연다.
func (c *turnCursor) callKeyFor(e event.Event, k kind, turnKey string) string {
	if e.CallKey != "" {
		return e.Vendor + ":" + e.CallKey
	}
	l := c.ledgers[turnKey]
	if l == nil {
		l = newCallLedger()
		c.ledgers[turnKey] = l
	}
	tool := e.Attr.ToolName

	if k == kindToolResult {
		if q := l.pending[tool]; len(q) > 0 {
			l.pending[tool] = q[1:]
			return q[0]
		}
		return e.Vendor + ":" + synthCallKey(e.SessionID, turnKey, tool, l.next(tool))
	}

	key := e.Vendor + ":" + synthCallKey(e.SessionID, turnKey, tool, l.next(tool))
	l.pending[tool] = append(l.pending[tool], key)
	return key
}

func (l *callLedger) next(tool string) int {
	n := l.opened[tool]
	l.opened[tool] = n + 1
	return n
}

// synthCallKey 는 (세션, 턴, 툴, 순번) 을 길이 접두로 이어 붙여 해시한다.
// 길이 접두가 없으면 ("a","bc") 와 ("ab","c") 가 같은 키가 돼 서로 다른 호출이 한 행으로 접힌다.
func synthCallKey(sessionID, turnKey, tool string, n int) string {
	h := sha256.New()
	for _, f := range []string{sessionID, turnKey, tool, strconv.Itoa(n)} {
		h.Write([]byte(strconv.Itoa(len(f))))
		h.Write([]byte(":"))
		h.Write([]byte(f))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// FileChange 는 file_changes 한 행이다.
//
// Path 는 **정규화하지 않은 원경로**다. 스키마가 그 컬럼을 NOT NULL "파일 경로" 로 정의하고,
// basename 을 넣으면 같은 이름의 다른 디렉터리 파일이 한 행으로 뭉개진다 (ADR 0010).
// 로컬 저장 전용이며 상위 전달에는 실리지 않는다.
type FileChange struct {
	Path        string
	Operation   string // create | modify | delete | rename
	RenamedFrom string
	Additions   event.Opt[int64]
	Deletions   event.Opt[int64]
	OldHash     string
	NewHash     string
}

// file_changes.operation 의 CHECK 어휘다. 스키마가 네 가지로 고정했다.
const (
	OperationCreate = "create"
	OperationModify = "modify"
	OperationDelete = "delete"
	OperationRename = "rename"
)

// FileChangeOf 는 도구 이벤트 하나에서 파일 변경을 뽑는다. 없으면 ok=false 다.
//
// 읽기 도구는 파일을 바꾸지 않으므로 대상이 아니다. 실패한 편집도 마찬가지다 —
// 성공 여부가 미상이면 반영한다(게이트가 꺼진 설치에서 success 가 오지 않는다).
//
// 줄 수(additions·deletions)는 채우지 않는다. lines_of_code 메트릭에 파일명이 없어
// 파일별 정확한 값을 알 수 없고, 스키마가 "미관측은 NULL" 이라고 못 박았다.
func FileChangeOf(in Input) (FileChange, bool) {
	if in.TargetPath == "" {
		return FileChange{}, false
	}
	act := actionOf(in.Event.Attr.ToolName)
	if !touchesFile(act) {
		return FileChange{}, false
	}
	if ok, set := in.Event.Measure.Success.Get(); set && !ok {
		return FileChange{}, false
	}

	op := OperationModify
	if act == ActionWrite {
		op = OperationCreate
	}
	return FileChange{Path: in.TargetPath, Operation: op}, true
}
