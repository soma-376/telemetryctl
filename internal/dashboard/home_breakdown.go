package dashboard

import (
	"context"
	"time"

	"github.com/your-org/pulsemetry/internal/pricing"
)

// Home 화면의 시간대·벤더·모델 사용량 분해 (PROJ-89).
//
// Home(q) 은 선택 날짜의 **합계**를 준다 (home.go). 이 파일은 그 하루를 셋으로 쪼갠다 —
// 2시간 창의 시계열, 벤더별 합계와 점유율, 벤더 안의 모델별 사용량.
//
// # ★ 시계열 합계는 Home 일간 총합과 같다
//
// 이것이 이 조회의 인수조건이고, 아래 세 가지가 그것을 보장한다.
//
//  1. **구간이 같다.** selectedDay·timezone.go 를 그대로 쓴다. 현지 자정 경계와 DST 의
//     23·25시간 하루가 저절로 따라온다.
//  2. **집계기가 같다.** 승격 테이블 쪽은 aggregate.go 를, llm_calls 행 스캔 쪽은
//     home_scan.go 의 스캐너와 costAccumulator 를 그대로 쓴다. dim 만 total 에서
//     vendor 로 바뀔 뿐 읽는 행은 한 행도 다르지 않다.
//  3. **금액을 정수 nano-USD 로 더한다.** 산정(pricing.Table.Estimate)은 호출 한 건당
//     정확히 한 번만 하고, 그 결과를 그 날·벤더·모델·창 누적기에 나눠 넣는다. 두 번
//     계산하면 같은 호출이 두 값을 낼 수 있고, float 으로 더하면 순서에 따라 마지막
//     자리가 흔들린다.
//
// 그래서 다음이 성립한다 (home_breakdown_test.go 가 Home 과 직접 대조해 고정한다).
//
//	Σ Windows[i]              = Σ Vendors[j]              = Home 의 하루 합계
//	Σ Windows[i].Vendors[j]   = Windows[i]
//	Σ Vendors[j].Models[k]    = Vendors[j]  (Models 가 잘리지 않았을 때)
//
// # 버킷 경계 — PROJ-88 의 규칙을 그대로 물려받는다
//
// 창에 넣는 기준이 값마다 다르다. 물려받은 것이지 새로 정한 것이 아니다.
//
//	비용            → llm_calls.called_at (초 단위)
//	그 밖의 모든 값 → aggregate.go 의 UTC 정시 버킷
//
// UTC+5:30·+5:45 같은 오프셋에서는 정시 버킷의 시작이 현지 2시간 창의 경계와 어긋나
// 호출 하나가 옆 창에 들어갈 수 있다. **하루 합계는 영향받지 않는다** — 구간 필터는
// called_at 으로 걸고, 구간 밖으로 밀린 버킷은 twoHourIndex 가 양 끝 창으로 눌러 담기
// 때문이다 (home.go).
//
// 규칙을 여기서 고치지 않은 이유는 Home 의 2시간 창(HomeSummary.TwoHour)과 이 시계열이
// **같은 화면에 나란히** 놓이기 때문이다. 한쪽만 고치면 같은 창의 같은 지표가 두 값을
// 낸다. 고치려면 aggregate.go 가 시각을 초 단위로 내려 줘야 하고, 그것은 Home·Today·
// Breakdown 이 함께 움직이는 별도 변경이다.
//
// # 토큰 중복 가산 금지 — Home 요약과 같은 규칙
//
// Tokens 는 **입력+출력**뿐이다. 캐시 토큰(cache_read·cache_write)은 이미 한 번 센 입력을
// 다시 읽은 양이라 따로 보이되 Tokens 에는 들어가지 않고, reasoning 은 출력 토큰의
// 부분집합이라 애초에 더할 것이 없다 (Totals.Tokens · docs/local-pipeline.md §6.8).
//
// **reasoning_tokens 필드를 두지 않는다.** v3 쓰기 경로에 출처가 없어
// (store/promote.go 가 이 컬럼에 NULL 을 넣는다) 항상 0 이 되기 때문이다. 항상 0 인 필드를
// 새 표면에 두면 화면이 "reasoning 0 토큰" 이라는 잘못된 사실을 그린다 — 값이 없는 것과
// 0 인 것은 다르다 (Totals 의 api_errors 주석과 같은 판단이고, 그쪽은 이미 나간 TS
// 바인딩 때문에 지우지 못했을 뿐이다). 출처가 생기면 그때 필드를 더한다.
//
// # 한 벤더만 있어도 화면 계약이 유지된다
//
//   - 창은 활동이 없어도 전부 들어 있다. 빠진 창이 있으면 화면의 막대가 옆으로 밀려
//     다른 시간대의 값처럼 보인다.
//   - **모든 창이 Vendors 와 같은 길이·같은 순서의 벤더 줄을 갖는다.** 그 창에 그 벤더의
//     활동이 없으면 0 이다. 화면은 창마다 벤더를 찾아 맞출 필요가 없다.
//   - 벤더가 하나뿐이면 그 하나가 점유율 1000 을 전부 갖는다 (share.go).
//   - 관측되지 않은 벤더는 **넣지 않는다.** 이 패키지는 관측한 사실만 돌려주고, 화면이
//     아는 벤더 목록과 대조하는 것은 GUI 몫이다 (vendors.go 의 VendorStatus 와 같은 규칙).

