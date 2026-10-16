package dashboard

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/event"
	"github.com/your-org/pulsemetry/internal/session"
	"github.com/your-org/pulsemetry/internal/store"
)

// PROJ-89 의 인수조건 세 가지를 이 파일이 지킨다.
//
//  1. **시계열 합계가 Home 일간 총합과 일치한다** — Home 을 직접 불러 대조한다.
//     금액은 float 이 아니라 정수 nano-USD 로 비교한다.
//  2. 벤더 점유율 합계와 반올림 규칙이 일관된다 (share_test.go 가 규칙 자체를 고정한다).
//  3. 데이터가 한 벤더에만 있어도 화면 계약이 유지된다.

// kolkata 는 UTC+5:30 이다. 현지 자정이 UTC 정시 버킷 한가운데에 떨어져, 집계의 시간
// 버킷과 창 경계가 어긋나는 시간대다 (home_breakdown.go 의 「버킷 경계」).
const kolkata = "Asia/Kolkata"

// modelOpusDated 는 날짜 꼬리가 붙은 표기다. 정규화하면 claude-opus-4-5 로 모인다.
const (
	modelOpusDated = "claude-opus-4-5-20251101"
	modelOpus      = "claude-opus-4-5"
	modelHaiku     = "claude-haiku-4-5"
	modelCodex     = "gpt-5-codex"
	modelCodex51   = "gpt-5.1-codex"
)

// seedHomeBreakdown 은 분해 조회의 씨앗이다.
//
// 두 벤더 · 다섯 모델 · 비용 산정의 세 갈래(보고·추정·불가)를 한 날에 모으고, 도구 호출을
// 한 턴에 세 건 매달아 출처 간 곱셈이 있으면 드러나게 한다. 시각을 UTC 로 흩뿌린 것은
// 시간대를 바꾸면 다른 하루에 들어가게 하기 위해서다 — 서울(UTC+9)의 8/10 은 UTC 로
// 8/9 15:00 에 시작한다.
func seedHomeBreakdown(f *fixture) {
	at := func(h, m int) time.Time { return time.Date(2026, 8, 10, h, m, 0, 0, time.UTC) }
	prev := time.Date(2026, 8, 9, 20, 0, 0, 0, time.UTC)

	recs := []store.EventRecord{
		// 보고값이 있는 호출 — 토큰 단가와 무관하게 2.5 가 아니라 1.5 가 비용이다.
		llmRecord("s-cc-1", "t-a", at(1, 10), 1, llmSpec{
			Model: modelSonnet, Cost: 1.5, Input: 1000, Output: 200,
		}),
		// 보고값이 없는 호출 — 단가로 메운다. 입력 5.0 + 출력 2.5 + 캐시읽기 0.25 + 캐시쓰기 1.25 = 9.0
		noCost(llmRecord("s-cc-1", "t-a", at(1, 20), 2, llmSpec{
			Model: modelOpusDated, Input: 1_000_000, Output: 100_000,
			CacheRead: 500_000, CacheWrit: 200_000,
		})),
		// 모르는 모델 — 비용을 정하지 못한다. 합계에 0 원으로도 들어가지 않는다.
		noCost(llmRecord("s-cc-2", "t-b", at(9, 5), 10, llmSpec{
			Model: "made-up-model-9", Input: 100,
		})),
		llmRecord("s-cc-2", "t-b", at(9, 40), 11, llmSpec{
			Model: modelSonnet, Cost: 0.25, Input: 500, Output: 100,
		}),
		// codex. 입력 0.25 + 출력 0.5 = 0.75
		noCost(llmRecord("s-cx-1", "t-c", at(15, 0), 20, llmSpec{
			Vendor: vendorCodex, Model: modelCodex, Input: 200_000, Output: 50_000,
		})),
		llmRecord("s-cx-1", "t-c", at(22, 30), 21, llmSpec{
			Vendor: vendorCodex, Model: modelCodex51, Cost: 3.75, Input: 1000, Output: 2000,
		}),
		// 전날(UTC)이지만 서울에서는 같은 8/10 이다.
		llmRecord("s-cc-0", "t-p", prev.Add(10*time.Minute), 30, llmSpec{
			Model: modelHaiku, Cost: 0.10, Input: 300, Output: 60,
		}),
	}
	// 한 턴에 매달린 도구 호출 셋. JOIN 으로 묶은 구현은 여기서 비용이 3배가 된다.
	for i := range 3 {
		recs = append(recs, toolRecord("s-cc-1", "t-a", "call-bd-"+string(rune('a'+i)),
			at(1, 30), 40+i, toolSpec{
				ToolName: "Edit",
				Success:  event.Some(true),
				Target:   workspaceA + "/apply.go",
				File:     fileChange(workspaceA+"/apply.go", 4, 1),
			}))
	}

	f.write(store.Batch{
		Sessions: []session.Session{
			newSession("s-cc-1", at(1, 10), func(s *session.Session) { s.ActiveSeconds = 300 }),
			newSession("s-cc-2", at(9, 5), func(s *session.Session) { s.ActiveSeconds = 120 }),
			newSession("s-cx-1", at(15, 0), codex, func(s *session.Session) { s.ActiveSeconds = 60 }),
			newSession("s-cc-0", prev, func(s *session.Session) { s.ActiveSeconds = 90 }),
		},
		Events: recs,
	})
}

