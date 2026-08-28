package dashboard

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/your-org/pulsemetry/internal/pricing"
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
// 승격 테이블을 오늘·어제 두 구간으로 집계한다 (aggregate.go). 구간 경계는 UTC 자정이
// 아니라 **tz 의 자정** 이다 — timezone.go 참고.
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

// totalsIn 은 구간 전체의 합계다. Breakdown(dim=total) 과 같은 집계기를 쓰므로
// GUI 의 Today 카드와 CLI 의 `stats` 합계가 같은 숫자를 낸다.
func (r *Reader) totalsIn(ctx context.Context, db sqlQuerier, tr timeRange) (Totals, error) {
	return sumAggregate(ctx, db, DimTotal, "", tr)
}

// activeAgents 는 상단 바의 "3 agents active" 다 — 진행 중 세션의 distinct vendor.
//
// v3 에는 sessions.status 컬럼이 없다. 진행 중 판정은 `ended_at IS NULL` 하나이고,
// abandoned·handoff 는 산출하지 않는다 (ADR 0009).
//
// 데몬이 죽은 뒤에는 마지막 진행 중 세션이 그대로 남으므로, 화면이 "언제 기준인가" 를
// 보이려면 Status 의 데몬 생존 정보를 함께 쓴다.
const activeAgentsSQL = `SELECT vendor_id, COUNT(*)
FROM sessions WHERE ended_at IS NULL GROUP BY vendor_id ORDER BY vendor_id`

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

// ── Home 화면 (PROJ-88) ─────────────────────────────────────────────────────

// Home 은 「선택 날짜」를 받는 요약이다. Today 는 "지금이 속한 하루" 밖에 물을 수 없어
// 어제·지난주를 되짚는 화면을 만들 수 없었다.
//
// Today 를 지우지 않는 이유는 GUI TypeScript 바인딩과 `stats --json` 이 이미 그 모양을
// 읽고 있기 때문이다 (Totals 의 빈 필드를 남긴 것과 같은 이유). 두 메서드는 같은 집계기와
// 같은 시간대 계산을 쓰므로 같은 날짜에 대해 같은 숫자를 낸다.
//
// # 합계의 정의 — 화면이 두 숫자를 나란히 놓기 전에 읽어야 한다
//
// Home 카드와 최근 세션 목록은 **자르는 기준이 다르다.** 같아 보이는 두 숫자가 어긋나는
// 것은 버그가 아니라 아래 정의의 결과다.
//
//	카드(HomeSummary.Totals·Cost·TwoHour)
//	  = 사실이 일어난 시각이 선택 날짜 구간 [StartAt, EndAt) 에 든 것만의 합.
//	    비용·토큰·API 호출 수는 llm_calls.called_at, 도구·파일은 tool_calls.called_at,
//	    프롬프트는 turns.started_at 이 기준이다.
//	    단 sessions_started·active_seconds 만은 세션이 **시작한** 날에 통째로 귀속된다 —
//	    활동 시간은 세션 전체의 값이라 시간으로 쪼갤 수 없기 때문이다 (aggregate.go).
//
//	최근 세션(HomeSummary.Recent)
//	  = sessions.started_at 이 같은 구간에 든 세션. 각 행의 수치는 그 세션의
//	    **생애 전체 합계**이지 선택 날짜로 자른 값이 아니다. Activity 화면·세션 상세와
//	    같은 값을 보여야 하기 때문이다.
//
// 따라서 다음이 성립한다.
//
//   - 자정을 넘겨 이어진 세션이 없고 목록이 잘리지 않았으면(RecentTruncated=false)
//     **최근 세션 행들의 토큰·비용 합 = 카드 값** 이다.
//   - 자정을 넘긴 세션이 있으면 그 세션의 다음 날 몫이 행에는 들어 있고 카드에는 없다.
//     이때 행 합계가 카드보다 **크다.**
//   - RecentLimit 로 잘린 목록의 합은 카드보다 **작다.** RecentTruncated 가 그 사실을 알린다.
//
// # 비용은 aggregate 의 SUM 이 아니다
//
// Totals.CostUSD 는 `SUM(llm_calls.cost_usd)`, 즉 **벤더가 보고한 비용만**의 합이다.
// HomeSummary.Cost 는 보고값이 없는 호출을 가격표 단가로 메운 「예상 비용」이라 항상
// 그보다 크거나 같다 (today_scan.go · internal/pricing). 카드에 나가는 것은 후자다.

