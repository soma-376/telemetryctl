package session

import (
	"fmt"
	"sort"
	"time"

	"github.com/your-org/pulsemetry/internal/event"
)

// 임계값은 관측 후 조정할 값이라 상수로 두되 Option 으로 바꿀 수 있게 한다 (ADR 0005 Follow-up).
const (
	// DefaultIdleThreshold 는 세션 마감 기준이다. Claude Code 의 로그 내보내기 주기가 5초,
	// 메트릭이 60초이므로 10분이면 충분히 여유롭다.
	DefaultIdleThreshold = 10 * time.Minute
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
	}
	for _, o := range opts {
		o(a)
	}
	return a
}

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

// Advance 는 now 기준으로 유휴 임계값을 넘긴 세션을 마감하고 마감된 세션을 돌려준다.
//
// now 는 인자다 — 조립기가 시계를 읽으면 10분 경계 테스트가 벽시계에 의존하게 된다.
// 데몬은 마감 틱에서, 테스트는 원하는 시각으로 부른다.
func (a *Assembler) Advance(now event.UnixSec) []Session {
	limit := event.UnixSec(a.idle / time.Second)

	var closed []Session
	for _, s := range a.sessions {
		if s.ended.Valid() || now-s.last < limit {
			continue
		}
		a.close(s)
		closed = append(closed, s.session())
	}
	sortSessions(closed)
	return closed
}

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

// Prune 은 before 이전에 마감된 세션을 조립기 메모리에서 지우고 지운 개수를 돌려준다.
// 데몬은 오래 살고 세션은 계속 쌓이므로 이 호출이 없으면 맵이 무한히 자란다.
// 지워진 session.id 가 다시 등장하면 새 세션으로 시작한다.
func (a *Assembler) Prune(before event.UnixSec) int {
	n := 0
	for id, s := range a.sessions {
		if v, ok := s.ended.Get(); ok && v < before {
			delete(a.sessions, id)
			n++
		}
	}
	return n
}

// close 는 세션을 마감하고 상태를 확정한다.
//
// ended_at 은 **마지막 이벤트 시각**이다. 마감을 감지한 시각(마지막 이벤트 + 10분)으로
// 두면 모든 세션의 소요 시간에 유휴 10분이 유령처럼 붙는다.
//
// 우선순위는 handoff > abandoned > completed 다. handoff 는 "다른 벤더 세션이 시작됐다"는
// 관측된 사건이고 abandoned 는 마지막 툴 실패에서 끌어낸 추측이라 근거가 더 강하다.
// 두 판정이 겹치면 근거 문장에 둘 다 남긴다.
func (a *Assembler) close(s *state) {
	s.ended = event.Some(s.last)

	abandonedReason := ""
	if s.lastOutcomeSeen && !s.lastOutcomeOK {
		abandonedReason = fmt.Sprintf("마지막 툴 이벤트 %s 가 실패(ts=%d)하고 이후 성공 없음",
			s.lastOutcomeTool, int64(s.lastOutcomeTS))
	}

	if to, at, ok := a.handedOff(s); ok {
		s.status = StatusHandoff
		s.statusReason = fmt.Sprintf("같은 프로젝트에서 %s 세션이 마지막 이벤트 %d초 뒤에 시작(vendor=%s → %s)",
			to, int64(at-s.last), s.vendor, to)
		if abandonedReason != "" {
			s.statusReason += "; " + abandonedReason
		}
		return
	}
	if abandonedReason != "" {
		s.status = StatusAbandoned
		s.statusReason = abandonedReason
		return
	}
	s.status = StatusCompleted
	s.statusReason = fmt.Sprintf("마지막 이벤트 후 %s 유휴", a.idle)
}

// handedOff 는 같은 project_hash 에서 벤더가 다른 세션이 이 세션의 마지막 이벤트로부터
// 시간 창 안에 시작했는지 본다.
//
// 기준점을 started_at 이 아니라 last_event_at 으로 잡았다. 계획서는 "30분 내에 다른 벤더
// 세션이 시작"이라고만 했는데, 알고 싶은 것은 "작업을 넘겼는가"이므로 이 세션이 멈춘
// 시점부터 재는 것이 맞다. 시작 시각 기준이면 30분 넘게 이어진 세션은 어떤 후속 세션도
// 잡지 못한다.
//
// project_hash 가 없으면(게이트·속성 부재) 판정하지 않는다 — 프로젝트를 모르는 세션끼리
// 묶으면 무관한 세션이 전부 handoff 가 된다.
func (a *Assembler) handedOff(s *state) (vendor string, at event.UnixSec, ok bool) {
	if s.projectHash == "" {
		return "", 0, false
	}
	window := event.UnixSec(a.handoff / time.Second)

	best := (*state)(nil)
	for _, t := range a.sessions {
		switch {
		case t.id == s.id, t.vendor == s.vendor,
			t.projectHash != s.projectHash,
			t.started < s.last, t.started > s.last+window:
			continue
		}
		// 가장 먼저 시작한 세션이 넘겨받은 세션이다. 동시 시작은 id 로 갈라 결정론을 유지한다.
		if best == nil || t.started < best.started || (t.started == best.started && t.id < best.id) {
			best = t
		}
	}
	if best == nil {
		return "", 0, false
	}
	return best.vendor, best.started, true
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
func Assemble(in []Input, now event.UnixSec, opts ...Option) []Session {
	a := New(opts...)
	for _, i := range in {
		a.Add(i)
	}
	a.Advance(now)
	return a.Snapshot()
}