// ★ 인수조건: 시계열 합계가 Home 일간 총합과 일치한다.
//
// 두 조회를 같은 날짜·시간대로 부르고 **Home 이 낸 값 자체**와 대조한다. 기대값을 손으로
// 적어 두면 두 구현이 함께 틀렸을 때 통과하고, 그것이 이 인수조건이 막으려는 사고다.
//
// 시간대를 여섯 가지로 도는 이유는 하루 경계가 계산의 유일한 위험 지점이기 때문이다 —
// 정시 오프셋 · 음수 오프셋 · DST 전환일(25시간) · 30분 오프셋 · 데이터가 없는 날.
func TestHomeBreakdownSeriesMatchesHomeDayTotals(t *testing.T) {
	f := newFixture(t)
	seedHomeBreakdown(f)
	ctx := context.Background()

	cases := []struct {
		name string
		tz   string
		date string
	}{
		{name: "UTC", tz: utc, date: "2026-08-10"},
		{name: "서울은 UTC 전날 15시에 하루가 시작한다", tz: seoul, date: "2026-08-10"},
		{name: "뉴욕은 오프셋이 음수다", tz: newYork, date: "2026-08-10"},
		{name: "뉴욕 가을 DST 전환일은 25시간이라 창이 13개다", tz: newYork, date: "2026-11-01"},
		{name: "콜카타는 하루 경계가 정시가 아니다", tz: kolkata, date: "2026-08-10"},
		{name: "활동이 없는 날", tz: utc, date: "2026-08-01"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := time.LoadLocation(tc.tz); err != nil {
				t.Skipf("%s 시간대를 못 읽는다: %v", tc.tz, err)
			}
			home, err := f.reader.Home(ctx, HomeQuery{TZ: tc.tz, Date: tc.date})
			if err != nil {
				t.Fatalf("Home: %v", err)
			}
			bd, err := f.reader.HomeBreakdown(ctx, HomeBreakdownQuery{TZ: tc.tz, Date: tc.date})
			if err != nil {
				t.Fatalf("HomeBreakdown: %v", err)
			}

			assertSameDay(t, bd, home)
			assertWindowsMatchHome(t, bd, home)
			assertVendorsSumToDay(t, bd)
			assertWindowVendorsSumToWindow(t, bd)
			assertModelsSumToVendor(t, bd)
			assertSharesSumToConstant(t, bd)
		})
	}
}

// assertSameDay 는 구간과 하루 합계가 Home 과 같은지 본다.
func assertSameDay(t *testing.T, bd HomeBreakdown, home HomeSummary) {
	t.Helper()
	if bd.TZ != home.TZ || bd.Date != home.Date {
		t.Errorf("시간대·날짜 = %s/%s, Home = %s/%s", bd.TZ, bd.Date, home.TZ, home.Date)
	}
	if bd.StartAt != home.StartAt || bd.EndAt != home.EndAt {
		t.Fatalf("구간 = [%d,%d), Home = [%d,%d) — 하루 경계가 다르면 나머지 비교가 무의미하다",
			bd.StartAt, bd.EndAt, home.StartAt, home.EndAt)
	}
	if !reflect.DeepEqual(bd.Totals, home.Totals) {
		t.Errorf("하루 집계가 다르다:\n분해 = %+v\nHome = %+v", bd.Totals, home.Totals)
	}
	// 금액은 정수 nano-USD 로 비교한다. float 으로 비교하면 더하는 순서가 달라 생긴 차이를
	// 놓친다 — 순서에 무관해야 한다는 것이 이 불변식의 요점이다.
	if !reflect.DeepEqual(bd.Cost, home.Cost) {
		t.Errorf("하루 예상 비용이 다르다:\n분해 = %+v\nHome = %+v", bd.Cost, home.Cost)
	}
}

