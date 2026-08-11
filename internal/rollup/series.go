package rollup

import "github.com/your-org/pulsemetry/internal/event"

// seriesID 는 cumulative 계열의 식별자다.
//
// 메트릭 이름이 같아도 속성 조합이 다르면 다른 계열이다. 예를 들어 claude_code.token.usage 의
// type=input 과 type=output 을 한 계열로 묶으면 서로의 직전 값을 덮어써 차이가 번갈아 음수가
// 되고 매 포인트가 리셋으로 오판된다.
//
// session_id 를 반드시 포함한다. Claude Code 두 개가 동시에 돌면 각자의 cumulative 카운터가
// 독립적으로 오르는데, 세션을 빼면 두 계열이 하나로 섞여 값이 오르내리며 리셋 판정이 폭주한다.
//
// event.Attributes 는 문자열 필드만 있어 comparable 이므로 구조체를 통째로 맵 키로 쓴다.
// 필드를 나열해 문자열 키를 만들면 event.Attributes 에 속성이 추가될 때 조용히 빠진다.
type seriesID struct {
	vendor       string
	installation string
	session      string
	name         string
	attr         event.Attributes
}

func seriesIDOf(e event.Event) seriesID {
	return seriesID{
		vendor:       e.Vendor,
		installation: e.InstallationID,
		session:      e.SessionID,
		name:         e.Name,
		attr:         e.Attr,
	}
}

type seriesState struct {
	// cum 은 이 계열의 직전 관측이다. 증분 판정 규칙 자체는 event 패키지가 소유한다 —
	// session 이 세션 합계에 같은 규칙을 써야 rollup_hourly 와 sessions 가 갈리지 않는다.
	cum event.CumulativeState
	// used 는 마지막으로 관측된 순번이다. 용량 초과 시 가장 오래 안 쓴 계열을 고르는 데 쓴다.
	// 계열 저장·축출은 이 패키지에만 있다 — 집계기가 데몬 수명 내내 모든 세션의 계열을
	// 보기 때문이고, 세션 상태는 마감되면 Prune 으로 통째로 사라져 이 문제가 없다.
	used uint64
}

// seriesStore 는 cumulative 계열의 직전 값을 유계로 들고 있는다.
type seriesStore struct {
	state    map[seriesID]seriesState
	capacity int
	tick     uint64

	resets    int64
	baselines int64
	evicted   int64
}

func newSeriesStore(capacity int) *seriesStore {
	return &seriesStore{
		state:    make(map[seriesID]seriesState),
		capacity: capacity,
	}
}

// observe 는 cumulative 데이터포인트를 받아 이번에 더할 양을 돌려준다.
//
// 판정은 event.CumulativeState.Step 이 한다 — 여기서는 계열을 찾아 상태를 넣고 빼고,
// 결과를 이 패키지의 Disposition·통계 어휘로 옮기는 것만 한다.
func (s *seriesStore) observe(id seriesID, p event.CumulativePoint) (float64, Disposition) {
	prev := s.state[id] // 없으면 제로값 = CumulativeState.Known false
	delta, next, kind := prev.cum.Step(p)
	s.put(id, next)

	switch kind {
	case event.CumulativeBaseline:
		s.baselines++
		return 0, Baseline
	case event.CumulativeReset:
		s.resets++
	}
	return delta, Counted
}

func (s *seriesStore) put(id seriesID, cum event.CumulativeState) {
	if _, known := s.state[id]; !known && len(s.state) >= s.capacity {
		s.evictLeastUsed()
	}
	s.tick++
	s.state[id] = seriesState{cum: cum, used: s.tick}
}

// evictLeastUsed 는 가장 오래 관측되지 않은 계열 하나를 버린다.
// 세션이 끝나면 그 계열은 다시 오지 않으므로 유휴 계열이 영원히 쌓이는 것을 막는다.
// 버린 계열이 나중에 다시 오면 콜드 스타트로 잡힌다 — start_time 이 없으면 기준선만 잡아
// 과소 집계되고, 있으면 값 전체를 다시 더해 밀려나기 전 구간이 두 번 들어간다.
// 그래서 용량은 넉넉해야 한다(Stats.SeriesEvicted 가 계속 늘면 모자라다는 뜻이다).
// 용량은 O(n) 스캔이 문제되지 않을 규모(수천)를 전제로 한다.
func (s *seriesStore) evictLeastUsed() {
	var victim seriesID
	var oldest uint64
	first := true
	for id, st := range s.state {
		if first || st.used < oldest {
			victim, oldest, first = id, st.used, false
		}
	}
	if !first {
		delete(s.state, victim)
		s.evicted++
	}
}
