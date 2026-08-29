package dashboard

import (
	"context"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/event"
	"github.com/your-org/pulsemetry/internal/session"
	"github.com/your-org/pulsemetry/internal/store"
)

// PROJ-88 의 인수조건 세 가지를 이 파일이 지킨다.
//
//  1. 일광절약시간(DST)과 로컬 자정 경계가 정확하다
//  2. 빈 날짜도 카드와 최근 활동을 빈 상태로 돌려준다
//  3. Home 카드 값과 최근 세션 값의 합계 정의가 지켜진다 (home.go 의 「합계의 정의」)

const newYork = "America/New_York"

// noCost 는 벤더가 cost_usd 를 보고하지 않은 호출을 만든다.
//
// helper_test.go 의 llmRecord 는 Cost 를 항상 Some 으로 채우므로 0 을 넣어도 "0 원을
// 보고했다" 가 된다. 「예상 비용」은 **보고값이 없는** 호출을 단가로 메우는 값이라,
// 그 경로를 밟으려면 NULL 을 만들어야 한다.
func noCost(rec store.EventRecord) store.EventRecord {
	rec.Event.Measure.CostUSD = event.Opt[float64]{}
	return rec
}

// modelSonnet 은 기본 가격표에 단가가 있는 모델이다 (입력 3 · 출력 15 · 캐시읽기 0.30 USD/MTok).
const modelSonnet = "claude-sonnet-4-5"

// seedHomeCost 는 「예상 비용」의 세 갈래를 한 날짜에 모아 넣는다.
//
//	A: 보고값 없음 + 아는 모델      → estimated. 3.0(입력) + 3.0(출력) + 0.3(캐시읽기) = 6.3 USD
//	B: 보고값 있음                  → reported. 2.5 USD (토큰 단가와 무관하다)
//	C: 보고값 없음 + 모르는 모델    → unavailable. 합계에 0 원으로도 들어가지 않는다
//
// A·B 는 세션 s-home-1, C 는 s-home-2 에 넣는다. 둘 다 같은 날 시작하고 자정을 넘기지
// 않으므로 「최근 세션 행들의 합 = 카드」 가 성립해야 한다.
func seedHomeCost(f *fixture) {
	at := time.Date(2026, 8, 10, 3, 0, 0, 0, time.UTC)
	f.write(store.Batch{
		Sessions: []session.Session{
			newSession("s-home-1", at, func(s *session.Session) { s.ActiveSeconds = 300 }),
			newSession("s-home-2", at.Add(time.Hour), func(s *session.Session) { s.ActiveSeconds = 100 }),
		},
		Events: []store.EventRecord{
			noCost(llmRecord("s-home-1", "t-a", at, 1, llmSpec{
				Model: modelSonnet, Input: 1_000_000, Output: 200_000, CacheRead: 1_000_000,
			})),
			llmRecord("s-home-1", "t-b", at.Add(time.Minute), 2, llmSpec{
				Model: modelSonnet, Cost: 2.5, Input: 100, Output: 50,
			}),
			noCost(llmRecord("s-home-2", "t-c", at.Add(time.Hour), 3, llmSpec{
				Model: "made-up-model-9", Input: 100,
			})),
		},
	})
}