// HomeQuery 는 Home 화면 한 장의 조회 조건이다.
type HomeQuery struct {
	// TZ 는 하루의 경계를 정하는 시간대다. 빈 문자열은 UTC, 잘못된 이름은 에러다.
	TZ string `json:"tz"`
	// Date 는 그 시간대의 날짜(YYYY-MM-DD)다. 비어 있으면 오늘이다.
	// 형식이 틀리면 에러다 — 조용히 오늘로 떨어지면 사용자는 어제를 보고 있다고 믿는다.
	Date string `json:"date"`
	// RecentLimit 는 최근 세션 목록의 길이 상한이다. 0 이하는 기본값, 상한 초과는 상한이다.
	RecentLimit int `json:"recent_limit"`
}

// HomeSummary 는 Home 화면 한 장이다.
type HomeSummary struct {
	// TZ 는 실제로 적용된 시간대 이름, Date 는 그 시간대의 선택 날짜다.
	TZ   string `json:"tz"`
	Date string `json:"date"`
	// IsToday 는 선택 날짜가 지금 기준 오늘인지다. false 면 ActiveAgents 는 그 날짜가
	// 아니라 **지금** 진행 중인 세션을 가리킨다는 뜻이므로 화면이 표기를 바꿔야 한다.
	IsToday bool `json:"is_today"`

	// StartAt·EndAt 은 선택 날짜 구간의 UTC unix 초 [시작, 끝) 이다. DST 전환일에는
	// 그 차이가 86400 이 아니다.
	StartAt int64 `json:"start_at"`
	EndAt   int64 `json:"end_at"`
	// PreviousStartAt 은 전날 구간의 시작이다. 끝은 StartAt 과 같다.
	PreviousStartAt int64 `json:"previous_start_at"`

	// Totals·PreviousTotals 는 두 날의 집계다. Cards 에 없는 지표를 화면이 직접 꺼내 쓴다.
	Totals         Totals `json:"totals"`
	PreviousTotals Totals `json:"previous_totals"`

	// Cost 는 선택 날짜의 예상 비용이다 (가격표 기준, 보고값 우선).
	Cost CostSummary `json:"cost"`
	// TwoHour 는 2시간 창 평균이다.
	TwoHour TwoHourAverage `json:"two_hour"`

	// Cards 는 화면 상단 4개 카드다 — 토큰 · 예상 비용 · AI 활동 시간 · 2시간 평균.
	Cards []Card `json:"cards"`

	// ActiveAgents·ActiveSessions 는 **지금** 진행 중인(ended_at IS NULL) 세션의 벤더와
	// 세션 수다. 선택 날짜와 무관하다 — "지금 몇 개가 돌고 있나" 는 날짜로 되돌릴 수 없다.
	ActiveAgents   []string `json:"active_agents"`
	ActiveSessions int64    `json:"active_sessions"`

	// Recent 는 선택 날짜에 시작한 세션의 요약이다. 최신순이다.
	Recent []RecentSession `json:"recent_sessions"`
	// RecentTruncated 가 true 면 목록이 RecentLimit 에서 잘렸다는 뜻이다. 이때 행 합계는
	// 카드보다 작다 (위 「합계의 정의」).
	RecentTruncated bool `json:"recent_truncated"`
}

// 카드 지표 이름. 값 문자열이 프런트엔드와의 계약이다.
const (
	// MetricEstimatedCostUSD 는 가격표로 메운 예상 비용이다. MetricCostUSD(보고값 합)와
	// 이름을 나눈 이유는 두 값이 다르기 때문이다 — 같은 이름을 쓰면 화면이 어느 쪽을 그리는지
	// 구분하지 못한다.
	MetricEstimatedCostUSD = "estimated_cost_usd"
	// MetricTwoHourActiveSeconds 는 활동이 있었던 2시간 창의 평균 AI 활동 시간(초)이다.
	MetricTwoHourActiveSeconds = "two_hour_active_seconds"
)

