package event

// 누적(cumulative) 메트릭을 증분으로 바꾸는 규칙이다.
//
// # 왜 event 패키지인가
//
// 이 규칙을 쓰는 곳이 둘이다 — rollup 은 rollup_hourly 의 시간 버킷을, session 은
// sessions 행의 세션 합계를 만든다. 화면에서 그 둘은 같은 사실의 두 표현이다(Today 카드가
// rollup, 세션 상세가 sessions). 규칙이 두 벌이면 같은 이벤트 스트림에서 서로 다른 비용이
// 나오고, 사용자는 어느 쪽을 믿어야 할지 알 수 없다. 실제로 한 번 갈렸다.
//
// # 무엇을 공유하고 무엇을 안 하는가
//
// 공유하는 것은 **포인트 하나를 증분으로 바꾸는 결정** 뿐이다. 계열을 어떻게 식별하고
// (rollup 은 vendor·installation·session·name·속성 전체, session 은 세션 상태 안에서
// name·기여 필드·속성), 상태를 얼마나 들고 있다가 무엇부터 버릴지(rollup 만 용량·LRU 축출을
// 가진다)는 각자에게 남긴다. 두 패키지의 수명과 메모리 압력이 다르기 때문이다 —
// rollup 의 집계기는 데몬 수명 내내 살아 모든 세션의 계열을 보지만, session 의 상태는
// 세션 하나에 매달려 있고 마감되면 Prune 으로 통째로 사라진다.
//
// 그래서 여기 있는 것은 상태 하나를 값으로 받아 다음 상태를 값으로 돌려주는 순수 함수다.
// 맵도, 용량도, 통계 카운터도 없다.

// CumulativeKind 는 포인트 하나를 어떻게 해석했는지다. 호출자가 통계를 세고 로그를 남기는 데
// 쓴다 — 조용히 0 을 더하면 화면의 숫자가 왜 작은지 아무도 설명하지 못한다.
type CumulativeKind uint8

const (
	// CumulativeIncrement 는 직전 값과의 차이를 더한 보통의 경우다.
	CumulativeIncrement CumulativeKind = iota
	// CumulativeFirstFull 은 첫 관측을 값 전체로 센 경우다. 계열이 우리가 보기 시작한 뒤에
	// 시작했으므로 이전 구간이 존재하지 않는다 — 값 전체가 새것이다.
	CumulativeFirstFull
	// CumulativeBaseline 은 첫 관측을 기준선으로만 기록하고 0 을 더한 경우다.
	// 계열이 우리보다 먼저 시작했거나 start_time 이 없어 판정할 수 없는 경우다.
	CumulativeBaseline
	// CumulativeReset 은 카운터가 다시 시작한 경우다. 새 값 전체가 재시작 이후의 누적이다.
	CumulativeReset
	// CumulativeOutOfOrder 는 같은 수집 구간에서 값이 뒤로 간 경우다. 0 을 더하고 기준선도
	// 유지한다 — 리셋이 아니라 순서가 뒤집힌 포인트다.
	CumulativeOutOfOrder
)

// CumulativePoint 는 누적 데이터포인트 하나와, 그것을 해석하는 데 필요한 관측자 쪽 문맥이다.
type CumulativePoint struct {
	// Value 는 데이터포인트의 누적값이다.
	Value float64
	// Start 는 이 포인트의 start_time_unix_nano (Event.StartTS). 0 이하면 모름이다.
	Start UnixNano
	// WatchFrom 은 **관측자의 문맥**이다 — 이 집계기가 이벤트를 보기 시작한 시각.
	// 포인트의 성질이 아니지만 첫 관측을 어떻게 셀지 여기서만 정해지므로 같이 받는다.
	// 두 패키지가 이 값을 각자 계산하면 콜드 스타트 판정이 갈리므로 규칙을 밖에 두지 않는다.
	WatchFrom UnixNano
}

// startedWhileWatching 은 이 계열이 우리가 보기 시작한 뒤에 값을 쌓기 시작했는지 본다.
//
// 계속 보고 있었는데 이 계열의 포인트를 이번에 처음 본다면, 그 이전 포인트는 존재하지 않았고
// 따라서 우리도 저장한 적이 없다. 값 전체가 새것이다.
// 반대로 계열이 우리보다 먼저 시작했다면 앞 구간을 이전 데몬 인스턴스가 이미 저장했을 수
// 있다. 그때 값을 통째로 더하면 계획서 리스크 표의 "cumulative 이중 집계 → 비용 10배" 다.
// start_time 이 없으면 판정할 수 없으므로 안전한 쪽(과소 집계)으로 간다.
func (p CumulativePoint) startedWhileWatching() bool {
	return p.Start > 0 && p.Start >= p.WatchFrom
}

// CumulativeState 는 누적 계열 하나의 직전 관측이다. 제로값이 "아직 본 적 없음" 이다.
//
// 값 타입이라 호출자가 자기 맵에 그대로 담고 Step 의 결과로 덮어쓰면 된다.
// 포인터를 돌려주면 계열 상태가 두 곳에서 갱신될 수 있고, 그 순간 리셋 판정이 순서에
// 의존하는 방식이 추적 불가능해진다.
type CumulativeState struct {
	Known bool
	Value float64
	Start UnixNano
}

// Step 은 누적 포인트 하나를 이번에 더할 증분으로 바꾸고 다음 상태를 돌려준다.
//
// 판정 규칙 — start_time 을 받은 경우가 정확한 쪽이고, 값의 증감은 못 받았을 때의 폴백이다.
//
//   - 첫 관측: 계열이 관측 시작 이후에 시작했으면 값 전체, 아니면 기준선만.
//   - **수집 구간이 바뀌면(Start 증가) 리셋이다.** 값이 줄지 않아도 리셋이다 — 벤더가
//     재시작한 뒤 다음 내보내기까지 직전 값을 넘어서면 값의 증감만으로는 못 잡고,
//     그때 차이만 더하면 재시작 이후 누적분의 대부분이 통째로 사라진다.
//   - 구간이 그대로인데 값이 줄면 리셋이 **아니다**. 0 을 더하고 기준선도 유지한다.
//     값 전체를 더하면 이중 집계이고, 기준선을 낮추면 다음 포인트의 차이가 그만큼 부풀어
//     결국 같은 양이 두 번 들어간다.
//   - Start 를 못 받은 채 값이 줄면 리셋으로 본다. 음수를 더해 집계를 깎느니 새 값 전체를 더한다.
//   - 그 외에는 차이만 더한다.
func (s CumulativeState) Step(p CumulativePoint) (delta float64, next CumulativeState, kind CumulativeKind) {
	seen := CumulativeState{Known: true, Value: p.Value, Start: p.Start}

	switch {
	case !s.Known:
		if p.startedWhileWatching() {
			return p.Value, seen, CumulativeFirstFull
		}
		return 0, seen, CumulativeBaseline

	case p.Start > 0 && s.Start > 0 && p.Start > s.Start:
		return p.Value, seen, CumulativeReset

	case p.Start > 0 && p.Start == s.Start && p.Value < s.Value:
		return 0, s, CumulativeOutOfOrder

	case p.Value < s.Value:
		return p.Value, seen, CumulativeReset

	default:
		return p.Value - s.Value, seen, CumulativeIncrement
	}
}
