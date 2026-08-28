package dashboard

import (
	"context"
	"database/sql"

	"github.com/your-org/pulsemetry/internal/event"
	"github.com/your-org/pulsemetry/internal/pricing"
)

// llm_calls 한 행을 가격표(internal/pricing)의 입력으로 읽는 스캐너다.
//
// # 왜 aggregate.go 의 SUM 을 그대로 쓰지 않는가
//
// aggregate.go 는 `SUM(llm_calls.cost_usd)` 를 더한다. 그것은 **벤더가 보고한 비용만**의
// 합이라, 모델과 토큰은 아는데 `cost_usd` 가 비어 있는 호출이 조용히 0 원으로 사라진다.
// Home 의 「예상 비용」은 그 빈칸을 토큰 단가로 메운 값이므로 SUM 으로는 만들 수 없고,
// **호출 한 건씩 pricing 에 태워야** 한다 (보고값 우선 — internal/pricing 머리말).
//
// 합산은 float 이 아니라 정수 nano-USD 로 한다. Home 카드의 합계와 세션별 값의 합이
// 어긋나면 사용자는 둘 다 믿지 않는다 (internal/pricing/money.go).
//
// # 왜 행을 슬라이스에 모으지 않는가
//
// 하루치 호출 수에 상한이 없다. 콜백으로 흘리면 누적기만 메모리에 남는다.

// llmCall 은 llm_calls 한 행이다. 세션 귀속과 시각은 창(2시간)·세션별 집계가 쓰고,
// 나머지는 그대로 pricing.Usage 로 간다.
type llmCall struct {
	SessionID int64
	CalledAt  int64
	Usage     pricing.Usage
}

// llmCallColumns 의 순서는 scanLLMCall 의 스캔 순서와 대응해야 한다.
const llmCallColumns = `s.id, COALESCE(c.called_at, 0), c.model,
  c.input_tokens, c.output_tokens, c.cache_read_tokens, c.cache_write_tokens,
  c.reasoning_tokens, c.cost_usd`

// 별칭은 aggregate.go 와 같게 고정한다 — c(승격 테이블) · t(turns) · s(sessions).
const llmCallFrom = ` FROM llm_calls c
  JOIN turns t ON t.id = c.turn_id
  JOIN sessions s ON s.id = t.session_id`

// llmCallsInRangeSQL 은 구간 안에서 일어난 호출이다.
//
// `called_at` 이 NULL 인 행은 **어느 날짜에도 넣지 않는다.** 언제 일어났는지 모르는 비용을
// 임의의 날에 얹으면 그 날의 카드만 조용히 부푼다. aggregate.go 의 구간 필터와 같은 규칙이다.
const llmCallsInRangeSQL = `SELECT ` + llmCallColumns + llmCallFrom +
	` WHERE c.called_at IS NOT NULL AND c.called_at >= ? AND c.called_at < ?`

// llmCallsOfSessionsSQL 은 세션 id 목록에 속한 **생애 전체**의 호출이다. 구간으로 자르지
// 않는 것이 의도다 — 최근 세션 목록의 값은 그 세션의 전체 합계다 (home.go 의 합계 정의).
func llmCallsOfSessionsSQL(n int) string {
	return `SELECT ` + llmCallColumns + llmCallFrom +
		` WHERE s.id IN (` + placeholders(n) + `)`
}

// eachLLMCall 은 질의 결과를 한 행씩 흘린다. fn 은 에러를 내지 않는다 — 누적만 하는
// 자리라 실패할 일이 없고, 실패할 수 있게 두면 중간에 끊긴 합계가 정상값처럼 보인다.
func eachLLMCall(ctx context.Context, db sqlQuerier, query string, args []any, fn func(llmCall)) (err error) {
	const op = "LLM 호출 비용 조회"
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return queryErr(op, err)
	}
	defer closeRows(rows, op, &err)

	for rows.Next() {
		c, serr := scanLLMCall(rows.Scan)
		if serr != nil {
			return queryErr(op, serr)
		}
		fn(c)
	}
	return nil
}

func scanLLMCall(scan func(...any) error) (llmCall, error) {
	var (
		c                               llmCall
		model                           sql.NullString
		in, out, read, write, reasoning sql.NullInt64
		cost                            sql.NullFloat64
	)
	if err := scan(&c.SessionID, &c.CalledAt, &model,
		&in, &out, &read, &write, &reasoning, &cost); err != nil {
		return llmCall{}, err
	}
	c.Usage = pricing.Usage{
		Model:            model.String,
		InputTokens:      optInt64(in),
		OutputTokens:     optInt64(out),
		CacheReadTokens:  optInt64(read),
		CacheWriteTokens: optInt64(write),
		// reasoning 은 출력 토큰의 부분집합이라 과금에 다시 더해지지 않는다. 그래도 넘기는
		// 이유는 pricing.Usage 머리말과 같다 — 빠뜨린 것과 구분되어야 한다.
		ReasoningTokens: optInt64(reasoning),
		ReportedCostUSD: optFloat64(cost),
	}
	return c, nil
}

