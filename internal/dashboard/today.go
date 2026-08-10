package dashboard

import (
	"context"
	"math"
)

// TodaySummary 는 Today 화면 한 장이다 (계획서 「화면 → 쿼리 대응」의 "Today 4개 카드 +
// 어제 대비 %" 와 상단 "3 agents active").
type TodaySummary struct {
	// TZ 는 실제로 적용된 시간대 이름이다. 빈 문자열을 넘기면 "UTC" 가 돌아온다 —
	// 화면이 무엇을 기준으로 계산됐는지 되돌려 확인할 수 있어야 한다.
	TZ string `json:"tz"`
	// Date 는 그 시간대의 오늘 날짜(YYYY-MM-DD)다.
	Date string `json:"date"`

	// StartAt·EndAt 은 오늘 구간의 UTC unix 초 [시작, 끝) 이다.
	StartAt int64 `json:"start_at"`
	EndAt   int64 `json:"end_at"`
	// YesterdayStartAt 은 어제 구간의 시작이다. 끝은 StartAt 과 같다.
	YesterdayStartAt int64 `json:"yesterday_start_at"`

	Today     Totals `json:"today"`
	Yesterday Totals `json:"yesterday"`

	// Cards 는 화면 상단 4개 카드다. 각 카드가 오늘 값·어제 값·증감률을 함께 들고 있다.
	// 다른 지표가 필요하면 Today·Yesterday 에서 직접 꺼내 계산하면 된다.
	Cards []Card `json:"cards"`

	// ActiveAgents 는 지금 돌고 있는 세션의 벤더 목록이다. 길이가 상단 바의 "3 agents active".
	ActiveAgents []string `json:"active_agents"`
	// ActiveSessions 는 running 세션 수다. 한 벤더가 여러 세션을 돌릴 수 있어 위와 다르다.
	ActiveSessions int64 `json:"active_sessions"`
}

// 카드 지표 이름. 값 문자열이 프런트엔드와의 계약이라 바꾸면 화면이 카드를 못 찾는다.
const (
	MetricCostUSD       = "cost_usd"
	MetricTokens        = "tokens"
	MetricSessions      = "sessions_started"
	MetricActiveSeconds = "active_seconds"
)

// Card 는 "오늘 값 + 어제 대비 %" 한 장이다.
type Card struct {
	Metric    string  `json:"metric"`
	Today     float64 `json:"today"`
	Yesterday float64 `json:"yesterday"`
	// DeltaPercent 는 (오늘-어제)/어제 × 100 을 소수 첫째 자리로 반올림한 값이다.
	DeltaPercent float64 `json:"delta_percent"`
	// HasBaseline 이 false 면 어제 값이 0 이라 증감률이 정의되지 않는다는 뜻이다.
	// 이때 DeltaPercent 는 0 이고, 화면은 "+∞%" 대신 "신규" 같은 표시를 골라야 한다.
	// 0 으로 나눈 결과를 그대로 내보내면 JSON 이 Inf 를 직렬화하지 못해 조회 전체가 실패한다.
	HasBaseline bool `json:"has_baseline"`
}

// Today 는 tz 기준 오늘의 요약을 어제 대비와 함께 돌려준다.
//
// rollup_hourly 를 오늘·어제 두 구간으로 두 번 조회한다 (계획서 「화면 → 쿼리 대응」).
// 구간 경계는 UTC 자정이 아니라 **tz 의 자정** 이다 — timezone.go 참고.
//
// tz 가 잘못되면 에러다. DB 가 없으면 에러가 아니라 0 으로 채운 요약이다.
func (r *Reader) Today(ctx context.Context, tz string) (TodaySummary, error) {
	loc, err := loadLocation(tz)
	if err != nil {
		return TodaySummary{}, err
	}
	now := r.now()
	today := dayOf(now, loc)
	yesterday := previousDay(today, loc)

	sum := TodaySummary{
		TZ:               loc.String(),
		Date:             today.Start.Format(dateKey),
		StartAt:          today.StartSec(),
		EndAt:            today.EndSec(),
		YesterdayStartAt: yesterday.StartSec(),
		ActiveAgents:     []string{},
	}

	db, ok := r.db()
	if !ok {
		// 미설치. 카드 모양은 유지해야 화면이 분기 없이 그린다.
		sum.Cards = buildCards(sum.Today, sum.Yesterday)
		return sum, nil
	}

	if sum.Today, err = r.totalsIn(ctx, db, today); err != nil {
		return TodaySummary{}, err
	}
	if sum.Yesterday, err = r.totalsIn(ctx, db, yesterday); err != nil {
		return TodaySummary{}, err
	}
	sum.Cards = buildCards(sum.Today, sum.Yesterday)

	if sum.ActiveAgents, sum.ActiveSessions, err = activeAgents(ctx, db); err != nil {
		return TodaySummary{}, err
	}
	return sum, nil
}

