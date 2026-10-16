package session

import (
	"sort"
	"time"

	"github.com/your-org/pulsemetry/internal/event"
)

// 임계값은 관측 후 조정할 값이라 상수로 두되 Option 으로 바꿀 수 있게 한다 (ADR 0005 Follow-up).
const (
	// DefaultIdleThreshold 는 세션 마감 기준이다.
	//
	// 벤더 lifecycle 훅이 생기기 전에는 이것이 유일한 마감 수단이라 exporter 주기(로그
	// 5초·메트릭 60초) 대비 여유만 보고 10분을 썼다. 지금은 훅이 정확한 종료 시각을
	// 주므로 이 값은 **훅이 유실됐을 때만 쓰는 바닥**이다 — 크래시·강제 종료·OS 종료.
	//
	// 그래서 기준이 "exporter 주기" 에서 "사람이 자리를 비우는 시간" 으로 바뀐다. 점심과
	// 긴 회의를 덮되, 훅을 놓친 세션이 진행 중으로 잘못 보이는 시간은 반나절 안쪽으로
	// 묶는다. 유휴로 닫힌 세션은 이벤트가 다시 오면 되살아나므로(state.observe) 살아 있는
	// 세션을 자르는 쪽의 대가가 더 작다.
	DefaultIdleThreshold = 4 * time.Hour
	// DefaultHandoffWindow 는 같은 프로젝트에서 다른 벤더로 넘어갔다고 볼 시간 창이다.
	DefaultHandoffWindow = 30 * time.Minute
)

// Assembler 는 이벤트를 세션으로 조립한다.
//
// 상태를 들고 있지만 IO 도 시계 접근도 하지 않는다 — 같은 입력을 같은 순서로 넣으면 항상
// 같은 결과가 나온다. "지금 시각"은 Advance 의 인자로만 들어온다.
//
// 동시성 안전하지 않다. 계획서의 수신기 워커가 2개이므로 조립기는 한 고루틴이 소유하거나
// 호출자가 직렬화해야 한다.
type Assembler struct {
	idle     time.Duration
	handoff  time.Duration
	sessions map[string]*state

	// turns 는 턴 경계 추적기다. Add 와 분리해 둔 이유는 Add 의 시그니처를 바꾸지 않기
	// 위해서다 — 턴 경계는 v3 스키마가 새로 요구한 것이고, 세션 조립의 기존 계약(이벤트
	// 하나를 넣고 성공 여부를 받는다)에는 자리가 없다. 두 호출이 같은 이벤트를 각자 보는
	// 약간의 중복을 감수하고 기존 호출자를 그대로 둔다.
	turns *TurnTracker

	// watchFrom 은 이 조립기가 처음 이벤트를 받은 시각이다. cumulative 첫 관측을 값 전체로
	// 셀지 기준선으로만 잡을지를 가른다 (event.CumulativePoint.WatchFrom).
	//
	// rollup.Aggregator 가 같은 방식으로 같은 값을 잡는다. 데몬이 같은 스트림을 두 곳에
	// 먹이므로 기준점이 어긋나면 sessions 와 rollup_hourly 의 비용이 달라진다.
	watchFrom event.UnixNano
	watching  bool
}

type Option func(*Assembler)

// WithIdleThreshold 는 유휴 마감 임계값을 바꾼다. 0 이하는 무시한다.
func WithIdleThreshold(d time.Duration) Option {
	return func(a *Assembler) {
		if d > 0 {
			a.idle = d
		}
	}
}

// WithHandoffWindow 는 handoff 판정 시간 창을 바꾼다. 0 이하는 무시한다.
func WithHandoffWindow(d time.Duration) Option {
	return func(a *Assembler) {
		if d > 0 {
			a.handoff = d
		}
	}
}

func New(opts ...Option) *Assembler {
	a := &Assembler{
		idle:     DefaultIdleThreshold,
		handoff:  DefaultHandoffWindow,
		sessions: make(map[string]*state),
		turns:    NewTurnTracker(),
	}
	for _, o := range opts {
		o(a)
	}
	return a
}

// IdleThreshold 는 Advance 가 쓰는 유휴 마감 임계값이다. DB 유휴 스윕이 조립기보다
// 공격적으로 닫지 않도록 같은 값을 보고 컷오프를 잡는다.
func (a *Assembler) IdleThreshold() time.Duration { return a.idle }