// assertWindowsMatchHome 은 창이 Home 의 2시간 창과 같은지, 그리고 창 합계가 하루 합계와
// 같은지 본다. 두 위젯이 같은 화면에 나란히 놓이므로 창 하나라도 갈리면 안 된다.
func assertWindowsMatchHome(t *testing.T, bd HomeBreakdown, home HomeSummary) {
	t.Helper()
	if len(bd.Windows) != len(home.TwoHour.Windows) {
		t.Fatalf("창 = %d개, Home = %d개", len(bd.Windows), len(home.TwoHour.Windows))
	}

	var (
		tokens int64
		nano   int64
		active float64
	)
	for i, w := range bd.Windows {
		hw := home.TwoHour.Windows[i]
		switch {
		case w.StartAt != hw.StartAt || w.EndAt != hw.EndAt || w.LocalHour != hw.LocalHour:
			t.Errorf("창 %d 의 경계 = [%d,%d) %02d시, Home = [%d,%d) %02d시",
				i, w.StartAt, w.EndAt, w.LocalHour, hw.StartAt, hw.EndAt, hw.LocalHour)
		case w.Active != hw.Active:
			t.Errorf("창 %d 의 Active = %v, Home = %v", i, w.Active, hw.Active)
		case w.Tokens != hw.Tokens:
			t.Errorf("창 %d 의 토큰 = %d, Home = %d", i, w.Tokens, hw.Tokens)
		case w.Cost.NanoUSD != hw.Cost.NanoUSD:
			t.Errorf("창 %d 의 비용 = %d nano, Home = %d nano", i, w.Cost.NanoUSD, hw.Cost.NanoUSD)
		case w.ActiveSeconds != hw.ActiveSeconds:
			t.Errorf("창 %d 의 활동 시간 = %v, Home = %v", i, w.ActiveSeconds, hw.ActiveSeconds)
		}
		tokens += w.Tokens
		nano += int64(w.Cost.NanoUSD)
		active += w.ActiveSeconds
	}

	if tokens != bd.Totals.Tokens() {
		t.Errorf("창 토큰 합 = %d, 하루 = %d", tokens, bd.Totals.Tokens())
	}
	if nano != int64(bd.Cost.Total.NanoUSD) {
		t.Errorf("창 비용 합 = %d nano, 하루 = %d nano", nano, bd.Cost.Total.NanoUSD)
	}
	if active != bd.Totals.ActiveSeconds {
		t.Errorf("창 활동 시간 합 = %v, 하루 = %v", active, bd.Totals.ActiveSeconds)
	}
}

// assertVendorsSumToDay 는 벤더 줄의 합이 하루 합계와 같은지 본다.
func assertVendorsSumToDay(t *testing.T, bd HomeBreakdown) {
	t.Helper()
	var (
		sum  UsageTotals
		cost costAccumulator
	)
	for _, v := range bd.Vendors {
		addUsage(&sum, v.UsageTotals)
		cost.total += v.Cost.Total.NanoUSD
		cost.calls += v.Cost.Calls
		cost.reported += v.Cost.Reported
		cost.estimated += v.Cost.Estimated
		cost.unavailable += v.Cost.Unavailable
		// 집계와 행 스캔이 같은 구간을 잘랐는지 — 두 출처가 같은 값을 내야 한다.
		if v.APIRequests != v.Cost.Calls {
			t.Errorf("%s 의 api_requests = %d, Cost.Calls = %d — 두 출처가 구간을 다르게 잘랐다",
				v.Vendor, v.APIRequests, v.Cost.Calls)
		}
	}

	want := usageFrom(bd.Totals)
	if !reflect.DeepEqual(sum, want) {
		t.Errorf("벤더 합 = %+v, 하루 = %+v", sum, want)
	}
	counts := []struct {
		name     string
		got, exp int64
	}{
		{name: "비용(nano)", got: int64(cost.total), exp: int64(bd.Cost.Total.NanoUSD)},
		{name: "calls", got: cost.calls, exp: bd.Cost.Calls},
		{name: "reported", got: cost.reported, exp: bd.Cost.Reported},
		{name: "estimated", got: cost.estimated, exp: bd.Cost.Estimated},
		{name: "unavailable", got: cost.unavailable, exp: bd.Cost.Unavailable},
	}
	for _, c := range counts {
		if c.got != c.exp {
			t.Errorf("벤더 %s 합 = %d, 하루 = %d", c.name, c.got, c.exp)
		}
	}
}

