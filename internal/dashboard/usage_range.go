package dashboard

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// ReadUsageBreakdown은 화면이 정한 연속 구간을 기존 집계·비용 산정기로 읽는다.
// 첫 경계와 마지막 경계가 조회 범위이며, 모든 경계는 엄격한 오름차순이어야 한다.
// 날짜별 반복 조회 없이 승격 집계와 LLM 호출 스캔을 각각 한 번 수행한다.
func ReadUsageBreakdown(ctx context.Context, db SQLQuerier, boundaries []time.Time) (HomeBreakdown, error) {
	if len(boundaries) < 2 {
		return HomeBreakdown{}, fmt.Errorf("dashboard: 사용량 조회 경계가 부족하다")
	}
	for i := 1; i < len(boundaries); i++ {
		if !boundaries[i].After(boundaries[i-1]) {
			return HomeBreakdown{}, fmt.Errorf("dashboard: 사용량 조회 경계가 오름차순이 아니다")
		}
	}
	tr := timeRange{Start: boundaries[0], End: boundaries[len(boundaries)-1]}
	out := HomeBreakdown{
		TZ: tr.Start.Location().String(), Date: tr.Start.Format(dateKey),
		StartAt: tr.StartSec(), EndAt: tr.EndSec(),
		Windows: make([]UsageWindow, len(boundaries)-1), Vendors: []VendorUsage{},
		Cost: newCostAccumulator().summary(), Peak: PeakWindow{Index: -1, Cost: nanoMoney(0)},
	}
	for i := range out.Windows {
		out.Windows[i] = UsageWindow{StartAt: boundaries[i].Unix(), EndAt: boundaries[i+1].Unix(),
			LocalHour: boundaries[i].Hour(), Cost: nanoMoney(0), Vendors: []VendorWindow{}}
	}
	if db == nil {
		return out, nil
	}
	a := newUsageAcc(tr, len(out.Windows))
	a.indexWindow = func(sec int64) int {
		// 정시 집계가 현지 자정보다 앞서면 첫 창에 귀속한다. 일별 조회와 같은 규칙이다.
		i := sort.Search(len(out.Windows), func(i int) bool { return out.Windows[i].EndAt > sec })
		if i == len(out.Windows) {
			i--
		}
		return i
	}
	if err := a.collectAggregate(ctx, db); err != nil {
		return HomeBreakdown{}, err
	}
	if err := a.collectCalls(ctx, db); err != nil {
		return HomeBreakdown{}, err
	}
	a.apply(&out, defaultTopModels)
	return out, nil
}

// ReadRecentSessions는 선택 기간에 시작한 세션의 생애 전체 사용량을 읽는다.
func ReadRecentSessions(ctx context.Context, db SQLQuerier, start, end time.Time, limit int) ([]RecentSession, bool, error) {
	if db == nil {
		return []RecentSession{}, false, nil
	}
	return recentSessions(ctx, db, timeRange{Start: start, End: end}, limit, false)
}

// ReadActiveAgents는 선택 기간과 무관하게 지금 진행 중인 벤더·세션을 읽는다.
func ReadActiveAgents(ctx context.Context, db SQLQuerier) ([]string, int64, error) {
	if db == nil {
		return []string{}, 0, nil
	}
	return activeAgents(ctx, db)
}