const totalsInRangeSQL = `SELECT ` + rollupSumColumns + `
FROM rollup_hourly
WHERE dim = 'total' AND "key" = '' AND hour >= ? AND hour < ?`

// totalsIn 은 dim='total' 행의 구간 합계다.
func (r *Reader) totalsIn(ctx context.Context, db sqlQuerier, tr timeRange) (Totals, error) {
	var t Totals
	row := db.QueryRowContext(ctx, totalsInRangeSQL, tr.StartSec(), tr.EndSec())
	if err := row.Scan(totalsDest(&t)...); err != nil {
		return Totals{}, queryErr("오늘 요약 조회", err)
	}
	return t, nil
}

// activeAgents 는 상단 바의 "3 agents active" 다 — status='running' 세션의 distinct vendor.
//
// 판정을 sessions.status 에만 맡긴다 (계획서 지정). 데몬이 죽은 뒤에는 마지막 running 세션이
// 그대로 남으므로, 화면이 "언제 기준인가" 를 보이려면 Status 의 데몬 생존 정보를 함께 쓴다.
const activeAgentsSQL = `SELECT vendor, COUNT(*)
FROM sessions WHERE status = 'running' GROUP BY vendor ORDER BY vendor`

func activeAgents(ctx context.Context, db sqlQuerier) (vendors []string, sessions int64, err error) {
	const op = "실행 중 세션 조회"
	rows, err := db.QueryContext(ctx, activeAgentsSQL)
	if err != nil {
		return nil, 0, queryErr(op, err)
	}
	defer closeRows(rows, op, &err)

	vendors = []string{}
	for rows.Next() {
		var (
			vendor string
			n      int64
		)
		if err := rows.Scan(&vendor, &n); err != nil {
			return nil, 0, queryErr(op, err)
		}
		vendors = append(vendors, vendor)
		sessions += n
	}
	return vendors, sessions, nil
}

// buildCards 는 계획서가 말한 "Today 4개 카드" 를 만든다.
func buildCards(today, yesterday Totals) []Card {
	return []Card{
		newCard(MetricCostUSD, today.CostUSD, yesterday.CostUSD),
		newCard(MetricTokens, float64(today.Tokens()), float64(yesterday.Tokens())),
		newCard(MetricSessions, float64(today.SessionsStarted), float64(yesterday.SessionsStarted)),
		newCard(MetricActiveSeconds, today.ActiveSeconds, yesterday.ActiveSeconds),
	}
}

// newCard 는 증감률을 계산한다.
//
// 어제가 0 인 경우가 실제로 흔하다 — 설치 첫날, 주말 다음 월요일, 새 프로젝트. 그대로
// 나누면 +Inf 이고 encoding/json 은 Inf 를 직렬화하지 못해 조회 전체가 에러로 바뀐다.
// 그래서 계산하지 않고 HasBaseline=false 로 넘겨 화면이 표현을 고르게 한다.
func newCard(metric string, today, yesterday float64) Card {
	c := Card{Metric: metric, Today: today, Yesterday: yesterday}
	if yesterday == 0 {
		return c
	}
	c.HasBaseline = true
	c.DeltaPercent = round1((today - yesterday) / yesterday * 100)
	return c
}

// round1 은 소수 첫째 자리 반올림이다. 33.333333333333336 같은 값이 그대로 화면에 흘러가면
// 프런트엔드마다 다르게 자른다.
func round1(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return math.Round(v*10) / 10
}