// assertWindowVendorsSumToWindow 는 창 안 벤더 줄의 합이 창 합계와 같은지, 그리고 벤더
// 축이 창마다 같은 길이·같은 순서인지 본다 (「한 벤더만 있어도 화면 계약이 유지된다」).
func assertWindowVendorsSumToWindow(t *testing.T, bd HomeBreakdown) {
	t.Helper()
	for i, w := range bd.Windows {
		if len(w.Vendors) != len(bd.Vendors) {
			t.Fatalf("창 %d 의 벤더 줄 = %d개, 하루 벤더 = %d개 — 화면이 창마다 벤더를 찾아 맞춰야 한다",
				i, len(w.Vendors), len(bd.Vendors))
		}
		var (
			sum  UsageTotals
			nano int64
		)
		for j, v := range w.Vendors {
			if v.Vendor != bd.Vendors[j].Vendor {
				t.Fatalf("창 %d 의 %d번 벤더 = %q, 하루 목록 = %q — 순서가 다르다",
					i, j, v.Vendor, bd.Vendors[j].Vendor)
			}
			addUsage(&sum, v.UsageTotals)
			nano += int64(v.Cost.NanoUSD)
		}
		if !reflect.DeepEqual(sum, w.UsageTotals) {
			t.Errorf("창 %d 의 벤더 합 = %+v, 창 = %+v", i, sum, w.UsageTotals)
		}
		if nano != int64(w.Cost.NanoUSD) {
			t.Errorf("창 %d 의 벤더 비용 합 = %d nano, 창 = %d nano", i, nano, w.Cost.NanoUSD)
		}
	}
}

// assertModelsSumToVendor 는 모델 줄의 합이 벤더 줄과 같은지 본다. 목록이 잘렸으면
// 작아야 하고, 잘리지 않았으면 정확히 같아야 한다.
func assertModelsSumToVendor(t *testing.T, bd HomeBreakdown) {
	t.Helper()
	for _, v := range bd.Vendors {
		var in, out, read, write, nano, calls int64
		for _, m := range v.Models {
			in += m.InputTokens
			out += m.OutputTokens
			read += m.CacheReadTokens
			write += m.CacheWriteTokens
			nano += int64(m.Cost.Total.NanoUSD)
			calls += m.Cost.Calls
			if m.Tokens != m.InputTokens+m.OutputTokens {
				t.Errorf("%s/%s 의 토큰 = %d, want %d (캐시를 더했다)",
					v.Vendor, m.Model, m.Tokens, m.InputTokens+m.OutputTokens)
			}
		}
		if v.ModelsTruncated {
			if nano > int64(v.Cost.Total.NanoUSD) {
				t.Errorf("%s 의 잘린 모델 합 %d nano 가 벤더 %d nano 보다 크다",
					v.Vendor, nano, v.Cost.Total.NanoUSD)
			}
			continue
		}
		fields := []struct {
			name     string
			got, exp int64
		}{
			{name: "입력 토큰", got: in, exp: v.InputTokens},
			{name: "출력 토큰", got: out, exp: v.OutputTokens},
			{name: "캐시 읽기", got: read, exp: v.CacheReadTokens},
			{name: "캐시 쓰기", got: write, exp: v.CacheWriteTokens},
			{name: "비용(nano)", got: nano, exp: int64(v.Cost.Total.NanoUSD)},
			{name: "호출 수", got: calls, exp: v.Cost.Calls},
		}
		for _, fd := range fields {
			if fd.got != fd.exp {
				t.Errorf("%s 의 모델별 %s 합 = %d, 벤더 = %d", v.Vendor, fd.name, fd.got, fd.exp)
			}
		}
	}
}

// assertSharesSumToConstant 는 점유율 합이 문서화된 상수인지 본다.
func assertSharesSumToConstant(t *testing.T, bd HomeBreakdown) {
	t.Helper()
	var cost, tokens, baseCost, baseTokens int64
	for _, v := range bd.Vendors {
		cost += int64(v.CostSharePermille)
		tokens += int64(v.TokenSharePermille)
		baseCost += int64(v.Cost.Total.NanoUSD)
		baseTokens += v.Tokens
	}
	shares := []struct {
		name string
		got  int64
		base int64
	}{
		{name: "비용 점유율", got: cost, base: baseCost},
		{name: "토큰 점유율", got: tokens, base: baseTokens},
	}
	for _, s := range shares {
		want := int64(SharePermilleTotal)
		if s.base <= 0 {
			want = 0
		}
		if s.got != want {
			t.Errorf("%s 합 = %d, want %d", s.name, s.got, want)
		}
	}
}

// addUsage 는 테스트 쪽 합산이다. 구현의 Totals.add 를 쓰면 같은 실수를 두 번 하게 된다.
func addUsage(dst *UsageTotals, src UsageTotals) {
	dst.APIRequests += src.APIRequests
	dst.InputTokens += src.InputTokens
	dst.OutputTokens += src.OutputTokens
	dst.CacheReadTokens += src.CacheReadTokens
	dst.CacheWriteTokens += src.CacheWriteTokens
	dst.Tokens += src.Tokens
	dst.ToolCalls += src.ToolCalls
	dst.Prompts += src.Prompts
	dst.SessionsStarted += src.SessionsStarted
	dst.ActiveSeconds += src.ActiveSeconds
}

