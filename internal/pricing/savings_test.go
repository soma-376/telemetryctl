package pricing

import "testing"

// 인수조건: 캐시 read/write 각각의 절감 계산이 표로 검증된다.
//
// 기준 단가(claude-sonnet-4-6): 입력 3 · 캐시읽기 0.30 · 캐시쓰기 3.75 USD/MTok.
// 토큰당 nano 로는 3000 · 300 · 3750 이다.
func TestCacheSavings(t *testing.T) {
	tests := []struct {
		name  string
		usage Usage
		want  Savings
	}{
		{
			name:  "캐시 읽기는 입력 단가와의 차이만큼 절감이다",
			usage: Usage{Model: sonnet46, CacheReadTokens: some[int64](100_000)},
			// 100,000 × (3000 - 300) = 270,000,000
			want: Savings{Available: true, Read: money(270_000_000), Total: money(270_000_000)},
		},
		{
			name:  "캐시 쓰기는 입력보다 비싸 절감액이 음수다 — 다음 읽기를 위한 선투자다",
			usage: Usage{Model: sonnet46, CacheWriteTokens: some[int64](2_000)},
			// 2,000 × (3000 - 3750) = -1,500,000
			want: Savings{Available: true, Write: money(-1_500_000), Total: money(-1_500_000)},
		},
		{
			name: "read 와 write 를 각각 계산하고 합을 Total 로 둔다",
			usage: Usage{
				Model:            sonnet46,
				CacheReadTokens:  some[int64](100_000),
				CacheWriteTokens: some[int64](2_000),
			},
			want: Savings{
				Available: true,
				Read:      money(270_000_000),
				Write:     money(-1_500_000),
				Total:     money(268_500_000),
			},
		},
		{
			name: "쓰기 요금이 입력과 같은 벤더는 쓰기 절감이 0 이다",
			// gpt-5-codex: 입력 1.25 · 캐시읽기 0.125 · 캐시쓰기 1.25 USD/MTok
			usage: Usage{
				Model:            "gpt-5-codex",
				CacheReadTokens:  some[int64](1_000_000),
				CacheWriteTokens: some[int64](1_000_000),
			},
			want: Savings{
				Available: true,
				Read:      money(1_125_000_000),
				Write:     money(0),
				Total:     money(1_125_000_000),
			},
		},
		{
			name:  "캐시를 쓰지 않은 호출의 절감액은 0 이다 — unavailable 이 아니다",
			usage: Usage{Model: sonnet46, InputTokens: some[int64](1_000)},
			want:  Savings{Available: true},
		},
		{
			name:  "캐시 토큰이 0 으로 보고돼도 0 절감이다",
			usage: Usage{Model: sonnet46, CacheReadTokens: some[int64](0), CacheWriteTokens: some[int64](0)},
			want:  Savings{Available: true},
		},
		{
			name:  "음수 캐시 토큰은 0 으로 눕힌다",
			usage: Usage{Model: sonnet46, CacheReadTokens: some[int64](-100)},
			want:  Savings{Available: true},
		},
		{
			name:  "보고 비용이 있어도 절감액은 표에서 계산한다 — 반사실이라 보고값과 무관하다",
			usage: Usage{Model: sonnet46, CacheReadTokens: some[int64](100_000), ReportedCostUSD: some(0.25)},
			want:  Savings{Available: true, Read: money(270_000_000), Total: money(270_000_000)},
		},
		{
			name:  "모르는 모델은 절감액도 알 수 없다",
			usage: Usage{Model: "claude-opus-9", CacheReadTokens: some[int64](100_000)},
			want:  Savings{Reason: ReasonUnknownModel},
		},
		{
			name:  "모델 이름이 없으면 no_model",
			usage: Usage{CacheReadTokens: some[int64](100_000)},
			want:  Savings{Reason: ReasonNoModel},
		},
		{
			name:  "보고 비용이 있어도 모르는 모델이면 절감액은 unavailable 이다",
			usage: Usage{Model: "claude-opus-9", CacheReadTokens: some[int64](100_000), ReportedCostUSD: some(0.25)},
			want:  Savings{Reason: ReasonUnknownModel},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Default().Estimate(tt.usage).CacheSavings
			if got != tt.want {
				t.Fatalf("CacheSavings = %+v\nwant %+v", got, tt.want)
			}
			if got.Total.NanoUSD != got.Read.NanoUSD+got.Write.NanoUSD {
				t.Fatalf("Total 이 Read+Write 와 다르다: %+v", got)
			}
			if !got.Available && (got.Read.NanoUSD != 0 || got.Write.NanoUSD != 0 || got.Total.NanoUSD != 0) {
				t.Fatalf("unavailable 인데 금액이 있다: %+v", got)
			}
		})
	}
}

// 단가가 비면 절감액도 명시적으로 unavailable 이다.
func TestCacheSavingsMissingRate(t *testing.T) {
	noCacheRates := NewTable("test-v1", "2026-01-01", map[string]Rate{
		"partial-model": {InputPerMTokUSD: 3, OutputPerMTokUSD: 15},
	}, nil)
	noInputRate := NewTable("test-v1", "2026-01-01", map[string]Rate{
		"output-only": {OutputPerMTokUSD: 15, CacheReadPerMTokUSD: 0.30, CacheWritePerMTokUSD: 3.75},
	}, nil)

	tests := []struct {
		name  string
		table Table
		usage Usage
		want  Savings
	}{
		{
			name:  "캐시 읽기 단가가 없으면 unavailable",
			table: noCacheRates,
			usage: Usage{Model: "partial-model", CacheReadTokens: some[int64](10)},
			want:  Savings{Reason: ReasonMissingRate},
		},
		{
			name:  "캐시 쓰기 단가가 없어도 unavailable",
			table: noCacheRates,
			usage: Usage{Model: "partial-model", CacheWriteTokens: some[int64](10)},
			want:  Savings{Reason: ReasonMissingRate},
		},
		{
			name:  "캐시를 쓰지 않았으면 캐시 단가가 없어도 0 절감이다",
			table: noCacheRates,
			usage: Usage{Model: "partial-model", InputTokens: some[int64](1_000)},
			want:  Savings{Available: true},
		},
		{
			name:  "비교 기준인 입력 단가가 없으면 절감액을 물을 수 없다",
			table: noInputRate,
			usage: Usage{Model: "output-only", CacheReadTokens: some[int64](10)},
			want:  Savings{Reason: ReasonMissingRate},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.table.Estimate(tt.usage).CacheSavings; got != tt.want {
				t.Fatalf("CacheSavings = %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

// 절감액은 "안 썼다면 냈을 비용" 과 "실제 비용" 의 차이여야 한다. 두 값을 따로 계산해 확인한다.
func TestCacheSavingsMatchesCounterfactual(t *testing.T) {
	const readTokens = 123_457
	actual := Usage{Model: sonnet46, CacheReadTokens: some[int64](readTokens)}
	// 같은 토큰이 캐시가 아니라 보통의 입력이었다면.
	counterfactual := Usage{Model: sonnet46, InputTokens: some[int64](readTokens)}

	actualCost := Default().Estimate(actual).Cost.Total.NanoUSD
	fullCost := Default().Estimate(counterfactual).Cost.Total.NanoUSD
	saved := Default().Estimate(actual).CacheSavings.Read.NanoUSD

	if saved != fullCost-actualCost {
		t.Fatalf("절감액 %d != 입력가 %d - 실제 %d", saved, fullCost, actualCost)
	}
}