const (
	// defaultTopModels 는 벤더당 돌려주는 모델 줄 수의 기본값이다. 화면의 "상위 모델" 은
	// 목록이 아니라 요약이라 길 이유가 없다.
	defaultTopModels = 5
	maxTopModels     = 50
)

// HomeBreakdownQuery 는 분해 조회 조건이다. 날짜·시간대의 의미는 HomeQuery 와 같다.
type HomeBreakdownQuery struct {
	// TZ 는 하루의 경계를 정하는 시간대다. 빈 문자열은 UTC, 잘못된 이름은 에러다.
	TZ string `json:"tz"`
	// Date 는 그 시간대의 날짜(YYYY-MM-DD)다. 비어 있으면 오늘이다. 형식이 틀리면 에러다.
	Date string `json:"date"`
	// ModelLimit 는 벤더당 모델 줄 수의 상한이다. 0 이하는 기본값, 상한 초과는 상한이다.
	ModelLimit int `json:"model_limit"`
}

// UsageTotals 는 창과 벤더가 함께 쓰는 사용량 묶음이다.
//
// 금액이 여기 없는 것은 의도다. 창은 pricing.Money 를, 벤더·모델은 산정 갈래까지 담은
// CostSummary 를 쓴다 — 같은 이름의 필드가 층마다 다른 타입이 되는 편보다 낫다.
type UsageTotals struct {
	// APIRequests 는 llm_calls 행 수다. 벤더 줄에서는 Cost.Calls 와 같은 값이어야 한다 —
	// 하나는 집계, 하나는 행 스캔에서 오므로 어긋나면 둘 중 하나가 구간을 다르게 잘랐다.
	APIRequests     int64 `json:"api_requests"`
	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	CacheReadTokens int64 `json:"cache_read_tokens"`
	// CacheWriteTokens 는 Totals 의 cache_creation_tokens 와 같은 값이다. 이름을 llm_calls
	// 컬럼(cache_write_tokens)과 pricing.Usage 쪽에 맞췄다.
	CacheWriteTokens int64 `json:"cache_write_tokens"`
	// Tokens 는 입력+출력이다. 캐시·reasoning 은 더하지 않는다 (파일 머리말).
	Tokens int64 `json:"tokens"`

	ToolCalls       int64   `json:"tool_calls"`
	Prompts         int64   `json:"prompts"`
	SessionsStarted int64   `json:"sessions_started"`
	ActiveSeconds   float64 `json:"active_seconds"`
}

// VendorWindow 는 창 하나 안의 벤더 한 줄이다.
type VendorWindow struct {
	Vendor string `json:"vendor"`
	// Cost 는 이 창·이 벤더의 예상 비용이다.
	Cost pricing.Money `json:"cost"`
	UsageTotals
}

// UsageWindow 는 2시간 창 하나다. 경계와 Active 의 정의는 Home 의 TwoHourWindow 와 같고,
// 실제로 같은 함수가 만든다 (home.go 의 twoHourWindows).
type UsageWindow struct {
	// StartAt·EndAt 은 이 창의 UTC unix 초 [시작, 끝) 이다. DST 전환일의 마지막 창은
	// 2시간보다 짧거나 길다.
	StartAt int64 `json:"start_at"`
	EndAt   int64 `json:"end_at"`
	// LocalHour 는 이 창이 시작하는 현지 시각(0~23)이다. 화면의 축 라벨이 쓴다.
	LocalHour int `json:"local_hour"`
	// Active 는 이 창에 사실이 하나라도 있었는지다. Home 의 TwoHourWindow.Active 와 같다.
	Active bool `json:"active"`

	Cost pricing.Money `json:"cost"`
	UsageTotals

	// Vendors 는 **항상 HomeBreakdown.Vendors 와 같은 길이·같은 순서**다. 활동이 없는
	// 벤더는 0 으로 들어 있다.
	Vendors []VendorWindow `json:"vendors"`
}