// Add 는 이벤트 하나를 반영한다.
//
// session.id 가 없거나 events 의 NOT NULL 계약을 못 지키는 이벤트는 무시하고 false 를
// 돌려준다. 조립기는 세션 단위로만 동작하므로 session.id 없는 이벤트는 rollup 과 events
// 테이블의 몫이다.
func (a *Assembler) Add(in Input) bool {
	e := in.Event
	if e.Validate() != nil {
		return false
	}
	// 관측 기준점은 session.id 검사보다 **먼저** 찍는다. session.id 없는 이벤트도 우리가
	// 그때부터 보고 있었다는 증거이고, rollup 의 집계기는 그런 이벤트도 받아 기준점을 잡는다.
	// 여기서만 세션 있는 이벤트를 기다리면 두 기준점이 어긋나 누적 집계가 갈린다.
	if !a.watching {
		a.watching, a.watchFrom = true, e.TS
	}
	if e.SessionID == "" {
		return false
	}
	s := a.sessions[e.SessionID]
	if s == nil {
		s = newState(e, a.watchFrom)
		a.sessions[e.SessionID] = s
	}
	s.observe(e)
	s.apply(in)
	return true
}

// TurnOf 는 이벤트 하나가 귀속될 턴과 도구 호출 식별자를 돌려준다 (turn.go).
//
// Add 와 짝으로 부른다. 순서는 상관없지만 **같은 이벤트를 두 번 넣으면 안 된다** —
// 도구 호출 순번이 그만큼 밀린다. 배선 단계의 중복 제거 창이 그 앞에 있다.
func (a *Assembler) TurnOf(in Input) Turn { return a.turns.Assign(in) }

// Snapshot 은 진행 중·마감된 세션을 모두 돌려준다. store 가 기본키로 upsert 한다.
func (a *Assembler) Snapshot() []Session {
	out := make([]Session, 0, len(a.sessions))
	for _, s := range a.sessions {
		out = append(out, s.session())
	}
	sortSessions(out)
	return out
}

// Session 은 세션 하나를 돌려준다.
func (a *Assembler) Session(id string) (Session, bool) {
	s := a.sessions[id]
	if s == nil {
		return Session{}, false
	}
	return s.session(), true
}

// StartLifecycle 는 명시적 시작·재개 훅을 **이미 관측 중인** 세션에 반영한다.
//
// 조립기가 모르는 세션에는 상태를 만들지 않는다 — EndLifecycle 과 같은 원칙이다.
// 행은 store 가 직접 만들고(startLifecycleSQL), 활동 관측이 없는 세션을 조립기가
// 소유하면 그 스냅샷이 생명주기 정본 행세를 하며 스윕이 닫은 세션을 매 틱 되살린다.
func (a *Assembler) StartLifecycle(sessionID string, at event.UnixSec) {
	s := a.sessions[sessionID]
	if s == nil {
		return
	}
	if s.started == 0 || at < s.started {
		s.started = at
	}
	// 재개도 활동이다. last 를 밀지 않으면 유휴로 닫혔던 세션을 재개한 직후 Advance 가
	// 즉시 도로 닫는다 — startLifecycleSQL 이 last_activity_at 을 MAX 로 미는 것과 짝.
	if at > s.last {
		s.last = at
	}
	s.ended = event.Opt[event.UnixSec]{}
	s.hookEnded = false
	s.status = StatusRunning
	s.statusReason = ""
}

// Prune 은 before 이전이 마지막 활동인 세션을 조립기 메모리에서 지우고 지운 개수를
// 돌려준다. 데몬은 오래 살고 세션은 계속 쌓이므로 이 호출이 없으면 맵이 무한히 자란다.
// 지워진 session.id 가 다시 등장하면 새 세션으로 시작한다.
//
// 마감이 아니라 **활동**을 기준으로 한다. 메모리 관리는 생명주기와 무관해야 한다 —
// 마감은 DB 가 소유하므로(ADR 0021) 조립기는 자기가 마지막으로 본 시각만 안다.
func (a *Assembler) Prune(before event.UnixSec) int {
	return a.PruneIf(before, nil)
}

// PruneIf 는 제거 대상 세션에 allow를 호출하고, 허용된 세션만 메모리에서 제거한다.
// 연관 상태의 정리가 준비되지 않으면 false를 반환해 다음 정리까지 보류할 수 있다.
func (a *Assembler) PruneIf(before event.UnixSec, allow func(Session) bool) int {
	n := 0
	for id, s := range a.sessions {
		if s.last < before {
			if allow != nil && !allow(s.session()) {
				continue
			}
			delete(a.sessions, id)
			a.turns.Forget(id)
			n++
		}
	}
	return n
}

func sortSessions(s []Session) {
	sort.Slice(s, func(i, j int) bool {
		if s[i].StartedAt != s[j].StartedAt {
			return s[i].StartedAt < s[j].StartedAt
		}
		return s[i].SessionID < s[j].SessionID
	})
}

// Assemble 은 이벤트 묶음을 한 번에 조립한다. 일괄 재처리와 테스트용 편의 함수다.
// 마감은 담지 않는다 — 생명주기는 DB 가 소유한다 (ADR 0021).
func Assemble(in []Input, opts ...Option) []Session {
	a := New(opts...)
	for _, i := range in {
		a.Add(i)
	}
	return a.Snapshot()
}
