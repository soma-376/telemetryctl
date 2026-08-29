package dashboard

import (
	"context"
	"sort"

	"github.com/your-org/pulsemetry/internal/pricing"
)

// HomeBreakdown 의 누적기다 (PROJ-89). 계약과 불변식은 home_breakdown.go 머리말에 있다.
//
// # 두 출처를 Go 에서 합친다
//
// 승격 테이블 집계(aggregate.go)와 llm_calls 행 스캔(home_scan.go)을 각각 한 번씩 읽고
// (벤더, 창) 으로 합친다. 한 질의에 JOIN 으로 묶으면 도구 호출이 5건인 턴의 비용이 정확히
// 5배가 된다 — TestBreakdownDoesNotMultiplyAcrossSources 가 지키는 성질이다.
//
// 두 출처가 채우는 필드는 서로 겹치지 않는다. 금액과 모델별 토큰만 행 스캔에서 오고
// 나머지는 전부 집계에서 온다.

// vendorLLMCallsInRangeSQL 은 구간 안의 호출을 **벤더와 함께** 읽는다.
//
// 컬럼 목록과 FROM 절은 home_scan.go 의 상수를 그대로 쓴다. 여기서 다시 적으면 언젠가
// 한쪽만 바뀌어 Home 의 비용과 이 화면의 비용이 갈린다. 앞에 붙인 s.vendor_id 하나만
// 이 파일의 몫이고, 스캔 순서도 그만큼만 밀린다 (scanVendorLLMCall).
const vendorLLMCallsInRangeSQL = `SELECT s.vendor_id, ` + llmCallColumns + llmCallFrom +
	` WHERE c.called_at IS NOT NULL AND c.called_at >= ? AND c.called_at < ?`

// usageAcc 는 하루치 분해의 누적 상태다.
type usageAcc struct {
	day      timeRange
	nWindows int

	// totals·cost 는 그 날 전체다. 각각 Home 의 Totals·Cost 와 같은 값이 된다.
	totals Totals
	cost   costAccumulator

	windows []windowAcc
	vendors map[string]*vendorAcc
}

// windowAcc 는 창 하나의 누적이다. 벤더 줄은 그 창에 실제로 사실이 있었던 벤더만 담고,
// 화면에 나가는 0 줄은 materialize 단계에서 채운다.
type windowAcc struct {
	totals  Totals
	cost    pricing.NanoUSD
	active  bool
	vendors map[string]*windowVendorAcc
}

type windowVendorAcc struct {
	totals Totals
	cost   pricing.NanoUSD
}

type vendorAcc struct {
	totals Totals
	cost   costAccumulator
	models map[string]*modelAcc
}

type modelAcc struct {
	cost                                 costAccumulator
	input, output, cacheRead, cacheWrite int64
}

func newUsageAcc(day timeRange, nWindows int) *usageAcc {
	a := &usageAcc{
		day:      day,
		nWindows: nWindows,
		cost:     newCostAccumulator(),
		windows:  make([]windowAcc, nWindows),
		vendors:  map[string]*vendorAcc{},
	}
	for i := range a.windows {
		a.windows[i].vendors = map[string]*windowVendorAcc{}
	}
	return a
}

func (a *usageAcc) vendor(name string) *vendorAcc {
	v, ok := a.vendors[name]
	if !ok {
		v = &vendorAcc{cost: newCostAccumulator(), models: map[string]*modelAcc{}}
		a.vendors[name] = v
	}
	return v
}

func (w *windowAcc) vendor(name string) *windowVendorAcc {
	v, ok := w.vendors[name]
	if !ok {
		v = &windowVendorAcc{}
		w.vendors[name] = v
	}
	return v
}

func (v *vendorAcc) model(name string) *modelAcc {
	m, ok := v.models[name]
	if !ok {
		m = &modelAcc{cost: newCostAccumulator()}
		v.models[name] = m
	}
	return m
}

// collectAggregate 는 승격 테이블 집계를 벤더·창으로 나눠 담는다.
//
// dim 이 vendor 인 것 말고는 Home 의 homeFigures 와 같은 호출이다. 같은 행을 다른 키로
// 묶을 뿐이므로 여기서 나온 합계는 Home 의 dim=total 합계와 정확히 같다.
func (a *usageAcc) collectAggregate(ctx context.Context, db sqlQuerier) error {
	rows, err := aggregate(ctx, db, DimVendor, "", a.day)
	if err != nil {
		return err
	}
	for _, row := range rows {
		a.totals.add(row.Totals)
		a.vendor(row.Key).totals.add(row.Totals)
		if a.nWindows == 0 {
			continue
		}
		w := &a.windows[twoHourIndex(a.day, row.Hour, a.nWindows)]
		w.totals.add(row.Totals)
		// Active 의 정의는 Home 과 같다 — 비용·토큰만 보면 도구만 쓴 시간대가 빠진다.
		w.active = w.active || hasActivity(row.Totals)
		w.vendor(row.Key).totals.add(row.Totals)
	}
	return nil
}