// 「예상 비용」은 보고값이 있으면 그것을 쓰고, 없으면 단가로 메우고, 둘 다 안 되면 빼 놓는다.
// 셋을 더하면 정확히 두 배가 되는 종류의 사고라 갈래마다 개수를 함께 확인한다.
func TestHomeEstimatedCostUsesPricingTable(t *testing.T) {
	f := newFixture(t)
	seedHomeCost(f)

	got, err := f.reader.Home(context.Background(), HomeQuery{TZ: utc, Date: "2026-08-10"})
	if err != nil {
		t.Fatalf("Home: %v", err)
	}

	tests := []struct {
		name string
		got  float64
		want float64
	}{
		{name: "예상 비용 합계", got: got.Cost.Total.USD, want: 6.3 + 2.5},
		{name: "보고값 합계는 그보다 작다", got: got.Totals.CostUSD, want: 2.5},
		{name: "캐시 절감액", got: got.Cost.CacheSavings.USD, want: 2.7},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("%s = %v, want %v", tc.name, tc.got, tc.want)
			}
		})
	}

	counts := []struct {
		name string
		got  int64
		want int64
	}{
		{name: "calls", got: got.Cost.Calls, want: 3},
		{name: "reported", got: got.Cost.Reported, want: 1},
		{name: "estimated", got: got.Cost.Estimated, want: 1},
		{name: "unavailable", got: got.Cost.Unavailable, want: 1},
	}
	for _, tc := range counts {
		if tc.got != tc.want {
			t.Errorf("Cost.%s = %d, want %d", tc.name, tc.got, tc.want)
		}
	}

	// 어느 판의 단가로 계산했는지가 결과에 남아야 한다.
	if got.Cost.TableVersion == "" || got.Cost.EffectiveDate == "" {
		t.Errorf("가격표 판 정보가 비었다: %+v", got.Cost)
	}
	// 카드에 나가는 것은 보고값 합이 아니라 예상 비용이다.
	if c := cardFor(t, got.Cards, MetricEstimatedCostUSD); c.Today != 6.3+2.5 {
		t.Errorf("예상 비용 카드 = %v, want %v", c.Today, 6.3+2.5)
	}
}

// 토큰 총량은 입력+출력이다. 캐시는 이미 한 번 센 입력을 다시 읽은 양이라 더하지 않고,
// reasoning 은 출력의 부분집합이라 따로 더할 것이 없다.
func TestHomeTokensExcludeCacheTokens(t *testing.T) {
	f := newFixture(t)
	seedHomeCost(f)

	got, err := f.reader.Home(context.Background(), HomeQuery{TZ: utc, Date: "2026-08-10"})
	if err != nil {
		t.Fatalf("Home: %v", err)
	}
	const wantInput = 1_000_000 + 100 + 100
	const wantOutput = 200_000 + 50
	const wantTokens = wantInput + wantOutput

	if got.Totals.Tokens() != wantTokens {
		t.Errorf("토큰 = %d, want %d (캐시 제외)", got.Totals.Tokens(), wantTokens)
	}
	if got.Totals.CacheReadTokens != 1_000_000 {
		t.Errorf("캐시 읽기 토큰 = %d, want 1000000 — 따로 보이되 총량에는 안 들어간다",
			got.Totals.CacheReadTokens)
	}
	if c := cardFor(t, got.Cards, MetricTokens); c.Today != wantTokens {
		t.Errorf("토큰 카드 = %v, want %d", c.Today, wantTokens)
	}
}

// 인수조건: Home 카드 값과 최근 세션 값의 합계 정의.
//
// 자정을 넘긴 세션이 없고 목록이 잘리지 않았으면 두 값이 정확히 같아야 한다.
// 금액은 float 이 아니라 정수 nano-USD 로 비교한다 — 더하는 순서가 달라도 같아야 한다는
// 것이 이 불변식의 요점이다.
func TestHomeRecentSessionSumMatchesCards(t *testing.T) {
	f := newFixture(t)
	seedHomeCost(f)

	got, err := f.reader.Home(context.Background(), HomeQuery{TZ: utc, Date: "2026-08-10"})
	if err != nil {
		t.Fatalf("Home: %v", err)
	}
	if got.RecentTruncated {
		t.Fatal("목록이 잘렸다 — 이 불변식은 잘리지 않은 목록에서만 성립한다")
	}
	if len(got.Recent) != 2 {
		t.Fatalf("최근 세션 = %d행, want 2", len(got.Recent))
	}

	var (
		tokens int64
		nano   int64
		active float64
	)
	for _, s := range got.Recent {
		tokens += s.Tokens
		nano += int64(s.Cost.Total.NanoUSD)
		active += s.ActiveSeconds
	}
	if tokens != got.Totals.Tokens() {
		t.Errorf("최근 세션 토큰 합 = %d, 카드 = %d", tokens, got.Totals.Tokens())
	}
	if nano != int64(got.Cost.Total.NanoUSD) {
		t.Errorf("최근 세션 비용 합 = %d nano, 카드 = %d nano", nano, got.Cost.Total.NanoUSD)
	}
	if active != got.Totals.ActiveSeconds {
		t.Errorf("최근 세션 활동 시간 합 = %v, 카드 = %v", active, got.Totals.ActiveSeconds)
	}
}