// 인수조건: 벤더별 비용·토큰·점유율. 값과 순서를 함께 고정한다.
func TestHomeBreakdownVendorRowsAndShares(t *testing.T) {
	f := newFixture(t)
	seedHomeBreakdown(f)

	got, err := f.reader.HomeBreakdown(context.Background(),
		HomeBreakdownQuery{TZ: utc, Date: "2026-08-10"})
	if err != nil {
		t.Fatalf("HomeBreakdown: %v", err)
	}
	if len(got.Vendors) != 2 {
		t.Fatalf("벤더 = %d개, want 2 (%+v)", len(got.Vendors), got.Vendors)
	}

	tests := []struct {
		name string
		row  VendorUsage
		// 비용 1.5(보고) + 9.0(추정) + 0.25(보고), 모르는 모델 1건은 빠진다.
		wantVendor      string
		wantCostUSD     float64
		wantTokens      int64
		wantCostShare   int
		wantTokenShare  int
		wantCalls       int64
		wantUnavailable int64
		wantToolCalls   int64
	}{
		{
			name: "1행은 비용이 큰 claude_code", row: got.Vendors[0],
			wantVendor: vendorClaude, wantCostUSD: 10.75, wantTokens: 1_001_600 + 100_300,
			wantCostShare: 705, wantTokenShare: 813,
			wantCalls: 4, wantUnavailable: 1, wantToolCalls: 3,
		},
		{
			name: "2행은 codex", row: got.Vendors[1],
			wantVendor: vendorCodex, wantCostUSD: 0.75 + 3.75, wantTokens: 201_000 + 52_000,
			wantCostShare: 295, wantTokenShare: 187,
			wantCalls: 2, wantUnavailable: 0, wantToolCalls: 0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			switch {
			case tc.row.Vendor != tc.wantVendor:
				t.Fatalf("벤더 = %q, want %q (비용 내림차순)", tc.row.Vendor, tc.wantVendor)
			case tc.row.Cost.Total.USD != tc.wantCostUSD:
				t.Errorf("비용 = %v, want %v", tc.row.Cost.Total.USD, tc.wantCostUSD)
			case tc.row.Tokens != tc.wantTokens:
				t.Errorf("토큰 = %d, want %d", tc.row.Tokens, tc.wantTokens)
			case tc.row.CostSharePermille != tc.wantCostShare:
				t.Errorf("비용 점유율 = %d‰, want %d‰", tc.row.CostSharePermille, tc.wantCostShare)
			case tc.row.TokenSharePermille != tc.wantTokenShare:
				t.Errorf("토큰 점유율 = %d‰, want %d‰", tc.row.TokenSharePermille, tc.wantTokenShare)
			case tc.row.Cost.Calls != tc.wantCalls:
				t.Errorf("호출 수 = %d, want %d", tc.row.Cost.Calls, tc.wantCalls)
			case tc.row.Cost.Unavailable != tc.wantUnavailable:
				t.Errorf("비용 미상 = %d, want %d", tc.row.Cost.Unavailable, tc.wantUnavailable)
			case tc.row.ToolCalls != tc.wantToolCalls:
				// 도구 호출은 llm_calls 와 다른 출처다. 한 질의로 JOIN 하면 비용이 3배가 된다.
				t.Errorf("도구 호출 = %d, want %d", tc.row.ToolCalls, tc.wantToolCalls)
			}
		})
	}

	// 두 점유율은 각각 정확히 1000 이어야 한다 — 화면이 두 막대를 나란히 채운다.
	if s := got.Vendors[0].CostSharePermille + got.Vendors[1].CostSharePermille; s != SharePermilleTotal {
		t.Errorf("비용 점유율 합 = %d, want %d", s, SharePermilleTotal)
	}
	if s := got.Vendors[0].TokenSharePermille + got.Vendors[1].TokenSharePermille; s != SharePermilleTotal {
		t.Errorf("토큰 점유율 합 = %d, want %d", s, SharePermilleTotal)
	}
}

