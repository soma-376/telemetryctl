package rollup

// dedupSet 은 최근 본 DedupKey 를 유계 FIFO 로 기억한다.
//
// **책임 범위**: 이 패키지는 "메모리에 살아 있는 집계 창 안에서" 같은 이벤트가 두 번 더해지는
// 것만 막는다. 영구적·권위적인 유일성은 6단계 store 의 events.dedup_key UNIQUE 제약이 진다.
// 둘 다 필요한 이유는 역할이 다르기 때문이다 —
//   - store 는 데몬이 재시작해도 유효하지만, INSERT 를 시도해야 알 수 있다.
//   - rollup 은 UPSERT 로 누적되는 rollup_hourly 특성상 한 번 더해진 값을 되돌릴 수 없어서
//     store 에 물어보기 전에 자체적으로 걸러야 한다.
//
// **메모리 경계**: 무한히 자라는 맵을 두지 않는다. 용량에 도달하면 가장 오래 들어온 키부터
// 버리는 FIFO 창이다. 창 밖으로 밀려난 중복은 여기서 통과하지만 store 의 UNIQUE 가 잡는다 —
// 즉 이 창의 크기는 정확성이 아니라 store 왕복을 얼마나 아끼느냐의 문제다.
// 시간 기반이 아니라 건수 기반인 이유는 이 패키지가 "지금 시각" 을 읽지 않기 때문이다.
type dedupSet struct {
	seenKeys map[string]struct{}
	ring     []string
	next     int
	capacity int
}

func newDedupSet(capacity int) *dedupSet {
	return &dedupSet{
		seenKeys: make(map[string]struct{}),
		ring:     make([]string, 0, capacity),
		capacity: capacity,
	}
}

// seen 은 키를 이미 봤으면 true 를 돌려주고, 처음이면 기록한 뒤 false 를 돌려준다.
func (d *dedupSet) seen(key string) bool {
	if _, ok := d.seenKeys[key]; ok {
		return true
	}
	if len(d.ring) < d.capacity {
		d.ring = append(d.ring, key)
	} else {
		delete(d.seenKeys, d.ring[d.next])
		d.ring[d.next] = key
		d.next = (d.next + 1) % d.capacity
	}
	d.seenKeys[key] = struct{}{}
	return false
}