// twoHourSeconds 는 「2시간 평균」 창 하나의 길이(초)다.
const twoHourSeconds int64 = 2 * 60 * 60

const (
	defaultRecentSessions = 10
	maxRecentSessions     = 100
)

// TwoHourWindow 는 현지 자정부터 2시간씩 자른 창 하나다.
type TwoHourWindow struct {
	// StartAt·EndAt 은 이 창의 UTC unix 초 [시작, 끝) 이다. DST 전환일의 마지막 창은
	// 2시간보다 짧거나 길 수 있다 — 하루가 23·25시간이기 때문이다.
	StartAt int64 `json:"start_at"`
	EndAt   int64 `json:"end_at"`
	// LocalHour 는 이 창이 시작하는 현지 시각(0~23)이다. 화면의 축 라벨이 쓴다.
	LocalHour int `json:"local_hour"`

	// Active 는 이 창에 활동이 하나라도 있었는지다. **평균의 분모가 되는 것은 이 창들뿐**이다 —
	// 자는 시간까지 분모에 넣으면 평균이 늘 바닥에 붙어 아무것도 구분하지 못한다.
	Active bool `json:"active"`

	// Tokens 는 입력+출력이다. 캐시 토큰은 더하지 않는다 (Totals.Tokens).
	Tokens        int64         `json:"tokens"`
	ActiveSeconds float64       `json:"active_seconds"`
	Cost          pricing.Money `json:"cost"`
}

// TwoHourAverage 는 「2시간 평균」 카드의 근거다.
//
// # 평균의 정의
//
// 하루를 현지 자정부터 2시간씩 자르고, **활동이 있었던 창만** 평균낸다. 분모는
// ActiveWindows 이고 0 이면 평균은 전부 0 이다. 창 전부를 그대로 내보내는 이유는 화면이
// 다른 분모(예: 24시간 전체)를 고르고 싶을 때 다시 조회하지 않아도 되게 하기 위해서다.
//
// 창들의 합은 그 날 카드의 합과 같다 — 구간 밖의 사실은 애초에 집계되지 않고, 구간 안의
// 사실은 반드시 어느 창 하나에 귀속된다.
type TwoHourAverage struct {
	Windows []TwoHourWindow `json:"windows"`
	// ActiveWindows 는 평균의 분모다.
	ActiveWindows int `json:"active_windows"`

	// Tokens·ActiveSeconds 는 활동 창 하나당 평균이다.
	Tokens        float64 `json:"tokens"`
	ActiveSeconds float64 `json:"active_seconds"`
	// Cost 는 활동 창 하나당 평균 예상 비용이다.
	Cost pricing.Money `json:"cost"`
}

// RecentSession 은 최근 활동 목록의 한 줄이다 — 화면 표시용 요약이다.
//
// SessionRow 를 그대로 쓰지 않는 이유는 두 가지다. 목록이 쓰지 않는 20여 개 필드를 매 행
// 상관 서브쿼리로 다시 세지 않아도 되고, 비용을 가격표 기준으로 낼 수 있다 —
// SessionRow.CostUSD 는 보고값 합이다. 자세한 지표는 Session(id) 가 준다.
type RecentSession struct {
	// ID 는 sessions.id 다. Session(id) 의 인자이자 화면 이동의 키다.
	ID int64 `json:"id"`
	// SessionKey 는 벤더가 준 세션 식별자다. 표시·디버깅용이다.
	SessionKey string `json:"session_key"`
	Vendor     string `json:"vendor"`
	Title      string `json:"title"`
	// WorkspacePath 는 작업 폴더 원경로, ProjectName 은 그 basename 이다 (ADR 0010).
	WorkspacePath string `json:"workspace_path"`
	ProjectName   string `json:"project_name"`

	StartedAt int64 `json:"started_at"`
	// LastEventAt 은 마지막으로 알려진 활동 시각이다 (sessions.go 의 lastActivityExpr).
	LastEventAt int64 `json:"last_event_at"`
	// EndedAt 이 null 이면 진행 중이다. Status 가 그것을 문자열로 말한다.
	EndedAt *int64 `json:"ended_at"`
	Status  string `json:"status"`

	DurationMS    int64   `json:"duration_ms"`
	ActiveSeconds float64 `json:"active_seconds"`

	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	// Tokens 는 입력+출력이다. reasoning 은 출력의 부분집합이고 캐시는 더하지 않는다.
	Tokens int64 `json:"tokens"`

	// Cost 는 이 세션 **생애 전체**의 예상 비용이다. 선택 날짜로 자른 값이 아니다
	// (Home 머리말의 「합계의 정의」).
	Cost CostSummary `json:"cost"`
}

