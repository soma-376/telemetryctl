package dashboard

import (
	"context"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/pricing"
)

func TestUsageRangeMatchesDailyTotals(t *testing.T) {
	f := newFixture(t)
	seedHomeBreakdown(f)
	ctx := context.Background()
	for _, tz := range []string{utc, seoul, kolkata, newYork} {
		t.Run(tz, func(t *testing.T) {
			loc, err := time.LoadLocation(tz)
			if err != nil {
				t.Fatal(err)
			}
			start := time.Date(2026, 8, 9, 0, 0, 0, 0, loc)
			bounds := []time.Time{start, start.AddDate(0, 0, 1), start.AddDate(0, 0, 2), start.AddDate(0, 0, 3)}
			got, err := ReadUsageBreakdown(ctx, f.db.SQL(), bounds)
			if err != nil {
				t.Fatal(err)
			}
			var tokens int64
			var cost pricing.NanoUSD
			for _, d := range bounds[:3] {
				day, err := f.reader.HomeBreakdown(ctx, HomeBreakdownQuery{TZ: tz, Date: d.Format(dateKey)})
				if err != nil {
					t.Fatal(err)
				}
				tokens += day.Totals.Tokens()
				cost += day.Cost.Total.NanoUSD
			}
			if tokens != got.Totals.Tokens() || cost != got.Cost.Total.NanoUSD {
				t.Fatalf("기간 합계 불일치: %+v", got)
			}
			var windowTokens int64
			var windowCost pricing.NanoUSD
			for _, w := range got.Windows {
				windowTokens += w.Tokens
				windowCost += w.Cost.NanoUSD
			}
			if windowTokens != tokens || windowCost != cost {
				t.Fatal("창 합계 불일치")
			}
			assertVendorsSumToDay(t, got)
			assertWindowVendorsSumToWindow(t, got)
			assertModelsSumToVendor(t, got)
			assertSharesSumToConstant(t, got)
		})
	}
}

func TestUsageRangeClipsPartialMonths(t *testing.T) {
	f := newFixture(t)
	seedHomeBreakdown(f)
	// 8/9 호출을 제외하고 8/10부터 담는다. 월 시작으로 넓히면 비용이 0.10 늘어난다.
	start := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	got, err := ReadUsageBreakdown(context.Background(), f.db.SQL(), []time.Time{start, start.AddDate(0, 1, 0)})
	if err != nil {
		t.Fatal(err)
	}
	day, err := f.reader.HomeBreakdown(context.Background(), HomeBreakdownQuery{TZ: utc, Date: "2026-08-10"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Cost.Total != day.Cost.Total || got.Totals.Tokens() != day.Totals.Tokens() {
		t.Fatal("선택 범위 밖의 호출을 포함했다")
	}
}