// 자정을 넘긴 세션이 있으면 행 합계가 카드보다 크다. 버그가 아니라 자르는 기준이 다르기
// 때문이고, 그 사실이 home.go 의 「합계의 정의」에 적혀 있다.
func TestHomeRecentSessionMayExceedCardsAcrossMidnight(t *testing.T) {
	f := newFixture(t)
	// 세션은 08-10 23:00Z 에 시작하고, 호출 하나는 그날 안, 다른 하나는 자정을 넘긴다.
	start := time.Date(2026, 8, 10, 23, 0, 0, 0, time.UTC)
	f.write(store.Batch{
		Sessions: []session.Session{newSession("s-cross", start)},
		Events: []store.EventRecord{
			llmRecord("s-cross", "t-1", start, 1, llmSpec{Model: modelSonnet, Cost: 1}),
			llmRecord("s-cross", "t-2", start.Add(2*time.Hour), 2, llmSpec{Model: modelSonnet, Cost: 4}),
		},
	})

	got, err := f.reader.Home(context.Background(), HomeQuery{TZ: utc, Date: "2026-08-10"})
	if err != nil {
		t.Fatalf("Home: %v", err)
	}
	if got.Cost.Total.USD != 1 {
		t.Errorf("카드 비용 = %v, want 1 — 카드는 호출 시각으로 자른다", got.Cost.Total.USD)
	}
	if len(got.Recent) != 1 {
		t.Fatalf("최근 세션 = %d행, want 1", len(got.Recent))
	}
	if got.Recent[0].Cost.Total.USD != 5 {
		t.Errorf("세션 행 비용 = %v, want 5 — 행은 세션 생애 전체다", got.Recent[0].Cost.Total.USD)
	}
}

// 선택 날짜는 UTC 자정이 아니라 그 시간대의 자정으로 잘린다.
func TestHomeSelectedDateHonorsTimeZone(t *testing.T) {
	f := newFixture(t)
	seedTZBoundary(f)

	tests := []struct {
		name      string
		tz        string
		date      string
		wantCost  float64
		wantStart time.Time
	}{
		{
			name: "서울의 08-10 은 UTC 08-09 15:00 에 시작한다",
			tz:   seoul, date: "2026-08-10",
			wantCost:  4 + 1,
			wantStart: time.Date(2026, 8, 9, 15, 0, 0, 0, time.UTC),
		},
		{
			name: "UTC 의 08-10 은 자정 경계",
			tz:   utc, date: "2026-08-10",
			wantCost:  1,
			wantStart: time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "서울의 08-09 (= 전날)",
			tz:   seoul, date: "2026-08-09",
			wantCost:  8 + 2,
			wantStart: time.Date(2026, 8, 8, 15, 0, 0, 0, time.UTC),
		},
		{
			name: "날짜를 비우면 지금이 속한 하루",
			tz:   seoul, date: "",
			wantCost:  4 + 1,
			wantStart: time.Date(2026, 8, 9, 15, 0, 0, 0, time.UTC),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := f.reader.Home(context.Background(), HomeQuery{TZ: tc.tz, Date: tc.date})
			if err != nil {
				t.Fatalf("Home: %v", err)
			}
			if got.StartAt != tc.wantStart.Unix() {
				t.Errorf("StartAt = %s, want %s",
					time.Unix(got.StartAt, 0).UTC(), tc.wantStart)
			}
			// 씨앗이 보고값만 담으므로 예상 비용과 보고값 합이 같다.
			if got.Cost.Total.USD != tc.wantCost {
				t.Errorf("예상 비용 = %v, want %v", got.Cost.Total.USD, tc.wantCost)
			}
			// 전날 구간은 오늘 구간에 정확히 맞닿아야 한다.
			if got.PreviousStartAt >= got.StartAt {
				t.Errorf("전날 시작 %d 가 오늘 시작 %d 보다 뒤다", got.PreviousStartAt, got.StartAt)
			}
		})
	}
}

