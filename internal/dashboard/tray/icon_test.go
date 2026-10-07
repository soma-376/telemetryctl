package tray

import (
	"math"
	"testing"

	"github.com/your-org/pulsemetry/internal/vendorlimit"
)

func limitResult(state vendorlimit.State, ratios ...float64) vendorlimit.Result {
	windows := make([]vendorlimit.Window, 0, len(ratios))
	for _, r := range ratios {
		windows = append(windows, vendorlimit.Window{UsedRatio: r})
	}
	return vendorlimit.Result{State: state, Windows: windows}
}

func TestIconFor(t *testing.T) {
	available := vendorlimit.StateAvailable
	cases := []struct {
		name      string
		reachable bool
		limits    []vendorlimit.Result
		want      Icon
	}{
		// 데몬이 없으면 마지막 숫자를 믿을 수 없다. 사용률이 높아도 오프라인이 먼저다.
		{"데몬 다운은 사용률보다 우선", false, []vendorlimit.Result{limitResult(available, 0.99)}, Icon{Kind: IconOffline}},
		{"한도 정보 없음", true, nil, Icon{Kind: IconUnknown}},
		{"한도 창 없음", true, []vendorlimit.Result{limitResult(available)}, Icon{Kind: IconUnknown}},
		{"모두 조회 실패", true, []vendorlimit.Result{limitResult(vendorlimit.StateUnavailable, 0.95)}, Icon{Kind: IconUnknown}},
		{"유효한 숫자 없음", true, []vendorlimit.Result{limitResult(available, math.NaN(), math.Inf(1))}, Icon{Kind: IconUnknown}},
		{"사용 전에는 가득 찬 링", true, []vendorlimit.Result{limitResult(available, 0)}, Icon{Kind: IconRing, Percent: 100}},
		{"6퍼센트 사용하면 94퍼센트 남음", true, []vendorlimit.Result{limitResult(available, 0.06)}, Icon{Kind: IconRing, Percent: 94}},
		{"벤더·창을 통틀어 가장 적게 남은 값", true, []vendorlimit.Result{
			limitResult(available, 0.12, 0.41),
			limitResult(available, 0.62),
		}, Icon{Kind: IconRing, Percent: 38}},
		{"남은 비율을 반올림", true, []vendorlimit.Result{limitResult(available, 0.625)}, Icon{Kind: IconRing, Percent: 38}},
		// 못 읽은 벤더의 옛 숫자가 섞이면 안 된다.
		{"unavailable 벤더는 뺀다", true, []vendorlimit.Result{
			limitResult(vendorlimit.StateUnavailable, 0.95),
			limitResult(available, 0.3),
		}, Icon{Kind: IconRing, Percent: 70}},
		// 89.6 은 반올림하면 90 이지만 경고 판정은 반올림 전 값으로 한다.
		{"경고선 직전", true, []vendorlimit.Result{limitResult(available, 0.896)}, Icon{Kind: IconRing, Percent: 10}},
		{"경고선", true, []vendorlimit.Result{limitResult(available, 0.90)}, Icon{Kind: IconAlert, Percent: 10}},
		{"소진", true, []vendorlimit.Result{limitResult(available, 1)}, Icon{Kind: IconAlert, Percent: 0}},
		{"초과 사용은 남음 0 에서 멈춘다", true, []vendorlimit.Result{limitResult(available, 1.4)}, Icon{Kind: IconAlert, Percent: 0}},
		{"음수 사용은 남음 100 에서 멈춘다", true, []vendorlimit.Result{limitResult(available, -0.1)}, Icon{Kind: IconRing, Percent: 100}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IconFor(tc.reachable, tc.limits); got != tc.want {
				t.Fatalf("IconFor = %+v, want %+v", got, tc.want)
			}
		})
	}
}
