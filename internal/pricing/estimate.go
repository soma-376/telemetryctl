// Package pricing 은 LLM 호출 한 건의 비용을 정한다.
//
// 순수 함수 패키지다 — 파일도, 네트워크도, 시계도 쓰지 않는다. 호출자가 llm_calls 한 행을
// Usage 로 넘기면 Result 를 돌려준다. 그래서 internal/event 처럼 표 주도 테스트만으로
// 규칙 전체를 고정할 수 있다.
//
// # 비용의 출처는 하나뿐이다
//
// 벤더가 cost_usd 를 보고한 호출은 **보고값이 이긴다.** 토큰 단가로 계산한 값은 보고값이
// 없을 때만 쓴다. 둘을 더하면 정확히 두 배가 된다 — internal/store/promote.go 가 경고하는
// "비용 10배" 와 같은 종류의 사고이고, 화면에 뜬 뒤에는 되짚을 근거가 남지 않는다.
//
// 그래서 Cost 에는 금액 필드가 **하나뿐**이다(Total). 보고값과 추정값을 서로 다른 필드에
// 담아 두면 언젠가 누군가 둘 다 더한다. 어느 쪽인지는 Source 가 말한다.
package pricing

import "github.com/your-org/pulsemetry/internal/event"

// Usage 는 llm_calls 한 행의 과금 입력이다. 값 없음(NULL)과 0 이 다르므로 event.Opt 를 쓴다.
//
// Opt 는 JSON 으로 나가지 않는다 — 이 타입은 저장소에서 들어오는 **입력**이고,
// GUI 로 나가는 표면은 Result 쪽이다.
type Usage struct {
	// Model 은 llm_calls.model 원본이다. 정규화는 이 패키지가 한다.
	Model string `json:"model"`

	InputTokens      event.Opt[int64] `json:"input_tokens"`
	OutputTokens     event.Opt[int64] `json:"output_tokens"`
	CacheReadTokens  event.Opt[int64] `json:"cache_read_tokens"`
	CacheWriteTokens event.Opt[int64] `json:"cache_write_tokens"`

	// ReasoningTokens 는 **출력 토큰의 부분집합**이다. 과금에 다시 더하지 않는다.
	// 쓰지 않는데도 받는 이유는, 호출자가 llm_calls 한 행을 그대로 넘길 수 있어야 하고
	// "왜 여기 없지" 하고 직접 더하는 일이 생기지 않게 하기 위해서다.
	ReasoningTokens event.Opt[int64] `json:"reasoning_tokens"`

	// ReportedCostUSD 는 벤더가 보고한 cost_usd 다. 있으면 이것이 비용이다.
	ReportedCostUSD event.Opt[float64] `json:"reported_cost_usd"`
}

// Source 는 비용이 어디서 왔는지다. 값은 셋 중 하나뿐이고, 둘이 동시에 성립하지 않는다.
type Source string

const (
	// SourceUnavailable 은 비용을 정할 수 없었다는 뜻이다. Total 은 항상 0 이고,
	// 화면은 "-" 로 그려야 한다. 0 원으로 그리면 비용이 과소 집계된 것처럼 보인다.
	SourceUnavailable Source = "unavailable"
	// SourceReported 는 벤더가 보고한 cost_usd 를 그대로 쓴 경우다.
	SourceReported Source = "reported"
	// SourceEstimated 는 보고값이 없어 토큰 단가로 계산한 경우다.
	SourceEstimated Source = "estimated"
)

// Reason 은 unavailable 의 기계 판독 가능한 사유다. 화면이 안내 문구를 고르고,
// 로그가 "모르는 모델이 늘고 있다" 를 셀 수 있어야 한다.
type Reason string

const (
	ReasonNone Reason = ""
	// ReasonNoModel 은 모델 이름 자체가 없는 경우다.
	ReasonNoModel Reason = "no_model"
	// ReasonUnknownModel 은 정규화한 이름이 가격표에 없는 경우다. 추측해서 채우지 않는다.
	ReasonUnknownModel Reason = "unknown_model"
	// ReasonMissingRate 는 표에 줄은 있으나 이 호출이 쓴 항목의 단가가 비어 있는 경우다.
	ReasonMissingRate Reason = "missing_rate"
	// ReasonNoUsage 는 보고 비용도 토큰도 없는 경우다. 계산할 것이 없다.
	ReasonNoUsage Reason = "no_usage"
)

// Components 는 추정 비용의 항목별 내역이다. 합이 Cost.Total 과 정확히 같다.
//
// SourceReported 일 때는 비어 있다 — 벤더가 내부 분해를 공개하지 않으므로 알 수 없고,
// 여기에 추정 내역을 채우면 보고 총액과 항목 합이 어긋난 채로 화면에 나간다.
type Components struct {
	Input  Money `json:"input"`
	Output Money `json:"output"`
	// CacheRead·CacheWrite 는 자기 단가로 매긴다. 입력 토큰에 다시 더하지 않는다.
	CacheRead  Money `json:"cache_read"`
	CacheWrite Money `json:"cache_write"`
}