// 선택 날짜가 오늘이면 IsToday 가 true 다. 화면은 이 값으로 "지금 몇 개가 돌고 있나" 의
// 표기를 고른다 — ActiveAgents 는 날짜가 아니라 지금 기준이기 때문이다.
func TestHomeIsTodayFollowsLocalCalendar(t *testing.T) {
	f := newFixture(t)
	// 2026-08-09 23:00 UTC = 08-10 08:00 KST. UTC 로는 어제, 서울로는 오늘이다.
	f.reader.now = func() time.Time { return time.Date(2026, 8, 9, 23, 0, 0, 0, time.UTC) }

	tests := []struct {
		tz          string
		date        string
		wantIsToday bool
		wantDate    string
	}{
		{tz: seoul, date: "", wantIsToday: true, wantDate: "2026-08-10"},
		{tz: seoul, date: "2026-08-10", wantIsToday: true, wantDate: "2026-08-10"},
		{tz: seoul, date: "2026-08-09", wantIsToday: false, wantDate: "2026-08-09"},
		{tz: utc, date: "", wantIsToday: true, wantDate: "2026-08-09"},
		{tz: utc, date: "2026-08-10", wantIsToday: false, wantDate: "2026-08-10"},
	}
	for _, tc := range tests {
		t.Run(tc.tz+"/"+tc.date, func(t *testing.T) {
			got, err := f.reader.Home(context.Background(), HomeQuery{TZ: tc.tz, Date: tc.date})
			if err != nil {
				t.Fatalf("Home: %v", err)
			}
			if got.Date != tc.wantDate {
				t.Errorf("Date = %q, want %q", got.Date, tc.wantDate)
			}
			if got.IsToday != tc.wantIsToday {
				t.Errorf("IsToday = %v, want %v", got.IsToday, tc.wantIsToday)
			}
		})
	}
}

// 잘못된 입력은 조용히 오늘·UTC 로 떨어지면 안 된다. 사용자는 자기가 고른 날짜를 보고
// 있다고 믿은 채 다른 날의 숫자를 읽게 된다.
func TestHomeRejectsBadInput(t *testing.T) {
	f := newFixture(t)
	tests := []struct {
		name string
		q    HomeQuery
	}{
		{name: "모르는 시간대", q: HomeQuery{TZ: "Mars/Phobos"}},
		{name: "날짜 오타", q: HomeQuery{TZ: utc, Date: "2026-13-45"}},
		{name: "구분자 없는 날짜", q: HomeQuery{TZ: utc, Date: "20260810"}},
		{name: "날짜가 아닌 문자열", q: HomeQuery{TZ: utc, Date: "오늘"}},
		{name: "시각까지 붙은 값", q: HomeQuery{TZ: utc, Date: "2026-08-10T00:00:00Z"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := f.reader.Home(context.Background(), tc.q); err == nil {
				t.Errorf("Home(%+v) 가 에러를 내지 않았다", tc.q)
			}
		})
	}
}

