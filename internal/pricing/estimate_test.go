package pricing

import (
	"math"
	"testing"

	"github.com/your-org/pulsemetry/internal/event"
)

// some 은 테이블을 읽기 좋게 만드는 별칭이다.
func some[T comparable](v T) event.Opt[T] { return event.Some(v) }

// sonnet46 은 표에서 고른 기준 모델이다 (입력 3 · 출력 15 · 캐시읽기 0.30 · 캐시쓰기 3.75).
const sonnet46 = "claude-sonnet-4-6"

// 비용 산정 규칙 전체를 한 표에 고정한다. 이 표가 인수조건이다 —
// 특히 "보고 비용과 계산 비용이 중복 합산되지 않는다".
func TestEstimateCost(t *testing.T) {
	tests := []struct {
		name    string
		usage   Usage
		want    Cost
		wantKey string
	}{
		{
			name: "보고 비용이 있으면 그것이 비용이다 — 토큰이 있어도 계산하지 않는다",
			usage: Usage{
				Model:           sonnet46,
				InputTokens:     some[int64](1_000),
				OutputTokens:    some[int64](500),
				ReportedCostUSD: some(0.25),
			},
			// 토큰으로 계산하면 10,500,000 nano 다. 보고값만 남아야 한다.
			want:    Cost{Source: SourceReported, Total: money(250_000_000)},
			wantKey: sonnet46,
		},
		{
			name: "보고 비용이 없을 때만 토큰 단가로 계산한다",
			usage: Usage{
				Model:            sonnet46,
				InputTokens:      some[int64](1_000),
				OutputTokens:     some[int64](500),
				CacheReadTokens:  some[int64](10_000),
				CacheWriteTokens: some[int64](2_000),
			},
			want: Cost{
				Source: SourceEstimated,
				Total:  money(21_000_000),
				Components: Components{
					Input:      money(3_000_000),
					Output:     money(7_500_000),
					CacheRead:  money(3_000_000),
					CacheWrite: money(7_500_000),
				},
			},
			wantKey: sonnet46,
		},
		{
			name: "reasoning 은 출력의 부분집합이라 다시 더하지 않는다",
			usage: Usage{
				Model:           sonnet46,
				OutputTokens:    some[int64](500),
				ReasoningTokens: some[int64](400),
			},
			want: Cost{
				Source:     SourceEstimated,
				Total:      money(7_500_000),
				Components: Components{Output: money(7_500_000)},
			},
			wantKey: sonnet46,
		},
		{
			name:  "보고 비용 0 은 유효한 금액이다 — 미보고와 다르다",
			usage: Usage{Model: sonnet46, InputTokens: some[int64](1_000), ReportedCostUSD: some(0.0)},
			want:  Cost{Source: SourceReported, Total: money(0)},

			wantKey: sonnet46,
		},
		{
			name:    "토큰 0 이 보고되면 비용 0 으로 계산된다",
			usage:   Usage{Model: sonnet46, InputTokens: some[int64](0), OutputTokens: some[int64](0)},
			want:    Cost{Source: SourceEstimated, Total: money(0)},
			wantKey: sonnet46,
		},
		{
			name:    "음수 토큰은 0 으로 눕힌다",
			usage:   Usage{Model: sonnet46, InputTokens: some[int64](-1_000), OutputTokens: some[int64](500)},
			want:    Cost{Source: SourceEstimated, Total: money(7_500_000), Components: Components{Output: money(7_500_000)}},
			wantKey: sonnet46,
		},
		{
			name:    "보고 비용도 토큰도 없으면 no_usage",
			usage:   Usage{Model: sonnet46},
			want:    Cost{Source: SourceUnavailable, Reason: ReasonNoUsage},
			wantKey: sonnet46,
		},
		{
			name:    "reasoning 만 있으면 과금할 항목이 없다",
			usage:   Usage{Model: sonnet46, ReasoningTokens: some[int64](400)},
			want:    Cost{Source: SourceUnavailable, Reason: ReasonNoUsage},
			wantKey: sonnet46,
		},
		{
			name:  "모르는 모델은 unavailable — 비슷한 모델의 단가를 쓰지 않는다",
			usage: Usage{Model: "claude-opus-9", InputTokens: some[int64](1_000)},
			want:  Cost{Source: SourceUnavailable, Reason: ReasonUnknownModel},
		},
		{
			name:  "모델 이름이 없으면 no_model",
			usage: Usage{InputTokens: some[int64](1_000)},
			want:  Cost{Source: SourceUnavailable, Reason: ReasonNoModel},
		},
		{
			name:  "모르는 모델이어도 보고 비용은 그대로 쓴다",
			usage: Usage{Model: "claude-opus-9", InputTokens: some[int64](1_000), ReportedCostUSD: some(0.5)},
			want:  Cost{Source: SourceReported, Total: money(500_000_000)},
		},
		{
			name:    "NaN 보고값은 없었던 것으로 보고 토큰으로 계산한다",
			usage:   Usage{Model: sonnet46, OutputTokens: some[int64](500), ReportedCostUSD: some(math.NaN())},
			want:    Cost{Source: SourceEstimated, Total: money(7_500_000), Components: Components{Output: money(7_500_000)}},
			wantKey: sonnet46,
		},
		{
			name:    "음수 보고값도 금액이 아니다",
			usage:   Usage{Model: sonnet46, OutputTokens: some[int64](500), ReportedCostUSD: some(-1.0)},
			want:    Cost{Source: SourceEstimated, Total: money(7_500_000), Components: Components{Output: money(7_500_000)}},
			wantKey: sonnet46,
		},
		{
			name:  "보고값이 금액이 아니고 토큰도 없으면 unavailable",
			usage: Usage{Model: sonnet46, ReportedCostUSD: some(math.Inf(1))},
			want:  Cost{Source: SourceUnavailable, Reason: ReasonNoUsage},

			wantKey: sonnet46,
		},
		{
			name:    "날짜 붙은 아이디도 같은 줄로 계산된다",
			usage:   Usage{Model: "claude-sonnet-4-6-20260101", OutputTokens: some[int64](500)},
			want:    Cost{Source: SourceEstimated, Total: money(7_500_000), Components: Components{Output: money(7_500_000)}},
			wantKey: sonnet46,
		},
	}

	table := Default()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := table.Estimate(tt.usage)
			if got.Cost != tt.want {
				t.Fatalf("Cost = %+v\nwant %+v", got.Cost, tt.want)
			}
			if got.Pricing.RateKey != tt.wantKey {
				t.Fatalf("RateKey = %q, want %q", got.Pricing.RateKey, tt.wantKey)
			}
			// 어느 판의 표로 계산했는지는 결과마다 항상 붙어 나간다.
			if got.Pricing.TableVersion != table.Version || got.Pricing.EffectiveDate != table.EffectiveDate {
				t.Fatalf("가격표 추적 정보가 비었다: %+v", got.Pricing)
			}
			if got.Model.Raw != tt.usage.Model {
				t.Fatalf("Model.Raw = %q, want %q", got.Model.Raw, tt.usage.Model)
			}
		})
	}
}

