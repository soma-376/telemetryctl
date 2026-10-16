package tray

import (
	"math"

	"github.com/your-org/pulsemetry/internal/vendorlimit"
)

// IconKind 는 GUI 가 런타임에 그릴 트레이 아이콘의 종류다.
type IconKind string

const (
	// IconRing 은 남은 한도를 링 게이지로 보여준다.
	IconRing IconKind = "ring"
	// IconUnknown 은 조회 가능한 한도 정보가 없다는 뜻이다.
	IconUnknown IconKind = "unknown"
	// IconAlert 는 어느 한도든 경고선(AlertPercent)을 넘었다는 뜻이다.
	IconAlert IconKind = "alert"
	// IconOffline 은 데몬에 닿지 않는다는 뜻이다. 사용률을 모르므로 숫자를 보이지 않는다.
	IconOffline IconKind = "offline"
)

// 사용률이 AlertPercent 이상이면 남은 한도 링에 경고 표시를 더한다.
const AlertPercent = 90

// Icon 은 트레이에 걸 그림 한 장이다. 값 비교로 교체 여부를 판단할 수 있게 비교 가능한 구조체로 둔다.
type Icon struct {
	Kind IconKind
	// Percent 는 정수로 반올림한 0~100 의 남은 비율이다. Offline·Unknown 이면 0 이다.
	// GUI 는 이 값이 바뀔 때만 아이콘을 다시 그린다.
	Percent int
}

// IconFor 는 데몬 도달 여부와 벤더 한도로 트레이 아이콘을 정한다.
//
// 표시하는 값은 조회 가능한 벤더들의 모든 한도 창 가운데 **가장 적게 남은 것**이다. 한 창이라도
// 바닥나면 그 벤더는 못 쓰므로, 평균은 사용자가 막히기 직전까지 안전해 보이게 만든다.
// 읽어 오지 못한 벤더(unavailable)는 0 이 아니라 판단에서 뺀다 — 모르는 것을 여유로 세지 않는다.
func IconFor(daemonReachable bool, limits []vendorlimit.Result) Icon {
	if !daemonReachable {
		return Icon{Kind: IconOffline}
	}
	peak := 0.0
	found := false
	for _, r := range limits {
		if r.State != vendorlimit.StateAvailable {
			continue
		}
		for _, w := range r.Windows {
			if math.IsNaN(w.UsedRatio) || math.IsInf(w.UsedRatio, 0) {
				continue
			}
			found = true
			peak = max(peak, w.UsedRatio)
		}
	}
	if !found {
		return Icon{Kind: IconUnknown}
	}
	// 퀵뷰와 같은 남은 비율을 사용한다. 초과 사용은 남음 0% 로 표시한다.
	usedPercent := min(peak*100, 100)
	remaining := int(math.Round((1 - min(peak, 1)) * 100))
	if usedPercent >= AlertPercent {
		return Icon{Kind: IconAlert, Percent: remaining}
	}
	return Icon{Kind: IconRing, Percent: remaining}
}