// 인수조건: 빈 날짜도 카드와 최근 활동을 빈 상태로 돌려준다.
func TestHomeEmptyDateKeepsShape(t *testing.T) {
	f := newFixture(t)
	seedHomeCost(f) // 데이터는 08-10 에만 있다

	got, err := f.reader.Home(context.Background(), HomeQuery{TZ: utc, Date: "2026-08-01"})
	if err != nil {
		t.Fatalf("Home: %v", err)
	}
	if len(got.Cards) != 4 {
		t.Fatalf("카드 = %d, want 4", len(got.Cards))
	}
	for _, c := range got.Cards {
		if c.Today != 0 || c.HasBaseline {
			t.Errorf("카드 %q = %+v, want 0 값 · 기준선 없음", c.Metric, c)
		}
	}
	if got.Recent == nil || len(got.Recent) != 0 {
		t.Errorf("Recent = %v, want 빈 슬라이스 (nil 이면 JSON 이 null 이다)", got.Recent)
	}
	if got.RecentTruncated {
		t.Error("RecentTruncated = true — 빈 목록이 잘렸다고 한다")
	}
	if len(got.TwoHour.Windows) != 12 {
		t.Errorf("2시간 창 = %d개, want 12", len(got.TwoHour.Windows))
	}
	if got.TwoHour.ActiveWindows != 0 || got.TwoHour.ActiveSeconds != 0 {
		t.Errorf("빈 날의 평균 = %+v, want 0", got.TwoHour)
	}
	if got.Cost.Calls != 0 || got.Cost.Total.NanoUSD != 0 {
		t.Errorf("빈 날의 비용 = %+v, want 0", got.Cost)
	}
	// 가격표 판 정보는 빈 날에도 있어야 한다 — 화면이 "어느 단가 기준" 을 늘 표시한다.
	if got.Cost.TableVersion == "" {
		t.Error("빈 날에 가격표 판 정보가 없다")
	}
}

// 인수조건: DST. 하루가 23·25시간인 날에도 창이 하루를 정확히 덮어야 한다.
// 2시간으로 나누어떨어지지 않으므로 마지막 창은 짧다.
func TestHomeTwoHourWindowsCoverDSTDay(t *testing.T) {
	if _, err := time.LoadLocation(newYork); err != nil {
		t.Skipf("%s 시간대를 못 읽는다: %v", newYork, err)
	}
	f := newFixture(t)

	tests := []struct {
		name        string
		date        string
		wantWindows int
		wantHours   float64
	}{
		{name: "봄 전환일은 23시간 · 창 12개", date: "2026-03-08", wantWindows: 12, wantHours: 23},
		{name: "가을 전환일은 25시간 · 창 13개", date: "2026-11-01", wantWindows: 13, wantHours: 25},
		{name: "평범한 날은 24시간 · 창 12개", date: "2026-08-10", wantWindows: 12, wantHours: 24},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := f.reader.Home(context.Background(), HomeQuery{TZ: newYork, Date: tc.date})
			if err != nil {
				t.Fatalf("Home: %v", err)
			}
			if hours := float64(got.EndAt-got.StartAt) / 3600; hours != tc.wantHours {
				t.Fatalf("하루 길이 = %v시간, want %v — 24시간을 더한 구현이다", hours, tc.wantHours)
			}
			windows := got.TwoHour.Windows
			if len(windows) != tc.wantWindows {
				t.Fatalf("창 = %d개, want %d", len(windows), tc.wantWindows)
			}
			// 창들이 하루를 구멍도 겹침도 없이 덮어야 한다. 어느 쪽이든 창 합계가 카드와 어긋난다.
			if windows[0].StartAt != got.StartAt {
				t.Errorf("첫 창 시작 = %d, 하루 시작 = %d", windows[0].StartAt, got.StartAt)
			}
			if last := windows[len(windows)-1]; last.EndAt != got.EndAt {
				t.Errorf("마지막 창 끝 = %d, 하루 끝 = %d", last.EndAt, got.EndAt)
			}
			for i := 1; i < len(windows); i++ {
				if windows[i].StartAt != windows[i-1].EndAt {
					t.Fatalf("창 %d 과 %d 사이가 어긋난다: %d != %d",
						i-1, i, windows[i-1].EndAt, windows[i].StartAt)
				}
			}
			// 전날도 같은 규칙으로 잘려야 한다 — 카드의 증감률이 전날 구간을 쓴다.
			if got.PreviousStartAt >= got.StartAt {
				t.Errorf("전날 시작 %d 가 오늘 시작 %d 보다 뒤다", got.PreviousStartAt, got.StartAt)
			}
		})
	}
}

