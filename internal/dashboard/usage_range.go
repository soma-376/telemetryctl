package dashboard

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// ReadDashboardUsageBreakdown은 Home GUI의 세션 건수만 표시 자격으로 센다.
// 다른 지표와 활동시간은 ReadUsageBreakdown의 전체 저장 데이터 그대로다.
func ReadDashboardUsageBreakdown(ctx context.Context, db SQLQuerier, boundaries []time.Time) (HomeBreakdown, error) {
	out, err := ReadUsageBreakdown(ctx, db, boundaries)
	if err != nil || db == nil {
		return out, err
	}
	if err := replaceDashboardSessionCounts(ctx, db, &out); err != nil {
		return HomeBreakdown{}, err
	}
	return out, nil
}

// 원시 사용량 집계에서 세션 수만 바꾼다. 같은 Home 스냅샷 안에서 실행되므로
// 보강 commit이 두 조회 사이에 와도 사용량과 목록의 분류 시점이 섞이지 않는다.
func replaceDashboardSessionCounts(ctx context.Context, db SQLQuerier, out *HomeBreakdown) (err error) {
	out.Totals.SessionsStarted = 0
	vendorIndex := make(map[string]int, len(out.Vendors))
	for i := range out.Vendors {
		out.Vendors[i].SessionsStarted = 0
		vendorIndex[out.Vendors[i].Vendor] = i
	}
	for i := range out.Windows {
		out.Windows[i].SessionsStarted = 0
		for j := range out.Windows[i].Vendors {
			out.Windows[i].Vendors[j].SessionsStarted = 0
		}
	}
	const op = "Home 표시 세션 수 조회"
	// 기존 사용량 창과 같은 UTC 정시 버킷을 사용해야 다른 벤더의 창별 세션 수가 유지된다.
	query := `SELECT s.vendor_id, s.started_at / 3600 * 3600, COUNT(*)
FROM sessions s WHERE s.started_at >= ? AND s.started_at < ?
  AND (` + DashboardSessionEligibleSQL + `)
GROUP BY s.vendor_id, 2`
	rows, err := db.QueryContext(ctx, query, out.StartAt, out.EndAt)
	if err != nil {
		return QueryErr(op, err)
	}
	defer CloseRows(rows, op, &err)
	for rows.Next() {
		var vendor string
		var hour, count int64
		if scanErr := rows.Scan(&vendor, &hour, &count); scanErr != nil {
			return QueryErr(op, scanErr)
		}
		// 원시 세션 fact가 같은 기간의 모든 벤더를 먼저 만들어 둔다.
		vi, ok := vendorIndex[vendor]
		if !ok {
			return fmt.Errorf("dashboard: Home 표시 세션의 벤더 %q가 원시 집계에 없음", vendor)
		}
		wi := sort.Search(len(out.Windows), func(i int) bool { return out.Windows[i].EndAt > hour })
		if wi == len(out.Windows) {
			wi--
		}
		out.Totals.SessionsStarted += count
		out.Vendors[vi].SessionsStarted += count
		out.Windows[wi].SessionsStarted += count
		out.Windows[wi].Vendors[vi].SessionsStarted += count
	}
	return nil
}

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

// ReadDashboardRecentSessions는 Home GUI의 표시 자격을 목록 제한 전에 적용한다.
// ReadRecentSessions와 트레이의 원시 조회는 그대로 둔다.
func ReadDashboardRecentSessions(ctx context.Context, db SQLQuerier, start, end time.Time, limit int) ([]RecentSession, bool, error) {
	if db == nil {
		return []RecentSession{}, false, nil
	}
	return recentSessionsWithQuery(ctx, db, timeRange{Start: start, End: end}, limit, false, true)
}

// ReadActiveAgents는 선택 기간과 무관하게 지금 진행 중인 벤더·세션을 읽는다.
func ReadActiveAgents(ctx context.Context, db SQLQuerier) ([]string, int64, error) {
	if db == nil {
		return []string{}, 0, nil
	}
	return activeAgents(ctx, db)
}