// collectCalls 는 llm_calls 를 한 행씩 흘리며 비용·모델을 누적한다.
//
// 행을 슬라이스에 모으지 않는 이유는 home_scan.go 와 같다 — 하루치 호출 수에 상한이 없다.
func (a *usageAcc) collectCalls(ctx context.Context, db sqlQuerier) (err error) {
	const op = "벤더별 LLM 호출 비용 조회"
	rows, err := db.QueryContext(ctx, vendorLLMCallsInRangeSQL, a.day.StartSec(), a.day.EndSec())
	if err != nil {
		return queryErr(op, err)
	}
	defer closeRows(rows, op, &err)

	for rows.Next() {
		vendor, c, serr := scanVendorLLMCall(rows.Scan)
		if serr != nil {
			return queryErr(op, serr)
		}
		a.addCall(vendor, c)
	}
	return nil
}

// scanVendorLLMCall 은 맨 앞의 vendor_id 를 떼고 나머지를 home_scan.go 의 스캐너에 넘긴다.
// Usage 를 만드는 규칙(NULL 과 0 의 구분 등)을 여기서 다시 쓰지 않기 위해서다.
func scanVendorLLMCall(scan func(...any) error) (string, llmCall, error) {
	var vendor string
	c, err := scanLLMCall(func(dest ...any) error {
		return scan(append([]any{&vendor}, dest...)...)
	})
	return vendor, c, err
}

// addCall 은 호출 한 건을 그 날·벤더·모델·창에 나눠 담는다.
//
// **산정은 여기서 딱 한 번이다.** costAccumulator.add 가 돌려준 Result 를 나머지 누적기에
// addResult 로 넘긴다. 층마다 Estimate 를 다시 부르면 같은 호출이 서로 다른 금액을 낼
// 여지가 생기고, 그러면 창 합계와 카드가 어긋난다 (home_scan.go 의 add 주석).
func (a *usageAcc) addCall(vendor string, c llmCall) {
	res := a.cost.add(c.Usage)

	v := a.vendor(vendor)
	v.cost.addResult(res)

	// 모델 키는 정규화 이름이다. 날짜·리전 표기가 다른 같은 모델이 한 줄로 모인다.
	m := v.model(pricing.Canonical(c.Usage.Model))
	m.cost.addResult(res)
	m.input += c.Usage.InputTokens.Or(0)
	m.output += c.Usage.OutputTokens.Or(0)
	m.cacheRead += c.Usage.CacheReadTokens.Or(0)
	m.cacheWrite += c.Usage.CacheWriteTokens.Or(0)

	if a.nWindows == 0 || !res.Cost.Countable() {
		// 비용을 정하지 못한 호출은 창에 0 원으로도 들어가지 않고, 창을 "활동 있음" 으로
		// 만들지도 않는다. Home 의 homeFigures 와 같은 순서다.
		return
	}
	// 비용만 호출 시각으로 창에 넣는다 (물려받은 규칙 — home_breakdown.go 머리말).
	w := &a.windows[twoHourIndex(a.day, c.CalledAt, a.nWindows)]
	w.cost += res.Cost.Total.NanoUSD
	w.active = true
	w.vendor(vendor).cost += res.Cost.Total.NanoUSD
}

// ── 결과 조립 ───────────────────────────────────────────────────────────────

// apply 는 누적 상태를 응답으로 옮긴다. 벤더 순서를 먼저 확정하고, 창의 벤더 줄을 그
// 순서에 맞춘다 — 창마다 순서가 다르면 화면이 창마다 벤더를 찾아 맞춰야 한다.
func (a *usageAcc) apply(out *HomeBreakdown, modelLimit int) {
	out.Totals = a.totals
	out.Cost = a.cost.summary()
	out.Vendors = a.vendorRows(modelLimit)

	for i := range out.Windows {
		w := &a.windows[i]
		out.Windows[i].Active = w.active
		out.Windows[i].Cost = nanoMoney(w.cost)
		out.Windows[i].UsageTotals = usageFrom(w.totals)
		out.Windows[i].Vendors = w.vendorRows(out.Vendors)
	}
	out.Peak = peakOf(out.Windows)
}