// 2시간 평균의 분모는 활동이 있었던 창뿐이다. 자는 시간까지 분모에 넣으면 평균이 늘
// 바닥에 붙어 아무것도 구분하지 못한다.
func TestHomeTwoHourAverageUsesActiveWindowsOnly(t *testing.T) {
	f := newFixture(t)
	// 03:00Z 와 09:00Z — UTC 기준 서로 다른 2시간 창(1번·4번)에 든다.
	first := time.Date(2026, 8, 10, 3, 0, 0, 0, time.UTC)
	second := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	f.write(store.Batch{
		Sessions: []session.Session{
			newSession("s-w1", first, func(s *session.Session) { s.ActiveSeconds = 300 }),
			newSession("s-w2", second, func(s *session.Session) { s.ActiveSeconds = 100 }),
		},
		Events: []store.EventRecord{
			llmRecord("s-w1", "t-w1", first, 1, llmSpec{Model: modelSonnet, Cost: 2}),
			llmRecord("s-w2", "t-w2", second, 2, llmSpec{Model: modelSonnet, Cost: 4}),
		},
	})

	got, err := f.reader.Home(context.Background(), HomeQuery{TZ: utc, Date: "2026-08-10"})
	if err != nil {
		t.Fatalf("Home: %v", err)
	}
	if got.TwoHour.ActiveWindows != 2 {
		t.Fatalf("활동 창 = %d개, want 2 (12개 전체가 분모면 안 된다)", got.TwoHour.ActiveWindows)
	}
	if got.TwoHour.ActiveSeconds != 200 {
		t.Errorf("2시간 평균 활동 시간 = %v, want 200 ((300+100)/2)", got.TwoHour.ActiveSeconds)
	}
	if got.TwoHour.Cost.USD != 3 {
		t.Errorf("2시간 평균 비용 = %v, want 3 ((2+4)/2)", got.TwoHour.Cost.USD)
	}
	if c := cardFor(t, got.Cards, MetricTwoHourActiveSeconds); c.Today != 200 {
		t.Errorf("2시간 평균 카드 = %v, want 200", c.Today)
	}

	// 창 합계는 그 날 카드와 같아야 한다 — 구간 안의 사실은 반드시 창 하나에 귀속된다.
	var (
		windowActive float64
		windowNano   int64
	)
	for _, w := range got.TwoHour.Windows {
		windowActive += w.ActiveSeconds
		windowNano += int64(w.Cost.NanoUSD)
	}
	if windowActive != got.Totals.ActiveSeconds {
		t.Errorf("창 활동 시간 합 = %v, 카드 = %v", windowActive, got.Totals.ActiveSeconds)
	}
	if windowNano != int64(got.Cost.Total.NanoUSD) {
		t.Errorf("창 비용 합 = %d nano, 카드 = %d nano", windowNano, got.Cost.Total.NanoUSD)
	}
}