// PeakWindow 는 「최고 사용 시간대」다.
//
// # 무엇으로 고르는가
//
// 토큰이 가장 많은 창이다. 토큰이 같으면 예상 비용(정수 nano-USD)이 큰 창, 그것도 같으면
// **이른 창**이 이긴다. 비용을 1순위로 두지 않은 이유는 비용을 정하지 못한 호출이 있기
// 때문이다 — 모르는 모델만 쓴 시간대가 "사용량 0" 으로 밀려나면 안 된다.
//
// 토큰도 비용도 0 인 하루에는 Found 가 false 이고 Index 는 -1 이다. 아무 창이나 골라
// 돌려주면 화면이 "새벽 0시가 가장 바빴다" 를 그린다.
type PeakWindow struct {
	Found bool `json:"found"`
	// Index 는 Windows 안의 위치다. Found 가 false 면 -1 이다.
	Index     int   `json:"index"`
	StartAt   int64 `json:"start_at"`
	EndAt     int64 `json:"end_at"`
	LocalHour int   `json:"local_hour"`

	Tokens int64         `json:"tokens"`
	Cost   pricing.Money `json:"cost"`
	// Vendor 는 그 창에서 토큰이 가장 많았던 벤더다. 같은 규칙으로 비용·이름 순으로 가른다.
	// 그 창에 사용량이 없으면 빈 문자열이다.
	Vendor string `json:"vendor"`
}