// Home 은 선택 날짜·시간대 기준의 Home 화면 요약이다.
//
// tz 나 date 가 잘못되면 에러다. DB 가 없으면 에러가 아니라 빈 상태의 요약이다 —
// 카드 4장과 2시간 창들은 모양을 유지한 채 0 으로 채워지고 최근 세션은 빈 목록이다.
func (r *Reader) Home(ctx context.Context, q HomeQuery) (HomeSummary, error) {
	loc, err := loadLocation(q.TZ)
	if err != nil {
		return HomeSummary{}, err
	}
	day, err := selectedDay(q.Date, loc, r.now())
	if err != nil {
		return HomeSummary{}, err
	}
	previous := previousDay(day, loc)

	sum := HomeSummary{
		TZ:              loc.String(),
		Date:            day.Start.Format(dateKey),
		IsToday:         day.Start.Equal(dayOf(r.now(), loc).Start),
		StartAt:         day.StartSec(),
		EndAt:           day.EndSec(),
		PreviousStartAt: previous.StartSec(),
		ActiveAgents:    []string{},
		Recent:          []RecentSession{},
	}

	current := emptyFigures(day, loc)
	db, ok := r.db()
	if !ok {
		// 미설치. 카드와 창의 모양은 유지해야 화면이 분기 없이 그린다.
		sum.applyFigures(current, emptyFigures(previous, loc))
		return sum, nil
	}

	if current, err = homeFigures(ctx, db, day, loc); err != nil {
		return HomeSummary{}, err
	}
	prev, err := homeFigures(ctx, db, previous, loc)
	if err != nil {
		return HomeSummary{}, err
	}
	sum.applyFigures(current, prev)

	if sum.ActiveAgents, sum.ActiveSessions, err = activeAgents(ctx, db); err != nil {
		return HomeSummary{}, err
	}
	if sum.Recent, sum.RecentTruncated, err = recentSessions(ctx, db, day, q.RecentLimit); err != nil {
		return HomeSummary{}, err
	}
	return sum, nil
}

func (s *HomeSummary) applyFigures(current, previous homeDay) {
	s.Totals = current.totals
	s.PreviousTotals = previous.totals
	s.Cost = current.cost
	s.TwoHour = current.twoHour
	s.Cards = buildHomeCards(current, previous)
}

// selectedDay 는 조회 대상 하루다. 빈 날짜는 "오늘" 이다.
//
// 경계는 UTC 자정이 아니라 loc 의 자정이고, DST 로 자정이 존재하지 않는 날은 time.Date 가
// 유효한 인스턴트로 정규화한다 (timezone.go).
func selectedDay(date string, loc *time.Location, now time.Time) (timeRange, error) {
	trimmed := strings.TrimSpace(date)
	if trimmed == "" {
		return dayOf(now, loc), nil
	}
	parsed, err := time.ParseInLocation(dateKey, trimmed, loc)
	if err != nil {
		// 원인 에러를 감싸지 않는다 — 사용자에게 보이는 문자열이라 파서 내부 사정을 싣지 않는다.
		return timeRange{}, fmt.Errorf("dashboard: 날짜 형식이 잘못됨 %q (YYYY-MM-DD 여야 한다)", date)
	}
	return dayOf(parsed, loc), nil
}

// homeDay 는 하루치 계산 결과다. 오늘과 전날에 같은 함수를 쓰기 위해 묶어 둔다.
type homeDay struct {
	totals  Totals
	cost    CostSummary
	twoHour TwoHourAverage
}