// 최근 활동은 그 날짜에 시작한 세션이고 최신순이다. 진행 중 세션의 상태와 길이는
// 세션 목록(Sessions)과 같은 식으로 계산돼야 한다.
func TestHomeRecentSessionsSummarizeSelectedDate(t *testing.T) {
	f := newFixture(t)
	day := time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC)
	f.write(store.Batch{Sessions: []session.Session{
		newSession("s-r1", day),
		newSession("s-r2", day.Add(2*time.Hour), codex),
		newSession("s-r3", day.Add(4*time.Hour), running),
		// 전날 세션은 08-10 목록에 없어야 한다.
		newSession("s-prev", day.Add(-24*time.Hour)),
	}})

	got, err := f.reader.Home(context.Background(), HomeQuery{TZ: utc, Date: "2026-08-10"})
	if err != nil {
		t.Fatalf("Home: %v", err)
	}
	if len(got.Recent) != 3 {
		t.Fatalf("최근 세션 = %d행, want 3", len(got.Recent))
	}
	wantOrder := []string{"s-r3", "s-r2", "s-r1"}
	for i, want := range wantOrder {
		if got.Recent[i].SessionKey != want {
			t.Errorf("Recent[%d] = %q, want %q (최신순)", i, got.Recent[i].SessionKey, want)
		}
	}

	inProgress := got.Recent[0]
	if inProgress.Status != StatusRunning || inProgress.EndedAt != nil {
		t.Errorf("진행 중 세션 = %+v, want status=running · ended_at=null", inProgress)
	}
	if got.Recent[1].Vendor != vendorCodex {
		t.Errorf("벤더 = %q, want %q", got.Recent[1].Vendor, vendorCodex)
	}
	done := got.Recent[2]
	if done.Status != StatusCompleted || done.EndedAt == nil {
		t.Errorf("완료 세션 = %+v, want status=completed · ended_at 있음", done)
	}
	if done.DurationMS != 600*1000 {
		t.Errorf("길이 = %dms, want 600000", done.DurationMS)
	}
	if done.ProjectName != "telemetryctl" {
		t.Errorf("프로젝트 이름 = %q, want telemetryctl (원경로의 basename)", done.ProjectName)
	}
	if done.WorkspacePath != workspaceA {
		t.Errorf("작업 폴더 = %q, want %q", done.WorkspacePath, workspaceA)
	}
}

// 잘린 목록은 그 사실을 알려야 한다. 조용히 자르면 행 합계가 카드보다 작은 이유를
// 아무도 설명하지 못한다.
func TestHomeRecentSessionsLimit(t *testing.T) {
	f := newFixture(t)
	day := time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC)
	var sessions []session.Session
	for i := range 5 {
		sessions = append(sessions, newSession(
			"s-lim-"+string(rune('a'+i)), day.Add(time.Duration(i)*time.Minute)))
	}
	f.write(store.Batch{Sessions: sessions})

	tests := []struct {
		name          string
		limit         int
		wantRows      int
		wantTruncated bool
	}{
		{name: "상한 미만", limit: 3, wantRows: 3, wantTruncated: true},
		{name: "정확히 전부", limit: 5, wantRows: 5, wantTruncated: false},
		{name: "0 은 기본값", limit: 0, wantRows: 5, wantTruncated: false},
		{name: "음수도 기본값", limit: -7, wantRows: 5, wantTruncated: false},
		{name: "상한 초과는 잘라 준다", limit: 100_000, wantRows: 5, wantTruncated: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := f.reader.Home(context.Background(),
				HomeQuery{TZ: utc, Date: "2026-08-10", RecentLimit: tc.limit})
			if err != nil {
				t.Fatalf("Home: %v", err)
			}
			if len(got.Recent) != tc.wantRows {
				t.Errorf("행 = %d, want %d", len(got.Recent), tc.wantRows)
			}
			if got.RecentTruncated != tc.wantTruncated {
				t.Errorf("RecentTruncated = %v, want %v", got.RecentTruncated, tc.wantTruncated)
			}
		})
	}
}

// 활성 벤더·세션의 근거는 ended_at IS NULL 하나다. 선택 날짜와 무관하게 "지금" 을 가리킨다.
func TestHomeActiveAgentsIgnoreSelectedDate(t *testing.T) {
	f := newFixture(t)
	f.write(store.Batch{Sessions: []session.Session{
		newSession("s-a1", testNow.Add(-time.Hour), running),
		newSession("s-a2", testNow.Add(-30*time.Minute), running, codex),
		newSession("s-a3", testNow.Add(-3*time.Hour)), // completed
	}})

	for _, date := range []string{"", "2026-08-10", "2026-01-01"} {
		got, err := f.reader.Home(context.Background(), HomeQuery{TZ: utc, Date: date})
		if err != nil {
			t.Fatalf("Home(%q): %v", date, err)
		}
		if got.ActiveSessions != 2 {
			t.Errorf("date=%q ActiveSessions = %d, want 2", date, got.ActiveSessions)
		}
		if len(got.ActiveAgents) != 2 {
			t.Errorf("date=%q ActiveAgents = %v, want 2종", date, got.ActiveAgents)
		}
	}
}

