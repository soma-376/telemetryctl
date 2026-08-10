package daemon

// dedupWindow 는 배선 단계의 중복 제거 창이다.
//
// # 왜 여기에 또 있는가
//
// 같은 이벤트를 세 소비자가 받는데 중복에 대한 태도가 셋 다 다르다.
//
//	store  — events.dedup_key UNIQUE 로 영구적으로 접는다. 재시작에도 유효하다.
//	rollup — 자체 유계 FIFO 창을 갖는다. UPSERT 누적이라 한 번 더한 값을 되돌릴 수 없다.
//	session— **중복 제거가 없다.** 조립기는 받은 것을 그대로 세고 타임라인에 붙인다.
//
// 그래서 배선이 걸러 주지 않으면 exporter 재전송 한 번에 두 가지가 동시에 깨진다.
// 툴 타임라인이 같은 호출을 여러 줄로 보여 주고, 더 나쁘게는 sessions 합계가
// rollup_hourly 합계와 갈린다 — rollup 은 중복을 무시하는데 session 은 두 번 세기
// 때문이다. 그것이 정확히 internal/session/agreement_test.go 가 막으려는 어긋남이다.
//
// 두 집계기가 "같은 스트림을 같은 순서로" 보려면 걸러내는 지점도 하나여야 한다.
// 그 지점이 여기다. rollup 의 자체 창은 그대로 두되(이 패키지 없이도 안전해야 한다)
// 이 창을 지난 이벤트만 양쪽에 들어간다.
//
// # 정확성이 아니라 비용의 문제다
//
// 창 밖으로 밀려난 중복은 통과하지만 store 의 UNIQUE 가 잡는다. 즉 창 크기는
// "얼마나 일찍 거르느냐" 이고, 창을 지나친 중복이 만드는 유일한 실질 피해는 세션
// 타임라인 중복이다. rollup 과 같은 16384 를 쓴다 — 두 창의 크기가 다르면 그 사이
// 구간에서만 둘의 판정이 갈리는, 재현하기 가장 어려운 종류의 불일치가 생긴다.
const dedupCapacity = 16384

type dedupWindow struct {
	seen     map[string]struct{}
	ring     []string
	next     int
	capacity int
}

func newDedupWindow(capacity int) *dedupWindow {
	if capacity <= 0 {
		capacity = dedupCapacity
	}
	return &dedupWindow{
		seen:     make(map[string]struct{}),
		ring:     make([]string, 0, capacity),
		capacity: capacity,
	}
}

// add 는 키가 처음이면 기록하고 true 를, 이미 본 것이면 false 를 돌려준다.
func (d *dedupWindow) add(key string) bool {
	if key == "" {
		// 키를 만들 수 없는 이벤트는 거르지 않는다. 빈 키끼리 서로를 중복으로
		// 판정하면 서로 다른 이벤트가 하나로 접힌다 — 중복을 덜 접는 쪽이 낫다.
		return true
	}
	if _, ok := d.seen[key]; ok {
		return false
	}
	if len(d.ring) < d.capacity {
		d.ring = append(d.ring, key)
	} else {
		delete(d.seen, d.ring[d.next])
		d.ring[d.next] = key
		d.next = (d.next + 1) % d.capacity
	}
	d.seen[key] = struct{}{}
	return true
}
