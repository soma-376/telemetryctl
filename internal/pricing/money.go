package pricing

import "math"

// 금액 표현.
//
// # 왜 정수 nano-USD 인가
//
// Home 카드의 합계와 세션별 값의 합이 어긋나면 사용자는 둘 다 믿지 않는다. 단가×토큰을
// float64 로 곱해 더하면 더하는 순서에 따라 마지막 자리가 흔들리고, 세션이 수천 개 쌓이면
// 그 차이가 표시 자릿수까지 올라올 수 있다.
//
// 그래서 계산은 전부 **정수 nano-USD**(10억분의 1 USD)로 한다. 공시 단가는 모두
// 0.001 USD/MTok 의 배수이므로 "MTok 당 USD" 를 "토큰 당 nano-USD" 로 옮길 때 나머지가
// 없다 — 0.001 USD/MTok 이 정확히 1 nano/token 이다. 곱셈도 덧셈도 정수라 결합법칙이
// 성립하고, 어떤 순서로 더해도 합이 같다.
//
// # 반올림 규칙
//
// 이 패키지는 결과를 반올림하지 않는다. 반올림은 두 군데뿐이다.
//
//   - 표의 단가를 토큰당 nano 로 옮길 때 (math.Round, 0.5 는 0 에서 먼 쪽).
//     공시 단가가 0.001 USD/MTok 의 배수인 한 반올림할 것이 없다.
//   - 벤더가 float 로 보고한 cost_usd 를 nano 로 옮길 때 (같은 규칙). 나노 자리에서
//     일어나므로 USD 로 보면 10억분의 1 미만이다.
//
// 표시용 반올림(소수 둘째 자리 등)은 **화면이 마지막에 한 번만** 한다. 여기서 미리 자르면
// 잘린 값들을 더한 합계가 전체 합계와 달라진다.

const (
	// nanoPerUSD 는 1 USD 의 nano-USD 값이다.
	nanoPerUSD = 1_000_000_000
	// tokensPerMTok 은 단가의 기준 단위다. 공시 단가는 100만 토큰당 USD 로 발표된다.
	tokensPerMTok = 1_000_000
	// nanoPerMTokUSD 는 "MTok 당 1 USD" 를 "토큰 당 nano-USD" 로 옮기는 계수다.
	// nanoPerUSD / tokensPerMTok = 1000 이다.
	nanoPerMTokUSD = nanoPerUSD / tokensPerMTok
)

// NanoUSD 는 10억분의 1 USD 단위의 정수 금액이다. int64 의 범위는 약 92 억 USD 로,
// 이 도구가 다루는 개인·팀 단위 비용에 비하면 사실상 무한하다.
type NanoUSD int64

// USD 는 화면 표시용 실수 값이다. 계산에는 쓰지 않는다.
func (n NanoUSD) USD() float64 { return float64(n) / nanoPerUSD }

// Money 는 금액 하나다. NanoUSD 가 원본이고 USD 는 화면용 파생값이다.
//
// 둘을 함께 내보내는 이유는 소비자가 둘로 갈리기 때문이다 — GUI 는 USD 만 쓰고,
// 여러 호출을 합산하는 쪽(Today 카드·세션 합계)은 NanoUSD 를 정수로 더해야 오차가 없다.
// **합산은 반드시 NanoUSD 로 한다.**
type Money struct {
	NanoUSD NanoUSD `json:"nano_usd"`
	USD     float64 `json:"usd"`
}

// money 는 이 패키지가 Money 를 만드는 유일한 경로다. USD 파생을 한 곳에 모아 둔다.
func money(n NanoUSD) Money { return Money{NanoUSD: n, USD: n.USD()} }

// perTokenNano 는 100만 토큰당 USD 단가를 토큰 하나당 nano-USD 로 옮긴다.
//
// 0.001 USD/MTok 보다 잔 단가는 공시된 적이 없다. 그런 값이 표에 들어오면 여기서
// 반올림되고, 그 사실은 표를 고치는 사람이 테스트로 확인해야 한다.
func perTokenNano(usdPerMTok float64) NanoUSD {
	if usdPerMTok <= 0 || math.IsNaN(usdPerMTok) || math.IsInf(usdPerMTok, 0) {
		return 0
	}
	return NanoUSD(math.Round(usdPerMTok * nanoPerMTokUSD))
}

// tokenCost 는 토큰 수 × 토큰당 단가다.
//
// 음수 토큰은 0 으로 본다. 벤더가 음수를 보고할 이유는 없지만, 그대로 곱하면 비용이
// 깎여 다른 호출의 정상 비용까지 지워진다 — 이상값은 세지 않는 쪽이 안전하다.
func tokenCost(tokens int64, usdPerMTok float64) NanoUSD {
	if tokens <= 0 {
		return 0
	}
	return NanoUSD(tokens) * perTokenNano(usdPerMTok)
}

// usdToNano 는 벤더가 보고한 USD 금액을 nano 로 옮긴다.
//
// ok=false 는 **금액으로 쓸 수 없는 값**이다 — NaN·무한대·음수·int64 범위 초과.
// 호출자는 이 경우 보고값이 없었던 것처럼 다뤄야 한다. 그대로 0 으로 눕히면
// "비용 0 인 호출" 과 "비용을 못 읽은 호출" 이 같아진다.
func usdToNano(usd float64) (NanoUSD, bool) {
	if math.IsNaN(usd) || math.IsInf(usd, 0) || usd < 0 {
		return 0, false
	}
	n := math.Round(usd * nanoPerUSD)
	if n > float64(math.MaxInt64) {
		return 0, false
	}
	return NanoUSD(n), true
}