// 카드는 티켓이 지정한 네 지표다. 이름이 곧 프런트엔드와의 계약이라 순서·이름을 고정한다.
func TestHomeCardsCoverTicketMetrics(t *testing.T) {
	f := newFixture(t)
	seedHomeCost(f)

	got, err := f.reader.Home(context.Background(), HomeQuery{TZ: utc, Date: "2026-08-10"})
	if err != nil {
		t.Fatalf("Home: %v", err)
	}
	want := []string{
		MetricTokens, MetricEstimatedCostUSD, MetricActiveSeconds, MetricTwoHourActiveSeconds,
	}
	if len(got.Cards) != len(want) {
		t.Fatalf("카드 = %d장, want %d장", len(got.Cards), len(want))
	}
	for i, metric := range want {
		if got.Cards[i].Metric != metric {
			t.Errorf("Cards[%d].Metric = %q, want %q", i, got.Cards[i].Metric, metric)
		}
	}
}

// 전날 대비는 전날 구간으로 계산한다. 전날이 0 이면 증감률이 정의되지 않는다 —
// 0 으로 나눈 +Inf 는 encoding/json 이 직렬화하지 못해 조회 전체가 깨진다.
func TestHomeComparesWithPreviousDay(t *testing.T) {
	f := newFixture(t)
	f.write(store.Batch{Events: []store.EventRecord{
		llmRecord("s-p1", "t-p1", time.Date(2026, 8, 10, 3, 0, 0, 0, time.UTC), 1,
			llmSpec{Model: modelSonnet, Cost: 15}),
		llmRecord("s-p2", "t-p2", time.Date(2026, 8, 9, 3, 0, 0, 0, time.UTC), 2,
			llmSpec{Model: modelSonnet, Cost: 10}),
	}})

	got, err := f.reader.Home(context.Background(), HomeQuery{TZ: utc, Date: "2026-08-10"})
	if err != nil {
		t.Fatalf("Home: %v", err)
	}
	card := cardFor(t, got.Cards, MetricEstimatedCostUSD)
	if card.Today != 15 || card.Yesterday != 10 {
		t.Fatalf("카드 = %+v, want 오늘 15 · 어제 10", card)
	}
	if !card.HasBaseline || card.DeltaPercent != 50 {
		t.Errorf("증감률 = %v (기준선 %v), want 50", card.DeltaPercent, card.HasBaseline)
	}
	if got.PreviousTotals.CostUSD != 10 {
		t.Errorf("PreviousTotals.CostUSD = %v, want 10", got.PreviousTotals.CostUSD)
	}
}

// 태그가 곧 TS 필드명이다 (ADR 0004). 빠뜨리면 화면이 조용히 undefined 를 읽는다.
func TestHomeResponseTagsAreSnakeCase(t *testing.T) {
	for _, v := range []any{
		HomeQuery{}, HomeSummary{}, TwoHourWindow{}, TwoHourAverage{},
		RecentSession{}, CostSummary{},
	} {
		assertSnakeCaseTags(t, v)
	}
}

// Service 는 Reader 와 같은 답을 준다. GUI 가 감싸는 것은 이쪽이다.
func TestServiceHomeDelegates(t *testing.T) {
	f := newFixture(t)
	seedHomeCost(f)

	svc := NewService(f.path)
	if err := svc.Start(); err != nil {
		t.Fatalf("Service.Start: %v", err)
	}
	t.Cleanup(func() {
		if err := svc.Stop(); err != nil {
			t.Errorf("Service.Stop: %v", err)
		}
	})

	got, err := svc.Home(context.Background(), HomeQuery{TZ: utc, Date: "2026-08-10"})
	if err != nil {
		t.Fatalf("Service.Home: %v", err)
	}
	if got.Cost.Total.USD != 6.3+2.5 {
		t.Errorf("예상 비용 = %v, want %v", got.Cost.Total.USD, 6.3+2.5)
	}
}