// 인수조건: 누락된 시간 버킷은 0 으로 채운다.
//
// 빠진 창이 있으면 화면의 막대가 옆으로 밀려 다른 시간대의 값처럼 보인다.
func TestHomeBreakdownFillsMissingWindows(t *testing.T) {
	f := newFixture(t)
	seedHomeBreakdown(f)

	got, err := f.reader.HomeBreakdown(context.Background(),
		HomeBreakdownQuery{TZ: utc, Date: "2026-08-10"})
	if err != nil {
		t.Fatalf("HomeBreakdown: %v", err)
	}
	if len(got.Windows) != 12 {
		t.Fatalf("창 = %d개, want 12", len(got.Windows))
	}

	// 활동이 있는 창은 넷뿐이다 — 01시대·09시대·15시대·22시대.
	active := map[int]bool{0: true, 4: true, 7: true, 11: true}
	for i, w := range got.Windows {
		if w.Active != active[i] {
			t.Errorf("창 %d(%02d시) Active = %v, want %v", i, w.LocalHour, w.Active, active[i])
		}
		if w.LocalHour != i*2 {
			t.Errorf("창 %d 의 현지 시각 = %d, want %d", i, w.LocalHour, i*2)
		}
		if active[i] {
			continue
		}
		// 빈 창도 모양은 그대로다.
		if w.Tokens != 0 || w.Cost.NanoUSD != 0 || w.ToolCalls != 0 {
			t.Errorf("빈 창 %d 이 0 이 아니다: %+v", i, w.UsageTotals)
		}
		if len(w.Vendors) != len(got.Vendors) {
			t.Errorf("빈 창 %d 의 벤더 줄 = %d개, want %d개", i, len(w.Vendors), len(got.Vendors))
		}
		for _, v := range w.Vendors {
			if v.Tokens != 0 || v.Cost.NanoUSD != 0 {
				t.Errorf("빈 창 %d 의 %s 가 0 이 아니다: %+v", i, v.Vendor, v)
			}
		}
	}
}

// 「최고 사용 시간대」. 토큰이 가장 많은 창이고, 그 창에서 가장 많이 쓴 벤더를 함께 준다.
func TestHomeBreakdownPeakWindow(t *testing.T) {
	f := newFixture(t)
	seedHomeBreakdown(f)

	got, err := f.reader.HomeBreakdown(context.Background(),
		HomeBreakdownQuery{TZ: utc, Date: "2026-08-10"})
	if err != nil {
		t.Fatalf("HomeBreakdown: %v", err)
	}
	if !got.Peak.Found {
		t.Fatalf("Peak 를 못 찾았다: %+v", got.Peak)
	}
	if got.Peak.Index != 0 || got.Peak.LocalHour != 0 {
		t.Errorf("Peak = %d번 창(%02d시), want 0번(00시)", got.Peak.Index, got.Peak.LocalHour)
	}
	if got.Peak.Vendor != vendorClaude {
		t.Errorf("Peak 벤더 = %q, want %q", got.Peak.Vendor, vendorClaude)
	}
	w := got.Windows[got.Peak.Index]
	if got.Peak.Tokens != w.Tokens || got.Peak.Cost.NanoUSD != w.Cost.NanoUSD {
		t.Errorf("Peak = %+v, 창 = %+v — 같은 창의 값이어야 한다", got.Peak, w.UsageTotals)
	}
	if got.Peak.StartAt != w.StartAt || got.Peak.EndAt != w.EndAt {
		t.Errorf("Peak 구간 = [%d,%d), 창 = [%d,%d)", got.Peak.StartAt, got.Peak.EndAt, w.StartAt, w.EndAt)
	}
}

// 사용량이 없는 날에 아무 창이나 고르면 화면이 "새벽 0시가 가장 바빴다" 를 그린다.
func TestHomeBreakdownPeakIsAbsentWithoutUsage(t *testing.T) {
	f := newFixture(t)
	at := time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC)
	// 도구 호출만 있는 날이다. 창은 Active 지만 사용량(토큰·비용)은 0 이다.
	f.write(store.Batch{Events: []store.EventRecord{
		toolRecord("s-tool", "t-tool", "call-tool-only", at, 1, toolSpec{
			ToolName: "Read", Success: event.Some(true),
		}),
	}})

	got, err := f.reader.HomeBreakdown(context.Background(),
		HomeBreakdownQuery{TZ: utc, Date: "2026-08-10"})
	if err != nil {
		t.Fatalf("HomeBreakdown: %v", err)
	}
	if got.Peak.Found || got.Peak.Index != -1 {
		t.Errorf("Peak = %+v, want 없음/-1 — 토큰도 비용도 없는 날이다", got.Peak)
	}
	if !got.Windows[0].Active {
		t.Error("도구만 쓴 창이 Active = false — 활동 판정은 비용·토큰만 보는 것이 아니다")
	}
}