// emptyFigures 는 DB 가 없을 때의 하루다. 창 골격은 유지한다.
func emptyFigures(tr timeRange, loc *time.Location) homeDay {
	return homeDay{
		cost:    newCostAccumulator().summary(),
		twoHour: TwoHourAverage{Windows: twoHourWindows(tr, loc), Cost: nanoMoney(0)},
	}
}

// homeFigures 는 하루의 집계·예상 비용·2시간 창을 한 번에 만든다.
//
// 질의는 둘이다 — 승격 테이블 집계(aggregate.go)와 llm_calls 행 스캔(today_scan.go).
// 둘을 JOIN 으로 묶지 않는 이유는 행이 곱해져 모든 SUM 이 부풀기 때문이다 (aggregate.go 머리말).
func homeFigures(ctx context.Context, db sqlQuerier, tr timeRange, loc *time.Location) (homeDay, error) {
	out := homeDay{twoHour: TwoHourAverage{Windows: twoHourWindows(tr, loc)}}

	rows, err := aggregate(ctx, db, DimTotal, "", tr)
	if err != nil {
		return homeDay{}, err
	}
	for _, row := range rows {
		out.totals.add(row.Totals)
		w := &out.twoHour.Windows[twoHourIndex(tr, row.Hour, len(out.twoHour.Windows))]
		w.Tokens += row.Tokens()
		w.ActiveSeconds += row.ActiveSeconds
		w.Active = w.Active || hasActivity(row.Totals)
	}

	day := newCostAccumulator()
	windowCost := make([]pricing.NanoUSD, len(out.twoHour.Windows))
	err = eachLLMCall(ctx, db, llmCallsInRangeSQL, []any{tr.StartSec(), tr.EndSec()},
		func(c llmCall) {
			res := day.add(c.Usage)
			if !res.Cost.Countable() {
				return
			}
			// 비용은 호출 시각으로 창에 넣는다. 집계의 시간 버킷과 달리 초 단위라
			// 30·45분 오프셋 시간대에서 더 정확하다.
			i := twoHourIndex(tr, c.CalledAt, len(windowCost))
			windowCost[i] += res.Cost.Total.NanoUSD
			out.twoHour.Windows[i].Active = true
		})
	if err != nil {
		return homeDay{}, err
	}
	for i := range out.twoHour.Windows {
		out.twoHour.Windows[i].Cost = nanoMoney(windowCost[i])
	}
	out.cost = day.summary()
	out.twoHour.average()
	return out, nil
}

// hasActivity 는 이 집계 조각에 사실이 하나라도 있었는지다. 비용·토큰만 보면 도구만 쓴
// 시간대가 "활동 없음" 으로 빠져 평균의 분모가 틀어진다.
func hasActivity(t Totals) bool {
	return t.APIRequests > 0 || t.ToolCalls > 0 || t.Prompts > 0 ||
		t.SessionsStarted > 0 || t.LinesAdded > 0 || t.LinesRemoved > 0
}

// twoHourWindows 는 구간을 2시간씩 자른다.
//
// 마지막 창은 구간 끝에서 잘린다. DST 전환일의 하루는 23·25시간이라 2시간으로 나누어
// 떨어지지 않는데, 창을 구간 밖으로 넘기면 그 하루의 창 합계가 카드와 어긋난다.
func twoHourWindows(tr timeRange, loc *time.Location) []TwoHourWindow {
	out := []TwoHourWindow{}
	for start := tr.StartSec(); start < tr.EndSec(); start += twoHourSeconds {
		end := start + twoHourSeconds
		if end > tr.EndSec() {
			end = tr.EndSec()
		}
		out = append(out, TwoHourWindow{
			StartAt:   start,
			EndAt:     end,
			LocalHour: localHour(start, loc),
			Cost:      nanoMoney(0),
		})
	}
	return out
}