// ModelUsage 는 벤더 안의 모델 한 줄이다.
type ModelUsage struct {
	// Model 은 pricing.Canonical 로 정규화한 이름이다. 같은 모델의 날짜·리전 표기가 여기서
	// 한 줄로 모인다 — 정규화하지 않으면 화면의 같은 모델이 여러 줄로 쪼개진다.
	// 빈 문자열은 모델 이름 자체가 없던 호출이다.
	Model string `json:"model"`
	// Cost 는 이 모델의 예상 비용이다. Unavailable 이 크면 이 줄의 금액은 과소 집계다.
	Cost CostSummary `json:"cost"`

	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	CacheReadTokens  int64 `json:"cache_read_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
	// Tokens 는 입력+출력이다. 캐시는 더하지 않는다.
	Tokens int64 `json:"tokens"`
}

// VendorUsage 는 벤더 한 줄이다 (선택 날짜 전체).
type VendorUsage struct {
	Vendor string `json:"vendor"`
	// Cost 는 이 벤더의 예상 비용이다 (가격표 기준, 보고값 우선).
	Cost CostSummary `json:"cost"`
	UsageTotals

	// CostSharePermille·TokenSharePermille 은 정수 천분율 점유율이다. 각각의 합은
	// 기준 합계가 0 보다 클 때 정확히 SharePermilleTotal(1000)이다 (share.go).
	CostSharePermille  int `json:"cost_share_permille"`
	TokenSharePermille int `json:"token_share_permille"`

	// Models 는 상위 모델이다. 비용·토큰 내림차순이고 동률은 모델 이름으로 가른다.
	Models []ModelUsage `json:"models"`
	// ModelsTruncated 가 true 면 목록이 ModelLimit 에서 잘렸다는 뜻이다. 이때 모델 줄의
	// 합은 이 벤더 줄보다 **작다.**
	ModelsTruncated bool `json:"models_truncated"`
}

// HomeBreakdown 은 선택 날짜의 시간대·벤더·모델 분해 한 장이다.
type HomeBreakdown struct {
	// TZ 는 실제로 적용된 시간대 이름, Date 는 그 시간대의 선택 날짜다.
	TZ   string `json:"tz"`
	Date string `json:"date"`
	// StartAt·EndAt 은 선택 날짜 구간의 UTC unix 초 [시작, 끝) 이다. Home 의 같은 이름과
	// 같은 값이다.
	StartAt int64 `json:"start_at"`
	EndAt   int64 `json:"end_at"`

	// Windows 는 현지 자정부터 2시간씩 자른 시계열이다. 활동이 없는 창도 0 으로 들어 있다.
	Windows []UsageWindow `json:"windows"`
	// Peak 는 사용량이 가장 많았던 창이다.
	Peak PeakWindow `json:"peak"`
	// Vendors 는 관측된 벤더의 합계다. 비용 내림차순이고 동률은 벤더 이름으로 가른다.
	Vendors []VendorUsage `json:"vendors"`

	// Totals 는 그 날 전체 집계다. **HomeSummary.Totals 와 같은 값이다.**
	Totals Totals `json:"totals"`
	// Cost 는 그 날 전체 예상 비용이다. **HomeSummary.Cost 와 같은 값이다.**
	Cost CostSummary `json:"cost"`
}

// HomeBreakdown 은 선택 날짜의 시간대·벤더·모델 사용량 분해다.
//
// tz 나 date 가 잘못되면 에러다. DB 가 없으면 에러가 아니라 빈 분해다 — 창 골격은 유지한
// 채 0 으로 채우고 벤더 목록은 비어 있다 (ADR 0004, absent_test.go).
func (r *Reader) HomeBreakdown(ctx context.Context, q HomeBreakdownQuery) (HomeBreakdown, error) {
	loc, err := loadLocation(q.TZ)
	if err != nil {
		return HomeBreakdown{}, err
	}
	day, err := selectedDay(q.Date, loc, r.now())
	if err != nil {
		return HomeBreakdown{}, err
	}

	out := HomeBreakdown{
		TZ:      loc.String(),
		Date:    day.Start.Format(dateKey),
		StartAt: day.StartSec(),
		EndAt:   day.EndSec(),
		Windows: emptyUsageWindows(day, loc),
		Peak:    PeakWindow{Index: -1, Cost: nanoMoney(0)},
		Vendors: []VendorUsage{},
		Cost:    newCostAccumulator().summary(),
	}

	db, ok := r.db()
	if !ok {
		// 미설치. 창 골격은 유지해야 화면이 분기 없이 그린다.
		return out, nil
	}

	acc := newUsageAcc(day, len(out.Windows))
	// 질의는 둘이다 — 승격 테이블 집계와 llm_calls 행 스캔. JOIN 으로 묶지 않는 이유는
	// 행이 곱해져 모든 SUM 이 부풀기 때문이다 (aggregate.go 머리말).
	if err := acc.collectAggregate(ctx, db); err != nil {
		return HomeBreakdown{}, err
	}
	if err := acc.collectCalls(ctx, db); err != nil {
		return HomeBreakdown{}, err
	}
	acc.apply(&out, clampLimit(q.ModelLimit, defaultTopModels, maxTopModels))
	return out, nil
}

// HomeBreakdown 은 선택 날짜의 시간대·벤더·모델 분해다.
//
// 이 메서드가 service.go 가 아니라 여기 있는 것은 home.go 와 같은 이유다 — 파일 소유를
// 나눠 병행 작업의 충돌을 줄인다. 동작은 다른 위임 메서드와 같다.
func (s *Service) HomeBreakdown(ctx context.Context, q HomeBreakdownQuery) (HomeBreakdown, error) {
	s.reconnect()
	return s.reader.HomeBreakdown(ctx, q)
}

// emptyUsageWindows 는 값이 0 인 창 골격이다.
//
// Home 의 twoHourWindows 를 그대로 쓴다. 경계 계산을 다시 쓰면 DST 전환일에 두 화면의
// 창 수가 갈린다 — 그런 어긋남은 1년에 이틀만 나타나 테스트를 빠져나간다.
func emptyUsageWindows(tr timeRange, loc *time.Location) []UsageWindow {
	base := twoHourWindows(tr, loc)
	out := make([]UsageWindow, len(base))
	for i, w := range base {
		out[i] = UsageWindow{
			StartAt:   w.StartAt,
			EndAt:     w.EndAt,
			LocalHour: w.LocalHour,
			Cost:      nanoMoney(0),
			Vendors:   []VendorWindow{},
		}
	}
	return out
}

// usageFrom 은 집계 합계를 화면용 묶음으로 옮긴다. Tokens 를 여기서 한 번만 만들어
// 층마다 다른 정의가 생기지 않게 한다.
func usageFrom(t Totals) UsageTotals {
	return UsageTotals{
		APIRequests:      t.APIRequests,
		InputTokens:      t.InputTokens,
		OutputTokens:     t.OutputTokens,
		CacheReadTokens:  t.CacheReadTokens,
		CacheWriteTokens: t.CacheCreationTokens,
		Tokens:           t.Tokens(),
		ToolCalls:        t.ToolCalls,
		Prompts:          t.Prompts,
		SessionsStarted:  t.SessionsStarted,
		ActiveSeconds:    t.ActiveSeconds,
	}
}
