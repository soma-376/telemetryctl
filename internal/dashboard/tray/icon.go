package tray

import (
	"math"

	"github.com/your-org/pulsemetry/internal/vendorlimit"
)

// IconKind 는 트레이 아이콘의 그림 종류다. GUI 가 이 값으로 임베드한 PNG 를 고른다.
type IconKind string

const (
	// IconRing 은 한도 사용률을 링 게이지로 보여준다.
	IconRing IconKind = "ring"
	// IconAlert 는 어느 한도든 경고선(AlertPercent)을 넘었다는 뜻이다.
	IconAlert IconKind = "alert"
	// IconOffline 은 데몬에 닿지 않는다는 뜻이다. 사용률을 모르므로 숫자를 보이지 않는다.
	IconOffline IconKind = "offline"
)

// AlertPercent 이상이면 링 대신 경고 아이콘을 쓴다.
const AlertPercent = 90

// Icon 은 트레이에 걸 그림 한 장이다. 값 비교로 교체 여부를 판단할 수 있게 비교 가능한 구조체로 둔다.
type Icon struct {
	Kind IconKind
	// Percent 는 정수로 반올림한 0~100 의 사용률이다. IconOffline 이면 0 이다.
	// GUI 는 이 값이 바뀔 때만 아이콘을 다시 그린다.
	Percent int
}

// IconFor 는 데몬 도달 여부와 벤더 한도로 트레이 아이콘을 정한다.
//
// 표시하는 값은 연결된 벤더들의 모든 한도 창 가운데 **가장 많이 쓴 것**이다. 한 창이라도
// 바닥나면 그 벤더는 못 쓰므로, 평균은 사용자가 막히기 직전까지 안전해 보이게 만든다.
// 읽어 오지 못한 벤더(unavailable)는 0 이 아니라 판단에서 뺀다 — 모르는 것을 여유로 세지 않는다.
func IconFor(daemonReachable bool, limits []vendorlimit.Result) Icon {
	if !daemonReachable {
		return Icon{Kind: IconOffline}
	}
	peak := 0.0
	for _, r := range limits {
		if r.State != vendorlimit.StateAvailable {
			continue
		}
		for _, w := range r.Windows {
			peak = max(peak, w.UsedRatio)
		}
	}
	// 한도를 넘겨 쓰면 1.0 을 넘을 수 있다. 게이지는 가득 찬 것 이상을 그리지 않는다.
	percent := min(peak*100, 100)
	rounded := int(math.Round(percent))
	if percent >= AlertPercent {
		return Icon{Kind: IconAlert, Percent: rounded}
	}
	return Icon{Kind: IconRing, Percent: rounded}
}