// twoHourIndex 는 시각이 속한 창의 번호다. 구간 밖의 값은 양 끝 창으로 눌러 담는다.
//
// 누르는 이유는 UTC+5:30 같은 30·45분 오프셋 때문이다. 집계의 시간 버킷은 UTC 정시라
// 현지 자정 직후의 버킷은 시작 시각이 구간 시작보다 이를 수 있는데, 그 사실을 버리면
// 창 합계가 카드보다 작아진다. 버킷 귀속 규칙(자기 시작 시각이 속한 날) 자체는 그대로다.
func twoHourIndex(tr timeRange, sec int64, n int) int {
	if n <= 0 {
		return 0
	}
	i := int((sec - tr.StartSec()) / twoHourSeconds)
	switch {
	case sec < tr.StartSec() || i < 0:
		return 0
	case i >= n:
		return n - 1
	default:
		return i
	}
}

// average 는 활동 창만으로 평균을 낸다.
func (a *TwoHourAverage) average() {
	var (
		tokens float64
		active float64
		nano   pricing.NanoUSD
	)
	a.ActiveWindows = 0
	for _, w := range a.Windows {
		if !w.Active {
			continue
		}
		a.ActiveWindows++
		tokens += float64(w.Tokens)
		active += w.ActiveSeconds
		nano += w.Cost.NanoUSD
	}
	if a.ActiveWindows == 0 {
		a.Tokens, a.ActiveSeconds, a.Cost = 0, 0, nanoMoney(0)
		return
	}
	n := float64(a.ActiveWindows)
	a.Tokens = round1(tokens / n)
	a.ActiveSeconds = round1(active / n)
	// 금액 평균도 정수 나눗셈이다. float 로 나눈 뒤 다시 nano 로 되돌리면 마지막 자리가 흔들린다.
	a.Cost = nanoMoney(nano / pricing.NanoUSD(a.ActiveWindows))
}

// buildHomeCards 는 티켓이 지정한 4개 카드다 — 토큰 · 예상 비용 · AI 활동 시간 · 2시간 평균.
//
// Today 의 4장과 구성이 다르다. Today 는 세션 수를 보여주고 비용은 보고값이다.
func buildHomeCards(current, previous homeDay) []Card {
	return []Card{
		newCard(MetricTokens, float64(current.totals.Tokens()), float64(previous.totals.Tokens())),
		newCard(MetricEstimatedCostUSD, current.cost.Total.USD, previous.cost.Total.USD),
		newCard(MetricActiveSeconds, current.totals.ActiveSeconds, previous.totals.ActiveSeconds),
		newCard(MetricTwoHourActiveSeconds, current.twoHour.ActiveSeconds, previous.twoHour.ActiveSeconds),
	}
}

// ── 최근 활동 ───────────────────────────────────────────────────────────────

// recentSessionsSQL 은 선택 날짜에 **시작한** 세션이다.
//
// 상태와 마지막 활동 시각은 sessions.go 의 식을 그대로 쓴다. 두 화면이 다른 식을 쓰면
// 같은 세션이 Home 에서는 진행 중, Activity 에서는 완료로 보인다.
//
// 정렬 2순위가 id 인 이유도 sessions.go 와 같다 — started_at 이 같은 세션들의 순서가
// 흔들리면 잘리는 지점이 새로고침마다 달라진다.
var recentSessionsSQL = `SELECT s.id, s.session_key, s.vendor_id, COALESCE(s.title,''),
  COALESCE(s.workspace_path,''), COALESCE(s.started_at,0), ` + lastActivityExpr + `,
  s.ended_at, ` + statusExpr + `, COALESCE(s.active_time_sec,0)
FROM sessions s
WHERE s.started_at IS NOT NULL AND s.started_at >= ? AND s.started_at < ?
ORDER BY s.started_at DESC, s.id DESC LIMIT ?`

// recentSessions 는 선택 날짜의 최근 세션 요약이다.
//
// 비용·토큰은 목록을 확정한 뒤 그 세션들의 llm_calls 를 한 번 더 읽어 채운다. 목록 질의에
// 상관 서브쿼리로 붙이지 않는 이유는 가격표 산정이 SQL 로 표현되지 않기 때문이다 —
// 보고값이 없는 호출의 단가는 Go 쪽 표에만 있다 (internal/pricing).
func recentSessions(ctx context.Context, db sqlQuerier, tr timeRange, limit int) ([]RecentSession, bool, error) {
	out, truncated, err := scanRecentSessions(ctx, db, tr, limit)
	if err != nil {
		return nil, false, err
	}
	// 목록을 확정하고 커서를 닫은 **뒤에** 두 번째 질의를 던진다. 첫 질의의 행을 흘리는
	// 도중에 던지면 조회 커넥션(최대 4개)을 두 개씩 잡는다.
	if err := fillRecentUsage(ctx, db, out); err != nil {
		return nil, false, err
	}
	return out, truncated, nil
}

