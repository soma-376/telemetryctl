package dashboard

import (
	"math/bits"
	"sort"
)

// 점유율(share)의 계산과 반올림 규칙 (PROJ-89).
//
// # 왜 float 백분율이 아닌가
//
// 화면은 점유율을 나란히 놓는다 — "Claude Code 62% · Codex 38%". 각 값을 따로
// `v/total*100` 으로 내고 표시할 때 반올림하면 합이 99% 나 101% 로 나오는 날이 생기고,
// 사용자는 그 화면 전체를 믿지 않게 된다. 어느 값이 틀렸는지 되짚을 근거도 남지 않는다.
//
// 그래서 점유율은 **정수 천분율(permille)** 로 미리 나눠서 내보낸다. 화면은 나눗셈을
// 하지 않고 받은 정수를 그대로 쓴다(10 으로 나누면 소수 첫째 자리까지의 백분율이다).
//
// # 반올림 규칙 — 최대잔여법(largest remainder)
//
//  1. 기준 합계(base)는 **양수 값들의 합**이다. base 가 0 이하면 모든 점유율은 0 이다.
//  2. 각 값에 `floor(v × 1000 / base)` 를 준다. 내림이므로 합은 1000 이하다.
//  3. 남은 몫(1000 − 합)을 **나머지가 큰 순서로** 한 단위씩 나눠 준다.
//  4. 나머지가 같으면 **이름 오름차순**으로 가른다. 동률에서 순서가 흔들리면 같은 데이터가
//     새로고침마다 다른 점유율을 낸다.
//
// # 합계는 상수다
//
// 그래서 다음이 성립한다. 테스트가 이것을 고정한다.
//
//	base > 0  → 점유율의 합 = SharePermilleTotal (정확히 1000)
//	base ≤ 0  → 점유율의 합 = 0 (전부 0)
//
// 항목이 하나뿐이어도 그 하나가 1000 을 받는다 — 「데이터가 한 벤더에만 있어도 화면 계약이
// 유지된다」 가 이 성질에 기댄다.

// SharePermilleTotal 은 점유율 합계의 고정값이다. 기준 합계가 0 보다 클 때 항목들의
// 점유율은 정확히 이 값을 이룬다.
const SharePermilleTotal = 1000

// sharePermille 은 values 의 점유율을 정수 천분율로 나눈다.
//
// names 는 동률을 가르는 이름이고 values 와 길이가 같아야 한다 — 길이가 다르면 이름을
// 쓰지 않고 입력 순서로만 가른다. 음수 값은 0 으로 본다. 벤더가 음수 토큰을 보고할
// 이유는 없지만, 그대로 두면 다른 항목의 점유율까지 깎여 합이 1000 을 넘는다.
func sharePermille(values []int64, names []string) []int {
	out := make([]int, len(values))
	var base int64
	for _, v := range values {
		if v > 0 {
			base += v
		}
	}
	if base <= 0 {
		// 나눌 것이 없다. 전부 0 이고 합도 0 이다 — 1000 을 임의로 배분하면 화면이
		// "아무 활동도 없는 날의 100%" 를 그린다.
		return out
	}

	// rest 는 내림에서 잘려 나간 나머지다. 남은 몫을 이 순서로 나눠 준다.
	type rest struct {
		index     int
		remainder uint64
	}
	rests := make([]rest, 0, len(values))
	left := SharePermilleTotal
	for i, v := range values {
		if v <= 0 {
			continue
		}
		q, r := mulDiv(uint64(v), SharePermilleTotal, uint64(base))
		out[i] = int(q)
		left -= int(q)
		rests = append(rests, rest{index: i, remainder: r})
	}

	sort.SliceStable(rests, func(a, b int) bool {
		if rests[a].remainder != rests[b].remainder {
			return rests[a].remainder > rests[b].remainder
		}
		return shareName(names, rests[a].index) < shareName(names, rests[b].index)
	})
	// left 는 항상 양수 항목 수보다 작다 — 내림 하나가 잃는 양이 1 미만이기 때문이다.
	// 그래도 길이로 한 번 더 막는다. 여기서 넘치면 합이 1000 을 넘는다.
	for k := 0; k < left && k < len(rests); k++ {
		out[rests[k].index]++
	}
	return out
}

// shareName 은 동률 판정에 쓸 이름이다. 이름 목록이 짧으면 빈 문자열이라 입력 순서가
// 그대로 남는다 (sort.SliceStable).
func shareName(names []string, i int) string {
	if i < 0 || i >= len(names) {
		return ""
	}
	return names[i]
}

// mulDiv 는 v × mul 의 몫과 나머지를 div 로 나눈 값이다.
//
// 중간 곱을 128비트로 두는 이유는 v 가 nano-USD 이기 때문이다. 1000 을 곱하면 int64 가
// 넘칠 수 있는 크기(약 92 억 nano-USD 위)가 이론상 존재하고, 넘치면 점유율이 음수가 되어
// 합이 1000 이 아니게 된다. bits 는 표준 라이브러리라 의존성이 늘지 않는다.
//
// v > div 는 호출자 계약 위반이다(v 는 base 의 일부다). 그래도 눌러 두는 이유는
// bits.Div64 가 몫이 64비트를 넘으면 패닉하기 때문이다 — 조회 하나가 앱을 죽이면 안 된다.
func mulDiv(v, mul, div uint64) (quotient, remainder uint64) {
	if div == 0 {
		return 0, 0
	}
	if v > div {
		v = div
	}
	hi, lo := bits.Mul64(v, mul)
	return bits.Div64(hi, lo, div)
}