// 인수조건: 데이터가 한 벤더에만 있어도 화면 계약이 유지된다.
func TestHomeBreakdownSingleVendorKeepsShape(t *testing.T) {
	f := newFixture(t)
	at := time.Date(2026, 8, 10, 3, 0, 0, 0, time.UTC)
	f.write(store.Batch{Events: []store.EventRecord{
		llmRecord("s-only", "t-only", at, 1, llmSpec{Model: modelSonnet, Cost: 2, Input: 100, Output: 20}),
	}})

	got, err := f.reader.HomeBreakdown(context.Background(),
		HomeBreakdownQuery{TZ: utc, Date: "2026-08-10"})
	if err != nil {
		t.Fatalf("HomeBreakdown: %v", err)
	}
	if len(got.Vendors) != 1 || got.Vendors[0].Vendor != vendorClaude {
		t.Fatalf("벤더 = %+v, want claude_code 하나 — 관측되지 않은 벤더를 지어내면 안 된다", got.Vendors)
	}
	// 하나뿐이어도 점유율은 1000 이다. 0 이나 100 을 주면 화면의 막대가 비어 보인다.
	if v := got.Vendors[0]; v.CostSharePermille != SharePermilleTotal ||
		v.TokenSharePermille != SharePermilleTotal {
		t.Errorf("점유율 = %d‰/%d‰, want %d‰", v.CostSharePermille, v.TokenSharePermille, SharePermilleTotal)
	}
	// 창은 12개 그대로이고 창마다 벤더 줄이 정확히 하나다.
	if len(got.Windows) != 12 {
		t.Fatalf("창 = %d개, want 12", len(got.Windows))
	}
	for i, w := range got.Windows {
		if len(w.Vendors) != 1 || w.Vendors[0].Vendor != vendorClaude {
			t.Fatalf("창 %d 의 벤더 줄 = %+v, want claude_code 하나", i, w.Vendors)
		}
	}
	if !got.Peak.Found || got.Peak.Vendor != vendorClaude {
		t.Errorf("Peak = %+v, want claude_code", got.Peak)
	}
}

// 벤더별 모델 사용량. 같은 모델의 다른 표기는 한 줄로 모이고, 상위 N 개로 잘린다.
func TestHomeBreakdownTopModels(t *testing.T) {
	f := newFixture(t)
	at := func(m int) time.Time { return time.Date(2026, 8, 10, 3, m, 0, 0, time.UTC) }
	f.write(store.Batch{Events: []store.EventRecord{
		// 같은 opus 를 두 표기로 부른다 — 정규화하지 않으면 두 줄로 쪼개진다.
		llmRecord("s-m", "t-m", at(0), 1, llmSpec{Model: modelOpusDated, Cost: 3, Input: 100}),
		llmRecord("s-m", "t-m", at(1), 2, llmSpec{Model: modelOpus, Cost: 2, Input: 50}),
		llmRecord("s-m", "t-m", at(2), 3, llmSpec{Model: modelSonnet, Cost: 1, Input: 10}),
		llmRecord("s-m", "t-m", at(3), 4, llmSpec{Model: modelHaiku, Cost: 1, Input: 10}),
	}})
	ctx := context.Background()

	t.Run("표기가 다른 같은 모델이 한 줄로 모인다", func(t *testing.T) {
		got, err := f.reader.HomeBreakdown(ctx, HomeBreakdownQuery{TZ: utc, Date: "2026-08-10"})
		if err != nil {
			t.Fatalf("HomeBreakdown: %v", err)
		}
		models := got.Vendors[0].Models
		if len(models) != 3 {
			t.Fatalf("모델 = %d줄, want 3 (%+v)", len(models), models)
		}
		if models[0].Model != modelOpus || models[0].Cost.Total.USD != 5 || models[0].Cost.Calls != 2 {
			t.Errorf("1행 = %+v, want %s / 5 USD / 2건", models[0], modelOpus)
		}
		// 비용이 같은 둘은 모델 이름 오름차순이다 — 동률에서 순서가 흔들리면 새로고침마다 바뀐다.
		if models[1].Model != modelHaiku || models[2].Model != modelSonnet {
			t.Errorf("동률 순서 = %s, %s, want %s, %s",
				models[1].Model, models[2].Model, modelHaiku, modelSonnet)
		}
		if got.Vendors[0].ModelsTruncated {
			t.Error("ModelsTruncated = true — 기본 상한(5)보다 적다")
		}
	})

	t.Run("상한을 넘으면 잘리고 그 사실을 알린다", func(t *testing.T) {
		got, err := f.reader.HomeBreakdown(ctx,
			HomeBreakdownQuery{TZ: utc, Date: "2026-08-10", ModelLimit: 1})
		if err != nil {
			t.Fatalf("HomeBreakdown: %v", err)
		}
		v := got.Vendors[0]
		if len(v.Models) != 1 || v.Models[0].Model != modelOpus {
			t.Fatalf("모델 = %+v, want opus 한 줄", v.Models)
		}
		if !v.ModelsTruncated {
			t.Error("ModelsTruncated = false — 잘렸다는 사실을 화면이 알아야 한다")
		}
		// 잘려도 벤더 줄 자체는 온전하다. 화면이 "상위 1개 + 전체" 를 함께 그릴 수 있어야 한다.
		if v.Cost.Total.USD != 7 || v.Cost.Calls != 4 {
			t.Errorf("벤더 합계 = %v USD / %d건, want 7 / 4 — 모델을 자를 때 함께 잘렸다",
				v.Cost.Total.USD, v.Cost.Calls)
		}
	})
}