// Cost 는 호출 한 건의 비용이다. 금액 필드는 Total 하나뿐이다 — 패키지 머리말 참조.
type Cost struct {
	Source Source `json:"source"`
	Reason Reason `json:"reason"`
	Total  Money  `json:"total"`
	// Components 는 Source 가 SourceEstimated 일 때만 채운다.
	Components Components `json:"components"`
}

// Countable 은 이 비용을 합계에 더해도 되는지다. 제로값 Cost 는 더하지 않는다.
func (c Cost) Countable() bool {
	return c.Source == SourceReported || c.Source == SourceEstimated
}

// Result 는 호출 한 건의 가격 산정 결과다.
type Result struct {
	Model   Model   `json:"model"`
	Cost    Cost    `json:"cost"`
	Pricing Applied `json:"pricing"`
}

// Estimate 는 기본 가격표로 산정한다.
func Estimate(u Usage) Result { return Default().Estimate(u) }

// Estimate 는 호출 한 건의 비용을 정한다.
func (t Table) Estimate(u Usage) Result {
	res := Result{
		Model:   Model{Raw: u.Model, Canonical: Canonical(u.Model)},
		Pricing: Applied{TableVersion: t.Version, EffectiveDate: t.EffectiveDate},
	}
	key, rate, known := t.Lookup(u.Model)
	res.Model.Known = known
	res.Pricing.RateKey = key
	res.Cost = cost(u, rate, known, res.Model.Canonical)
	return res
}

// cost 는 보고값 우선 규칙을 집행한다.
func cost(u Usage, rate Rate, known bool, canonical string) Cost {
	if reported, has := u.ReportedCostUSD.Get(); has {
		if n, ok := usdToNano(reported); ok {
			return Cost{Source: SourceReported, Total: money(n)}
		}
		// 금액으로 쓸 수 없는 보고값(NaN·무한대·음수)은 없었던 것으로 본다.
		// 그대로 0 으로 눕히면 "무료 호출" 과 구분되지 않는다.
	}

	if !known {
		return Cost{Source: SourceUnavailable, Reason: unknownReason(canonical)}
	}
	if !u.hasTokens() {
		return Cost{Source: SourceUnavailable, Reason: ReasonNoUsage}
	}

	in := tokens(u.InputTokens)
	out := tokens(u.OutputTokens)
	read := tokens(u.CacheReadTokens)
	write := tokens(u.CacheWriteTokens)

	if rateMissing(in, rate.InputPerMTokUSD) ||
		rateMissing(out, rate.OutputPerMTokUSD) ||
		rateMissing(read, rate.CacheReadPerMTokUSD) ||
		rateMissing(write, rate.CacheWritePerMTokUSD) {
		return Cost{Source: SourceUnavailable, Reason: ReasonMissingRate}
	}

	// 출력 단가에는 reasoning 토큰이 이미 포함돼 있다(llm_calls 문서). 다시 더하지 않는다.
	comp := Components{
		Input:      money(tokenCost(in, rate.InputPerMTokUSD)),
		Output:     money(tokenCost(out, rate.OutputPerMTokUSD)),
		CacheRead:  money(tokenCost(read, rate.CacheReadPerMTokUSD)),
		CacheWrite: money(tokenCost(write, rate.CacheWritePerMTokUSD)),
	}
	total := comp.Input.NanoUSD + comp.Output.NanoUSD + comp.CacheRead.NanoUSD + comp.CacheWrite.NanoUSD
	return Cost{Source: SourceEstimated, Total: money(total), Components: comp}
}

// unknownReason 은 "이름이 없다" 와 "이름은 있는데 표에 없다" 를 나눈다. 화면의 안내가 다르다.
func unknownReason(canonical string) Reason {
	if canonical == "" {
		return ReasonNoModel
	}
	return ReasonUnknownModel
}

// hasTokens 는 과금할 토큰이 하나라도 **보고됐는지**다. 값이 0 이어도 보고된 것은 보고된 것이다 —
// reasoning 은 세지 않는다. 그것만 있으면 과금할 항목이 없다.
func (u Usage) hasTokens() bool {
	return u.InputTokens.Valid() || u.OutputTokens.Valid() ||
		u.CacheReadTokens.Valid() || u.CacheWriteTokens.Valid()
}

// tokens 는 미설정과 이상값(음수)을 0 으로 눕힌다.
func tokens(o event.Opt[int64]) int64 {
	if v := o.Or(0); v > 0 {
		return v
	}
	return 0
}

// rateMissing 은 이 호출이 실제로 쓴 항목의 단가가 비었는지다. 쓰지 않은 항목의 빈 단가는
// 문제가 아니다 — 캐시를 안 쓴 호출까지 unavailable 로 만들면 화면의 비용이 통째로 사라진다.
func rateMissing(tokens int64, usdPerMTok float64) bool {
	return tokens > 0 && usdPerMTok <= 0
}
