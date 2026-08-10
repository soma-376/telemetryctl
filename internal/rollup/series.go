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
	last float64
	// used 는 마지막으로 관측된 순번이다. 용량 초과 시 가장 오래 안 쓴 계열을 고르는 데 쓴다.
	used uint64
}

// seriesStore 는 cumulative 계열의 직전 값을 유계로 들고 있는다.
type seriesStore struct {
	state     map[seriesID]seriesState
	capacity  int
	tick      uint64
	coldStart ColdStartPolicy

	resets    int64
	baselines int64
	evicted   int64
}

func newSeriesStore(capacity int, coldStart ColdStartPolicy) *seriesStore {
	return &seriesStore{
		state:     make(map[seriesID]seriesState),
		capacity:  capacity,
		coldStart: coldStart,
	}
}

// observe 는 cumulative 데이터포인트를 받아 이번에 더할 양을 돌려준다.
//
// 판정 규칙:
//   - 직전 값이 있고 새 값이 그보다 크거나 같으면 → 차이만 더한다.
//   - 새 값이 직전 값보다 **작으면** 리셋이다. 벤더 프로세스가 재시작해 카운터가 0 부터 다시
//     시작한 경우라 차이는 음수가 된다. 음수를 더하면 집계가 깎이므로 새 값 전체를 더한다.
//   - 직전 값이 없으면 ColdStartPolicy 를 따른다.
func (s *seriesStore) observe(id seriesID, raw float64) (float64, Disposition) {
	prev, known := s.state[id]
	switch {
	case !known:
		s.put(id, raw)
		if s.coldStart == ColdStartFull {
			return raw, Counted
		}
		s.baselines++
		return 0, Baseline
	case raw < prev.last:
		s.put(id, raw)
		s.resets++
		return raw, Counted
	default:
		s.put(id, raw)
		return raw - prev.last, Counted
	}
}

func (s *seriesStore) put(id seriesID, v float64) {
	if _, known := s.state[id]; !known && len(s.state) >= s.capacity {
		s.evictLeastUsed()
	}
	s.tick++
	s.state[id] = seriesState{last: v, used: s.tick}
}

// evictLeastUsed 는 가장 오래 관측되지 않은 계열 하나를 버린다.
// 세션이 끝나면 그 계열은 다시 오지 않으므로 유휴 계열이 영원히 쌓이는 것을 막는다.
// 버린 계열이 나중에 다시 오면 콜드 스타트로 잡힌다 — 기본 정책에서는 과소 집계이고
// 과대 집계는 아니다. 용량은 O(n) 스캔이 문제되지 않을 규모(수천)를 전제로 한다.
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