// 인수조건: reasoning·cache 토큰의 중복 가산 금지는 Home 요약과 동일하게 적용한다.
//
// reasoning_tokens 는 v3 쓰기 경로에 출처가 없어(store/promote.go 가 NULL 을 넣는다)
// 이벤트로는 값을 만들 수 없다. 그래서 여기서만 컬럼을 직접 채워, 값이 생기더라도
// 토큰 총량과 비용이 움직이지 않는다는 것을 고정한다.
func TestHomeBreakdownIgnoresReasoningAndCacheTokens(t *testing.T) {
	f := newFixture(t)
	at := time.Date(2026, 8, 10, 3, 0, 0, 0, time.UTC)
	f.write(store.Batch{Events: []store.EventRecord{
		llmRecord("s-r", "t-r", at, 1, llmSpec{
			Model: modelSonnet, Cost: 2, Input: 1000, Output: 200,
			CacheRead: 5_000_000, CacheWrit: 3_000_000,
		}),
	}})
	ctx := context.Background()

	before, err := f.reader.HomeBreakdown(ctx, HomeBreakdownQuery{TZ: utc, Date: "2026-08-10"})
	if err != nil {
		t.Fatalf("HomeBreakdown: %v", err)
	}
	v := before.Vendors[0]
	if v.Tokens != 1200 {
		t.Fatalf("토큰 = %d, want 1200 (입력+출력만) — 캐시 800만이 더해졌다", v.Tokens)
	}
	if v.CacheReadTokens != 5_000_000 || v.CacheWriteTokens != 3_000_000 {
		t.Errorf("캐시 토큰 = %d/%d, want 5000000/3000000 — 따로는 보여야 한다",
			v.CacheReadTokens, v.CacheWriteTokens)
	}

	if _, err := f.db.SQL().ExecContext(ctx,
		`UPDATE llm_calls SET reasoning_tokens = 999999`); err != nil {
		t.Fatalf("reasoning_tokens 채우기: %v", err)
	}

	after, err := f.reader.HomeBreakdown(ctx, HomeBreakdownQuery{TZ: utc, Date: "2026-08-10"})
	if err != nil {
		t.Fatalf("HomeBreakdown: %v", err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Errorf("reasoning_tokens 가 결과를 바꿨다 — 출력의 부분집합이라 다시 더할 것이 없다:\n전 = %+v\n후 = %+v",
			before.Vendors[0], after.Vendors[0])
	}
}

// 오타를 "데이터 없음" 으로 보이게 하면 안 된다. Home 과 같은 규칙이다.
func TestHomeBreakdownRejectsBadInput(t *testing.T) {
	f := newFixture(t)

	tests := []struct {
		name  string
		query HomeBreakdownQuery
	}{
		{name: "알 수 없는 시간대", query: HomeBreakdownQuery{TZ: "Mars/Phobos"}},
		{name: "날짜 형식 오류", query: HomeBreakdownQuery{TZ: utc, Date: "2026/08/10"}},
		{name: "날짜가 아닌 문자열", query: HomeBreakdownQuery{TZ: utc, Date: "어제"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := f.reader.HomeBreakdown(context.Background(), tc.query); err == nil {
				t.Errorf("에러가 없다 — 조용히 오늘로 떨어지면 사용자는 어제를 보고 있다고 믿는다")
			}
		})
	}
}

// 태그가 곧 TS 필드명이다 (ADR 0004). 규약은 snake_case 다.
func TestHomeBreakdownTagsAreSnakeCase(t *testing.T) {
	types := []any{
		HomeBreakdownQuery{}, HomeBreakdown{}, UsageTotals{},
		UsageWindow{}, VendorWindow{}, VendorUsage{}, ModelUsage{}, PeakWindow{},
	}
	for _, v := range types {
		assertSnakeCaseTags(t, v)
	}
}

// GUI 가 이 화면을 만들려면 서비스가 같은 질의를 감싸야 한다.
func TestServiceHomeBreakdownDelegates(t *testing.T) {
	f := newFixture(t)
	seedHomeBreakdown(f)

	svc := NewService(f.path)
	if err := svc.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { svc.Stop() }) //nolint:errcheck // 테스트 정리
	svc.Reader().now = func() time.Time { return testNow }

	ctx := context.Background()
	q := HomeBreakdownQuery{TZ: seoul, Date: "2026-08-10"}
	want, err := f.reader.HomeBreakdown(ctx, q)
	if err != nil {
		t.Fatalf("Reader.HomeBreakdown: %v", err)
	}
	got, err := svc.HomeBreakdown(ctx, q)
	if err != nil {
		t.Fatalf("Service.HomeBreakdown: %v", err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("결과가 다르다:\nReader  = %+v\nService = %+v", want, got)
	}
}