// optInt64·optFloat64 는 NULL 과 0 을 구분해 옮긴다. 0 으로 눕히면 "보고되지 않은 토큰" 과
// "0 토큰" 이 같아지고, 그러면 비용을 낼 수 없는 호출이 0 원 호출처럼 보인다.
func optInt64(n sql.NullInt64) event.Opt[int64] {
	if !n.Valid {
		return event.Opt[int64]{}
	}
	return event.Some(n.Int64)
}

func optFloat64(n sql.NullFloat64) event.Opt[float64] {
	if !n.Valid {
		return event.Opt[float64]{}
	}
	return event.Some(n.Float64)
}

// ── 비용 누적 ───────────────────────────────────────────────────────────────

// CostSummary 는 구간(또는 세션) 하나의 예상 비용이다.
//
// 금액 필드가 Total 하나뿐인 것은 pricing.Cost 와 같은 이유다 — 보고값과 추정값을 서로
// 다른 필드에 담아 두면 언젠가 누군가 둘 다 더한다. 어느 쪽이 몇 건이었는지는 아래 개수가
// 말한다.
type CostSummary struct {
	// Total 은 이 구간의 비용이다. 정수 nano-USD 로 더한 값이라 더하는 순서에 무관하다.
	Total pricing.Money `json:"total"`

	// Calls 는 이 구간의 LLM 호출 수다. Reported+Estimated+Unavailable 과 같다.
	Calls int64 `json:"calls"`
	// Reported 는 벤더가 보고한 cost_usd 를 그대로 쓴 호출 수다.
	Reported int64 `json:"reported"`
	// Estimated 는 보고값이 없어 토큰 단가로 계산한 호출 수다.
	Estimated int64 `json:"estimated"`
	// Unavailable 은 비용을 정하지 못한 호출 수다. **Total 에 0 원으로 들어간 것이 아니라
	// 빠져 있다.** 이 값이 크면 화면은 "일부 호출의 비용을 알 수 없음" 을 알려야 한다.
	Unavailable int64 `json:"unavailable"`

	// CacheSavings 는 캐시로 아낀(음수면 더 낸) 금액이다. **Total 과 더하지 않는다** —
	// 비용이 아니라 반사실(counterfactual)이다 (internal/pricing/savings.go).
	CacheSavings pricing.Money `json:"cache_savings"`

	// TableVersion·EffectiveDate 는 이 값을 만든 가격표의 판이다. 화면에 뜬 비용이 어느
	// 판의 단가에서 나왔는지 되짚을 수 있어야 한다.
	TableVersion  string `json:"table_version"`
	EffectiveDate string `json:"effective_date"`
}

// costAccumulator 는 호출들을 정수 nano-USD 로 더한다.
type costAccumulator struct {
	table   pricing.Table
	total   pricing.NanoUSD
	savings pricing.NanoUSD

	calls       int64
	reported    int64
	estimated   int64
	unavailable int64
}

func newCostAccumulator() costAccumulator {
	return costAccumulator{table: pricing.Default()}
}

// add 는 호출 한 건을 더하고 그 결과를 돌려준다. 결과를 돌려주는 이유는 호출자가 같은
// 산정 결과를 창(2시간)별 누적에도 써야 하기 때문이다 — 두 번 계산하면 두 값이 갈릴 수 있다.
func (a *costAccumulator) add(u pricing.Usage) pricing.Result {
	res := a.table.Estimate(u)
	a.addResult(res)
	return res
}

func (a *costAccumulator) addResult(res pricing.Result) {
	a.calls++
	switch res.Cost.Source {
	case pricing.SourceReported:
		a.reported++
	case pricing.SourceEstimated:
		a.estimated++
	default:
		a.unavailable++
	}
	if res.Cost.Countable() {
		a.total += res.Cost.Total.NanoUSD
	}
	if res.CacheSavings.Available {
		a.savings += res.CacheSavings.Total.NanoUSD
	}
}

func (a costAccumulator) summary() CostSummary {
	return CostSummary{
		Total:         nanoMoney(a.total),
		Calls:         a.calls,
		Reported:      a.reported,
		Estimated:     a.estimated,
		Unavailable:   a.unavailable,
		CacheSavings:  nanoMoney(a.savings),
		TableVersion:  a.table.Version,
		EffectiveDate: a.table.EffectiveDate,
	}
}

// nanoMoney 는 누적한 정수 금액을 화면용 Money 로 옮긴다. pricing 의 생성자가 비공개라
// 여기서 같은 규칙(USD 는 nano 의 파생)을 지킨다.
func nanoMoney(n pricing.NanoUSD) pricing.Money {
	return pricing.Money{NanoUSD: n, USD: n.USD()}
}