// 인수조건: 보고 비용과 계산 비용이 중복 합산되지 않는다.
//
// 타입이 그것을 구조적으로 막는다 — 금액 필드가 Total 하나뿐이라 "둘 다 더한다" 를
// 쓸 자리가 없다. 이 테스트는 그 성질이 유지되는지를 값으로 확인한다.
func TestReportedCostNeverAddsToEstimate(t *testing.T) {
	usage := Usage{
		Model:            sonnet46,
		InputTokens:      some[int64](1_000),
		OutputTokens:     some[int64](500),
		CacheReadTokens:  some[int64](10_000),
		CacheWriteTokens: some[int64](2_000),
	}

	estimated := Default().Estimate(usage).Cost
	withReported := usage
	withReported.ReportedCostUSD = some(0.25)
	reported := Default().Estimate(withReported).Cost

	if estimated.Source != SourceEstimated || reported.Source != SourceReported {
		t.Fatalf("출처가 갈리지 않았다: %v / %v", estimated.Source, reported.Source)
	}
	if reported.Total.NanoUSD != 250_000_000 {
		t.Fatalf("보고값이 그대로 나오지 않았다: %d", reported.Total.NanoUSD)
	}
	if reported.Total.NanoUSD == estimated.Total.NanoUSD+250_000_000 {
		t.Fatal("보고값과 추정값이 더해졌다")
	}
	if reported.Components != (Components{}) {
		t.Fatalf("보고 비용에 추정 내역이 채워졌다: %+v", reported.Components)
	}
}

// 캐시 토큰은 자기 단가로만 매긴다. 입력 토큰에 다시 더하면 캐시가 절감이 아니라 추가 비용이 된다.
func TestCacheTokensAreNotRepricedAsInput(t *testing.T) {
	base := Usage{Model: sonnet46, InputTokens: some[int64](1_000)}
	withCache := base
	withCache.CacheReadTokens = some[int64](100_000)

	baseCost := Default().Estimate(base).Cost
	cacheCost := Default().Estimate(withCache).Cost

	// 캐시 읽기 10만 토큰 × 0.30 USD/MTok = 30,000,000 nano.
	const wantDelta = NanoUSD(30_000_000)
	if got := cacheCost.Total.NanoUSD - baseCost.Total.NanoUSD; got != wantDelta {
		t.Fatalf("캐시 읽기 증가분 = %d, want %d (입력 단가로 매겨졌을 수 있다)", got, wantDelta)
	}
	if cacheCost.Components.Input != baseCost.Components.Input {
		t.Fatalf("캐시 토큰이 입력 항목에 섞였다: %+v", cacheCost.Components)
	}
}