func scanRecentSessions(ctx context.Context, db sqlQuerier, tr timeRange, limit int) (out []RecentSession, truncated bool, err error) {
	const op = "최근 세션 조회"
	out = []RecentSession{}

	want := clampLimit(limit, defaultRecentSessions, maxRecentSessions)
	// 상한 +1 을 받아 "더 있다" 를 별도 질의 없이 판정한다 (sessions.go 의 타임라인과 같은 수법).
	rows, err := db.QueryContext(ctx, recentSessionsSQL, tr.StartSec(), tr.EndSec(), want+1)
	if err != nil {
		return nil, false, queryErr(op, err)
	}
	defer closeRows(rows, op, &err)

	for rows.Next() {
		if len(out) == want {
			truncated = true
			break
		}
		s, serr := scanRecentSession(rows.Scan)
		if serr != nil {
			return nil, false, queryErr(op, serr)
		}
		out = append(out, s)
	}
	return out, truncated, nil
}

func scanRecentSession(scan func(...any) error) (RecentSession, error) {
	var (
		s     RecentSession
		ended sql.NullInt64
	)
	if err := scan(&s.ID, &s.SessionKey, &s.Vendor, &s.Title,
		&s.WorkspacePath, &s.StartedAt, &s.LastEventAt, &ended, &s.Status,
		&s.ActiveSeconds); err != nil {
		return RecentSession{}, err
	}
	s.EndedAt = nullInt64(ended)
	s.ProjectName = baseName(s.WorkspacePath)
	// 길이 계산은 세션 목록과 같은 함수를 쓴다. 진행 중인 세션은 마지막 활동까지가 길이다.
	s.DurationMS = durationMS(SessionRow{
		StartedAt:   s.StartedAt,
		LastEventAt: s.LastEventAt,
		EndedAt:     s.EndedAt,
	})
	s.Cost = newCostAccumulator().summary()
	return s, nil
}

// fillRecentUsage 는 목록의 토큰과 예상 비용을 채운다. 질의는 한 번이다 — 세션마다 한 번씩
// 물으면 목록 길이만큼 왕복한다.
func fillRecentUsage(ctx context.Context, db sqlQuerier, rows []RecentSession) error {
	if len(rows) == 0 {
		return nil
	}
	args := make([]any, len(rows))
	index := make(map[int64]int, len(rows))
	cost := make([]costAccumulator, len(rows))
	for i, s := range rows {
		args[i] = s.ID
		index[s.ID] = i
		cost[i] = newCostAccumulator()
	}

	err := eachLLMCall(ctx, db, llmCallsOfSessionsSQL(len(rows)), args, func(c llmCall) {
		i, ok := index[c.SessionID]
		if !ok {
			return
		}
		cost[i].add(c.Usage)
		rows[i].InputTokens += c.Usage.InputTokens.Or(0)
		rows[i].OutputTokens += c.Usage.OutputTokens.Or(0)
	})
	if err != nil {
		return err
	}
	for i := range rows {
		// 캐시 토큰은 더하지 않고 reasoning 은 출력에 이미 들어 있다 (Totals.Tokens 와 같은 규칙).
		rows[i].Tokens = rows[i].InputTokens + rows[i].OutputTokens
		rows[i].Cost = cost[i].summary()
	}
	return nil
}

// Home 은 선택 날짜의 Home 화면 요약이다.
//
// 이 메서드가 service.go 가 아니라 여기 있는 것은 파일 소유를 나눠 병행 작업의 충돌을
// 줄이기 위해서다. 동작은 service.go 의 다른 위임 메서드와 같다 — 조회 전에 재연결한다.
func (s *Service) Home(ctx context.Context, q HomeQuery) (HomeSummary, error) {
	s.reconnect()
	return s.reader.Home(ctx, q)
}