// vendorRows 는 벤더 줄을 만들고 정렬한 뒤 점유율을 매긴다.
func (a *usageAcc) vendorRows(modelLimit int) []VendorUsage {
	out := make([]VendorUsage, 0, len(a.vendors))
	for name, v := range a.vendors {
		row := VendorUsage{
			Vendor:      name,
			Cost:        v.cost.summary(),
			UsageTotals: usageFrom(v.totals),
			Models:      v.modelRows(modelLimit),
		}
		row.ModelsTruncated = len(v.models) > len(row.Models)
		out = append(out, row)
	}
	// 비용 내림차순이 화면의 기본 순서다. 비용을 정하지 못한 벤더끼리는 토큰으로,
	// 그것도 같으면 이름으로 가른다 — 벤더 이름은 유일하므로 순서가 완전히 정해진다.
	sort.Slice(out, func(i, j int) bool {
		x, y := out[i], out[j]
		if x.Cost.Total.NanoUSD != y.Cost.Total.NanoUSD {
			return x.Cost.Total.NanoUSD > y.Cost.Total.NanoUSD
		}
		if x.Tokens != y.Tokens {
			return x.Tokens > y.Tokens
		}
		return x.Vendor < y.Vendor
	})

	names := make([]string, len(out))
	costs := make([]int64, len(out))
	tokens := make([]int64, len(out))
	for i, v := range out {
		names[i] = v.Vendor
		costs[i] = int64(v.Cost.Total.NanoUSD)
		tokens[i] = v.Tokens
	}
	costShare := sharePermille(costs, names)
	tokenShare := sharePermille(tokens, names)
	for i := range out {
		out[i].CostSharePermille = costShare[i]
		out[i].TokenSharePermille = tokenShare[i]
	}
	return out
}

// modelRows 는 모델 줄을 만들어 상위 limit 개로 자른다.
func (v *vendorAcc) modelRows(limit int) []ModelUsage {
	out := make([]ModelUsage, 0, len(v.models))
	for name, m := range v.models {
		out = append(out, ModelUsage{
			Model:            name,
			Cost:             m.cost.summary(),
			InputTokens:      m.input,
			OutputTokens:     m.output,
			CacheReadTokens:  m.cacheRead,
			CacheWriteTokens: m.cacheWrite,
			// 캐시는 더하지 않는다 (Totals.Tokens 와 같은 규칙).
			Tokens: m.input + m.output,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		x, y := out[i], out[j]
		if x.Cost.Total.NanoUSD != y.Cost.Total.NanoUSD {
			return x.Cost.Total.NanoUSD > y.Cost.Total.NanoUSD
		}
		if x.Tokens != y.Tokens {
			return x.Tokens > y.Tokens
		}
		return x.Model < y.Model
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// vendorRows 는 창의 벤더 줄을 **하루 벤더 목록과 같은 길이·같은 순서**로 만든다.
// 그 창에 사실이 없던 벤더는 0 줄이다 (home_breakdown.go 의 「화면 계약」).
func (w *windowAcc) vendorRows(day []VendorUsage) []VendorWindow {
	out := make([]VendorWindow, len(day))
	for i, v := range day {
		row := VendorWindow{Vendor: v.Vendor, Cost: nanoMoney(0)}
		if acc, ok := w.vendors[v.Vendor]; ok {
			row.Cost = nanoMoney(acc.cost)
			row.UsageTotals = usageFrom(acc.totals)
		}
		out[i] = row
	}
	return out
}

// peakOf 는 「최고 사용 시간대」를 고른다. 규칙은 PeakWindow 주석에 있다.
func peakOf(windows []UsageWindow) PeakWindow {
	best := -1
	for i, w := range windows {
		if w.Tokens <= 0 && w.Cost.NanoUSD <= 0 {
			continue
		}
		if best < 0 || busierThan(w, windows[best]) {
			best = i
		}
	}
	if best < 0 {
		return PeakWindow{Index: -1, Cost: nanoMoney(0)}
	}
	w := windows[best]
	return PeakWindow{
		Found:     true,
		Index:     best,
		StartAt:   w.StartAt,
		EndAt:     w.EndAt,
		LocalHour: w.LocalHour,
		Tokens:    w.Tokens,
		Cost:      w.Cost,
		Vendor:    topVendorOf(w.Vendors),
	}
}

// busierThan 은 a 가 b 보다 바쁜지다. 동률이면 false 라 **먼저 만난 창(이른 창)** 이 남는다.
func busierThan(a, b UsageWindow) bool {
	if a.Tokens != b.Tokens {
		return a.Tokens > b.Tokens
	}
	return a.Cost.NanoUSD > b.Cost.NanoUSD
}

// topVendorOf 는 창 안에서 토큰이 가장 많았던 벤더다. 사용량이 없으면 빈 문자열이다.
func topVendorOf(rows []VendorWindow) string {
	best := -1
	for i, r := range rows {
		if r.Tokens <= 0 && r.Cost.NanoUSD <= 0 {
			continue
		}
		if best < 0 || busierVendor(r, rows[best]) {
			best = i
		}
	}
	if best < 0 {
		return ""
	}
	return rows[best].Vendor
}

// busierVendor 는 topVendorOf 의 비교다. 동률은 이름 오름차순으로 가른다 — 목록이 이미
// 비용 순이라 그것에 기대면 토큰이 같은 두 벤더의 순서가 비용에 따라 흔들린다.
func busierVendor(a, b VendorWindow) bool {
	if a.Tokens != b.Tokens {
		return a.Tokens > b.Tokens
	}
	if a.Cost.NanoUSD != b.Cost.NanoUSD {
		return a.Cost.NanoUSD > b.Cost.NanoUSD
	}
	return a.Vendor < b.Vendor
}