// 표에 줄은 있으나 이 호출이 쓴 항목의 단가가 비면 unavailable 이다.
func TestMissingRateIsExplicit(t *testing.T) {
	// 캐시 단가가 없는 표를 만든다.
	partial := NewTable("test-v1", "2026-01-01", map[string]Rate{
		"partial-model": {InputPerMTokUSD: 3, OutputPerMTokUSD: 15},
	}, nil)

	tests := []struct {
		name  string
		usage Usage
		want  Cost
	}{
		{
			name:  "캐시를 쓰지 않은 호출은 캐시 단가가 없어도 계산된다",
			usage: Usage{Model: "partial-model", InputTokens: some[int64](1_000)},
			want: Cost{
				Source: SourceEstimated, Total: money(3_000_000),
				Components: Components{Input: money(3_000_000)},
			},
		},
		{
			name:  "캐시 읽기 토큰이 있는데 단가가 없으면 unavailable",
			usage: Usage{Model: "partial-model", InputTokens: some[int64](1_000), CacheReadTokens: some[int64](10)},
			want:  Cost{Source: SourceUnavailable, Reason: ReasonMissingRate},
		},
		{
			name:  "캐시 쓰기 토큰이 있는데 단가가 없어도 unavailable",
			usage: Usage{Model: "partial-model", CacheWriteTokens: some[int64](10)},
			want:  Cost{Source: SourceUnavailable, Reason: ReasonMissingRate},
		},
		{
			name:  "캐시 토큰이 0 으로 보고된 경우는 쓰지 않은 것이다",
			usage: Usage{Model: "partial-model", InputTokens: some[int64](1_000), CacheReadTokens: some[int64](0)},
			want: Cost{
				Source: SourceEstimated, Total: money(3_000_000),
				Components: Components{Input: money(3_000_000)},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := partial.Estimate(tt.usage).Cost; got != tt.want {
				t.Fatalf("Cost = %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

// unavailable 은 합계에서 0 이어야 한다. 화면이 실수로 더해도 총액이 틀어지지 않게 한다.
func TestUnavailableCostIsZeroAndNotCountable(t *testing.T) {
	got := Estimate(Usage{Model: "claude-opus-9", InputTokens: some[int64](1_000)}).Cost
	if got.Total.NanoUSD != 0 || got.Total.USD != 0 {
		t.Fatalf("unavailable 인데 금액이 있다: %+v", got.Total)
	}
	if got.Countable() {
		t.Fatal("unavailable 이 합산 대상이 됐다")
	}
	if (Cost{}).Countable() {
		t.Fatal("제로값 Cost 가 합산 대상이 됐다")
	}
	if !(Cost{Source: SourceReported}).Countable() || !(Cost{Source: SourceEstimated}).Countable() {
		t.Fatal("보고·추정 비용이 합산 대상에서 빠졌다")
	}
}

// 항목의 합은 총액과 **정확히** 같아야 한다. 화면이 내역과 합계를 함께 그리기 때문이다.
func TestComponentsSumToTotal(t *testing.T) {
	usages := []Usage{
		{Model: sonnet46, InputTokens: some[int64](7), OutputTokens: some[int64](13)},
		{Model: "claude-haiku-4-5", InputTokens: some[int64](999_999), CacheReadTokens: some[int64](3)},
		{Model: "gpt-5-codex", OutputTokens: some[int64](1), CacheWriteTokens: some[int64](123_457)},
	}

	for _, u := range usages {
		t.Run(u.Model, func(t *testing.T) {
			c := Default().Estimate(u).Cost
			sum := c.Components.Input.NanoUSD + c.Components.Output.NanoUSD +
				c.Components.CacheRead.NanoUSD + c.Components.CacheWrite.NanoUSD
			if sum != c.Total.NanoUSD {
				t.Fatalf("항목 합 %d != 총액 %d", sum, c.Total.NanoUSD)
			}
		})
	}
}

// 호출을 어떻게 묶어 더해도 총합이 같아야 한다 — Home 카드와 세션 상세가 어긋나지 않는 조건이다.
func TestTotalsAreGroupingIndependent(t *testing.T) {
	usages := make([]Usage, 0, 200)
	for i := int64(1); i <= 200; i++ {
		usages = append(usages, Usage{
			Model:           sonnet46,
			InputTokens:     some(i * 7),
			OutputTokens:    some(i * 3),
			CacheReadTokens: some(i * 101),
		})
	}

	var all NanoUSD
	for _, u := range usages {
		all += Default().Estimate(u).Cost.Total.NanoUSD
	}

	var evens, odds NanoUSD
	for i, u := range usages {
		if i%2 == 0 {
			evens += Default().Estimate(u).Cost.Total.NanoUSD
		} else {
			odds += Default().Estimate(u).Cost.Total.NanoUSD
		}
	}

	if evens+odds != all {
		t.Fatalf("묶는 방식이 합을 바꿨다: %d != %d", evens+odds, all)
	}
}
