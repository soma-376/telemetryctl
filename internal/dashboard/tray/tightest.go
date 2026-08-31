package tray

// 「가장 빠듯한 한도」 선택 (PROJ-96). vendorlimit.Result 만 보는 순수 판정이다.

import (
	"math"

	"github.com/your-org/pulsemetry/internal/vendorlimit"
)

// TightestLimit 은 「가장 빠듯한 한도」 하나다.
//
// 선택은 결정론이어야 한다 — 볼 때마다 다른 창이 강조되면 사용자는 그 표시를 믿지 않는다.
// 순서대로: available 벤더의 창만 후보 → 사용률 높은 쪽 → 초기화 빠른 쪽(모르는 창은 맨
// 뒤) → 벤더 이름 → 창 종류(5시간·주·월·미상) → Label → 입력 순서. 여기까지가 전순서다.
type TightestLimit struct {
	// Found 가 false 면 후보가 하나도 없었다는 뜻이다. 나머지 필드는 영값이다.
	Found  bool                   `json:"found"`
	Vendor string                 `json:"vendor"`
	Period vendorlimit.PeriodKind `json:"period"`
	Label  string                 `json:"label"`
	// UsedRatio 는 0.0~1.0 사용률이다. 한도를 넘겨 쓴 경우 1.0 을 넘을 수 있다.
	UsedRatio float64 `json:"used_ratio"`
	// ResetsAt 은 RFC3339 UTC 다. 모르면 빈 문자열, ResetsInSeconds 는 0 이다.
	ResetsAt        string `json:"resets_at"`
	ResetsInSeconds int64  `json:"resets_in_seconds"`
}

// TightestOf 는 available 한 벤더의 창 중 가장 빠듯한 하나를 고른다. 규칙은 TightestLimit 주석에 있다.
func TightestOf(results []vendorlimit.Result) TightestLimit {
	var (
		best  TightestLimit
		found bool
	)
	for _, res := range results {
		// unavailable 벤더는 숫자가 없다. 후보가 되면 0% 창이 뽑히거나 동률 규칙을 흔든다.
		if res.State != vendorlimit.StateAvailable {
			continue
		}
		for _, w := range res.Windows {
			cand := TightestLimit{
				Found:           true,
				Vendor:          string(res.Vendor),
				Period:          w.Period,
				Label:           w.Label,
				UsedRatio:       w.UsedRatio,
				ResetsAt:        w.ResetsAt,
				ResetsInSeconds: w.ResetsInSeconds,
			}
			if !found || tighter(cand, best) {
				best, found = cand, true
			}
		}
	}
	return best
}

// tighter 는 a 가 b 보다 빠듯한지다. 전순서라 같은 입력에 늘 같은 답을 낸다.
func tighter(a, b TightestLimit) bool {
	if a.UsedRatio != b.UsedRatio {
		return a.UsedRatio > b.UsedRatio
	}
	if ra, rb := resetKey(a), resetKey(b); ra != rb {
		return ra < rb
	}
	if a.Vendor != b.Vendor {
		return a.Vendor < b.Vendor
	}
	if pa, pb := periodRank(a.Period), periodRank(b.Period); pa != pb {
		return pa < pb
	}
	// Label 도 같으면 먼저 본 쪽을 유지한다 (입력 순서).
	return a.Label < b.Label
}

// resetKey 는 0 을 맨 뒤로 보낸다. 0 은 "모른다" 이고 "0초 뒤" 가 아니라서
// (vendorlimit.Window.ResetsInSeconds) 그대로 비교하면 정보 없는 창이 언제나 이긴다.
func resetKey(t TightestLimit) int64 {
	if t.ResetsInSeconds <= 0 {
		return math.MaxInt64
	}
	return t.ResetsInSeconds
}

// periodRank 는 창 종류의 고정 순서다. 짧은 창이 앞이다 — 먼저 다시 차기 때문이다.
func periodRank(p vendorlimit.PeriodKind) int {
	switch p {
	case vendorlimit.PeriodFiveHour:
		return 0
	case vendorlimit.PeriodWeekly:
		return 1
	case vendorlimit.PeriodMonthly:
		return 2
	default:
		return 3
	}
}
