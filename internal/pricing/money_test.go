package pricing

import (
	"math"
	"testing"
)

// 단가 → 토큰당 nano 변환은 모든 금액의 뿌리다. 여기서 한 자리가 밀리면 화면의 모든 비용이
// 같은 배수로 밀린다.
func TestPerTokenNano(t *testing.T) {
	tests := []struct {
		name       string
		usdPerMTok float64
		want       NanoUSD
	}{
		{name: "3 USD/MTok 은 토큰당 3000 nano", usdPerMTok: 3, want: 3000},
		{name: "0.001 USD/MTok 은 토큰당 1 nano — 공시 단가의 최소 자릿수", usdPerMTok: 0.001, want: 1},
		{name: "0.075 USD/MTok 은 토큰당 75 nano", usdPerMTok: 0.075, want: 75},
		{name: "0.5 USD/MTok 은 토큰당 500 nano", usdPerMTok: 0.5, want: 500},
		{name: "75 USD/MTok 은 토큰당 75000 nano", usdPerMTok: 75, want: 75000},
		{name: "0 단가는 0 — 표에 단가가 없다는 뜻이다", usdPerMTok: 0, want: 0},
		{name: "음수 단가는 0 으로 눕힌다", usdPerMTok: -1, want: 0},
		{name: "NaN 단가는 0 으로 눕힌다", usdPerMTok: math.NaN(), want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := perTokenNano(tt.usdPerMTok); got != tt.want {
				t.Fatalf("perTokenNano(%v) = %d, want %d", tt.usdPerMTok, got, tt.want)
			}
		})
	}
}

func TestTokenCost(t *testing.T) {
	tests := []struct {
		name       string
		tokens     int64
		usdPerMTok float64
		want       NanoUSD
	}{
		{name: "1M 토큰 × 3 USD/MTok = 3 USD", tokens: 1_000_000, usdPerMTok: 3, want: 3 * nanoPerUSD},
		{name: "토큰 1개도 나머지 없이 떨어진다", tokens: 1, usdPerMTok: 3, want: 3000},
		{name: "0 토큰은 0", tokens: 0, usdPerMTok: 3, want: 0},
		{name: "음수 토큰은 0 — 이상값이 다른 호출의 비용을 깎지 못하게 한다", tokens: -100, usdPerMTok: 3, want: 0},
		{name: "단가가 없으면 0", tokens: 1_000_000, usdPerMTok: 0, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tokenCost(tt.tokens, tt.usdPerMTok); got != tt.want {
				t.Fatalf("tokenCost(%d, %v) = %d, want %d", tt.tokens, tt.usdPerMTok, got, tt.want)
			}
		})
	}
}

func TestUSDToNano(t *testing.T) {
	tests := []struct {
		name string
		usd  float64
		want NanoUSD
		ok   bool
	}{
		{name: "보통의 보고 비용", usd: 0.0123, want: 12_300_000, ok: true},
		{name: "0 은 유효한 금액이다 — 무료 호출과 미보고는 다르다", usd: 0, want: 0, ok: true},
		{name: "나노 미만은 0 에서 먼 쪽으로 반올림한다", usd: 0.0000000015, want: 2, ok: true},
		{name: "NaN 은 금액이 아니다", usd: math.NaN(), ok: false},
		{name: "무한대는 금액이 아니다", usd: math.Inf(1), ok: false},
		{name: "음수 비용은 금액이 아니다", usd: -0.01, ok: false},
		{name: "int64 를 넘는 금액은 담지 않는다", usd: 1e12, ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := usdToNano(tt.usd)
			if ok != tt.ok {
				t.Fatalf("usdToNano(%v) ok = %v, want %v", tt.usd, ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Fatalf("usdToNano(%v) = %d, want %d", tt.usd, got, tt.want)
			}
		})
	}
}

// 정수 nano 를 고른 이유가 이것이다 — 더하는 순서를 바꿔도 합이 **정확히** 같아야
// Home 카드의 총합과 세션별 값의 합이 어긋나지 않는다.
func TestNanoSumIsOrderIndependent(t *testing.T) {
	const rate = 0.075 // 캐시 읽기 단가처럼 잔 단가로 확인한다.
	tokens := []int64{7, 13, 101, 999, 1_000_003, 37, 5}

	var forward NanoUSD
	for _, n := range tokens {
		forward += tokenCost(n, rate)
	}
	var backward NanoUSD
	for i := len(tokens) - 1; i >= 0; i-- {
		backward += tokenCost(tokens[i], rate)
	}
	var whole int64
	for _, n := range tokens {
		whole += n
	}

	if forward != backward {
		t.Fatalf("더하는 순서가 합을 바꿨다: %d != %d", forward, backward)
	}
	if got := tokenCost(whole, rate); got != forward {
		t.Fatalf("토큰을 먼저 합친 값과 호출별 합이 다르다: %d != %d", got, forward)
	}
}

func TestMoneyUSDIsDerived(t *testing.T) {
	m := money(1_234_500_000)
	if m.NanoUSD != 1_234_500_000 {
		t.Fatalf("NanoUSD = %d", m.NanoUSD)
	}
	if m.USD != 1.2345 {
		t.Fatalf("USD = %v, want 1.2345", m.USD)
	}
}
