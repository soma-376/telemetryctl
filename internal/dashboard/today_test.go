package dashboard

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/session"
	"github.com/your-org/pulsemetry/internal/store"
)

// seedTZBoundary 는 "오늘" 의 경계를 시간대마다 다르게 잡아야만 통과하는 데이터를 넣는다.
//
// testNow = 2026-08-10 02:00 UTC 기준으로
//
//	Asia/Seoul 오늘  = 08-09 15:00Z ~ 08-10 15:00Z
//	Asia/Seoul 어제  = 08-08 15:00Z ~ 08-09 15:00Z
//	UTC        오늘  = 08-10 00:00Z ~ 08-11 00:00Z
//	UTC        어제  = 08-09 00:00Z ~ 08-10 00:00Z
//
// 네 시각을 아래처럼 놓으면 두 시간대의 합계가 어느 하나도 겹치지 않는다.
// 비용의 유일한 출처가 llm_calls 라 씨앗도 api_request 로그다 (ADR 0009).
func seedTZBoundary(f *fixture) {
	at := []struct {
		when time.Time
		cost float64
	}{
		// 서울의 오늘 아침 이전(= 서울 어제) / UTC 로도 어제
		{time.Date(2026, 8, 9, 2, 0, 0, 0, time.UTC), 2},
		// 서울의 오늘 시작 직후(= 08-10 03:00 KST) / UTC 로는 어제
		{time.Date(2026, 8, 9, 18, 0, 0, 0, time.UTC), 4},
		// 두 시간대 모두 오늘
		{time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC), 1},
		// 서울의 어제 / UTC 로는 그저께 (어느 쪽 UTC 구간에도 안 들어간다)
		{time.Date(2026, 8, 8, 20, 0, 0, 0, time.UTC), 8},
	}
	var recs []store.EventRecord
	for i, a := range at {
		key := fmt.Sprintf("s-tz-%d", i)
		recs = append(recs, llmRecord(key, key+"-turn", a.when, i, llmSpec{Cost: a.cost}))
	}
	f.write(store.Batch{Events: recs})
}

// UTC 자정으로 잘라 답하는 구현은 이 테스트를 통과할 수 없다.
// Asia/Seoul 의 오늘은 UTC 로 전날 15:00 에 시작하기 때문이다.
func TestTodayHonorsTimeZoneBoundary(t *testing.T) {
	f := newFixture(t)
	seedTZBoundary(f)

	tests := []struct {
		name          string
		tz            string
		wantToday     float64
		wantYesterday float64
		wantStart     time.Time
	}{
		{
			name:          "Asia/Seoul 은 UTC 전날 15시에 오늘이 시작한다",
			tz:            seoul,
			wantToday:     4 + 1,
			wantYesterday: 8 + 2,
			wantStart:     time.Date(2026, 8, 9, 15, 0, 0, 0, time.UTC),
		},
		{
			name:          "UTC 는 자정 경계",
			tz:            utc,
			wantToday:     1,
			wantYesterday: 2 + 4,
			wantStart:     time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC),
		},
		{
			name:          "빈 문자열은 UTC 로 본다",
			tz:            "",
			wantToday:     1,
			wantYesterday: 2 + 4,
			wantStart:     time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := f.reader.Today(context.Background(), tc.tz)
			if err != nil {
				t.Fatalf("Today(%q): %v", tc.tz, err)
			}
			if got.Today.CostUSD != tc.wantToday {
				t.Errorf("오늘 cost = %v, want %v", got.Today.CostUSD, tc.wantToday)
			}
			if got.Yesterday.CostUSD != tc.wantYesterday {
				t.Errorf("어제 cost = %v, want %v", got.Yesterday.CostUSD, tc.wantYesterday)
			}
			if got.StartAt != tc.wantStart.Unix() {
				t.Errorf("StartAt = %d(%s), want %d(%s)",
					got.StartAt, time.Unix(got.StartAt, 0).UTC(), tc.wantStart.Unix(), tc.wantStart)
			}
			// 하루는 24시간이어야 하고 어제 구간은 오늘 구간에 정확히 맞닿아야 한다.
			if got.EndAt-got.StartAt != int64((24 * time.Hour).Seconds()) {
				t.Errorf("오늘 길이 = %d초, want 86400", got.EndAt-got.StartAt)
			}
			if got.YesterdayStartAt != got.StartAt-int64((24*time.Hour).Seconds()) {
				t.Errorf("어제 시작 = %d, 오늘 시작 = %d — 두 구간이 맞닿지 않는다", got.YesterdayStartAt, got.StartAt)
			}
		})
	}
}

// 서울의 오늘 날짜는 UTC 날짜와 다를 수 있다. testNow 는 같은 날이지만 경계를 넘는 시각을
// 따로 확인한다 — 화면 제목의 날짜가 하루 어긋나는 흔한 버그다.
func TestTodayDateUsesLocalCalendar(t *testing.T) {
	f := newFixture(t)
	// 2026-08-09 23:00 UTC = 2026-08-10 08:00 KST. UTC 로는 어제, 서울로는 오늘이다.
	f.reader.now = func() time.Time { return time.Date(2026, 8, 9, 23, 0, 0, 0, time.UTC) }

	seoulSummary, err := f.reader.Today(context.Background(), seoul)
	if err != nil {
		t.Fatalf("Today(Seoul): %v", err)
	}
	utcSummary, err := f.reader.Today(context.Background(), utc)
	if err != nil {
		t.Fatalf("Today(UTC): %v", err)
	}
	if seoulSummary.Date != "2026-08-10" {
		t.Errorf("서울 Date = %q, want 2026-08-10", seoulSummary.Date)
	}
	if utcSummary.Date != "2026-08-09" {
		t.Errorf("UTC Date = %q, want 2026-08-09", utcSummary.Date)
	}
	if seoulSummary.TZ != seoul {
		t.Errorf("TZ = %q, want %q", seoulSummary.TZ, seoul)
	}
}

func TestTodayRejectsUnknownTimeZone(t *testing.T) {
	f := newFixture(t)
	for _, tz := range []string{"Mars/Phobos", "Asia/Seoull", "정오"} {
		if _, err := f.reader.Today(context.Background(), tz); err == nil {
			t.Errorf("Today(%q) 가 에러를 내지 않았다 — 잘못된 시간대는 조용히 UTC 로 떨어지면 안 된다", tz)
		}
	}
}

// DST 가 있는 시간대에서 하루는 23시간이거나 25시간이다. 24시간을 빼서 어제를 구하는
// 구현은 전환일 전후로 한 시간씩 어긋난다.
func TestDayRangesHandleDST(t *testing.T) {
	const ny = "America/New_York"
	loc, err := time.LoadLocation(ny)
	if err != nil {
		t.Skipf("%s 시간대를 못 읽는다: %v", ny, err)
	}

	tests := []struct {
		name      string
		local     string // 그 시간대의 정오
		wantHours float64
	}{
		{name: "봄 전환일은 23시간", local: "2026-03-08 12:00", wantHours: 23},
		{name: "가을 전환일은 25시간", local: "2026-11-01 12:00", wantHours: 25},
		{name: "평범한 날은 24시간", local: "2026-08-10 12:00", wantHours: 24},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			at := mustTime(t, "2006-01-02 15:04", tc.local, ny)
			day := dayOf(at, loc)
			if got := day.End.Sub(day.Start).Hours(); got != tc.wantHours {
				t.Fatalf("하루 길이 = %v시간, want %v", got, tc.wantHours)
			}
			// 어제 구간의 끝은 오늘의 시작과 정확히 같아야 한다 (구멍도 겹침도 없다).
			prev := previousDay(day, loc)
			if !prev.End.Equal(day.Start) {
				t.Fatalf("어제 끝 %s != 오늘 시작 %s", prev.End, day.Start)
			}
			// 전환일의 자정은 여전히 자정이다.
			if h, m, s := day.Start.In(loc).Clock(); h != 0 || m != 0 || s != 0 {
				t.Fatalf("현지 하루 시작 = %02d:%02d:%02d, want 00:00:00", h, m, s)
			}
		})
	}
}

// 전환일 다음 날의 "어제" 는 23/25시간짜리 구간이어야 한다.
func TestPreviousDaySpansDSTDay(t *testing.T) {
	const ny = "America/New_York"
	loc, err := time.LoadLocation(ny)
	if err != nil {
		t.Skipf("%s 시간대를 못 읽는다: %v", ny, err)
	}
	at := mustTime(t, "2006-01-02 15:04", "2026-03-09 12:00", ny)
	prev := previousDay(dayOf(at, loc), loc)
	if got := prev.End.Sub(prev.Start).Hours(); got != 23 {
		t.Fatalf("어제(봄 전환일) 길이 = %v시간, want 23 — 24시간을 뺀 구현이다", got)
	}
}

func TestTodayDeltaPercent(t *testing.T) {
	tests := []struct {
		name        string
		today       float64
		yesterday   float64
		wantDelta   float64
		wantHasBase bool
	}{
		{name: "증가", today: 15, yesterday: 10, wantDelta: 50, wantHasBase: true},
		{name: "감소", today: 5, yesterday: 10, wantDelta: -50, wantHasBase: true},
		{name: "오늘 0", today: 0, yesterday: 4, wantDelta: -100, wantHasBase: true},
		{
			// 0 으로 나누면 +Inf 이고 encoding/json 이 Inf 를 직렬화하지 못해 조회 전체가 깨진다.
			name:  "어제 0 이면 증감률이 정의되지 않는다",
			today: 3, yesterday: 0, wantDelta: 0, wantHasBase: false,
		},
		{name: "둘 다 0", today: 0, yesterday: 0, wantDelta: 0, wantHasBase: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			var recs []store.EventRecord
			if tc.today > 0 {
				recs = append(recs, llmRecord("s-today", "t-today",
					time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC), 1, llmSpec{Cost: tc.today}))
			}
			if tc.yesterday > 0 {
				recs = append(recs, llmRecord("s-yday", "t-yday",
					time.Date(2026, 8, 9, 1, 0, 0, 0, time.UTC), 2, llmSpec{Cost: tc.yesterday}))
			}
			if len(recs) > 0 {
				f.write(store.Batch{Events: recs})
			}

			got, err := f.reader.Today(context.Background(), utc)
			if err != nil {
				t.Fatalf("Today: %v", err)
			}
			card := cardFor(t, got.Cards, MetricCostUSD)
			if card.Today != tc.today {
				t.Errorf("카드 오늘 값 = %v, want %v", card.Today, tc.today)
			}
			if card.HasBaseline != tc.wantHasBase {
				t.Errorf("HasBaseline = %v, want %v", card.HasBaseline, tc.wantHasBase)
			}
			if card.DeltaPercent != tc.wantDelta {
				t.Errorf("DeltaPercent = %v, want %v", card.DeltaPercent, tc.wantDelta)
			}
			if math.IsInf(card.DeltaPercent, 0) || math.IsNaN(card.DeltaPercent) {
				t.Errorf("DeltaPercent 가 %v — JSON 직렬화가 불가능한 값이다", card.DeltaPercent)
			}
		})
	}
}

func TestTodayCardsCoverFourMetrics(t *testing.T) {
	f := newFixture(t)
	at := time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC)
	f.write(store.Batch{
		Sessions: []session.Session{
			newSession("s-card-1", at, func(s *session.Session) { s.ActiveSeconds = 40 }),
			newSession("s-card-2", at.Add(time.Minute), func(s *session.Session) { s.ActiveSeconds = 50 }),
			newSession("s-card-3", at.Add(2*time.Minute), func(s *session.Session) { s.ActiveSeconds = 30 }),
		},
		Events: []store.EventRecord{
			llmRecord("s-card-1", "t1", at, 1, llmSpec{Cost: 2, Input: 100, Output: 50, CacheRead: 900}),
		},
	})

	got, err := f.reader.Today(context.Background(), utc)
	if err != nil {
		t.Fatalf("Today: %v", err)
	}
	if len(got.Cards) != 4 {
		t.Fatalf("카드 수 = %d, want 4", len(got.Cards))
	}
	if c := cardFor(t, got.Cards, MetricCostUSD); c.Today != 2 {
		t.Errorf("비용 카드 = %v, want 2", c.Today)
	}
	// 캐시 읽기 토큰은 "쓴 토큰" 에 더하지 않는다 — 이미 한 번 센 입력을 다시 읽은 양이다.
	if c := cardFor(t, got.Cards, MetricTokens); c.Today != 150 {
		t.Errorf("토큰 카드 = %v, want 150 (캐시 토큰 제외)", c.Today)
	}
	if c := cardFor(t, got.Cards, MetricSessions); c.Today != 3 {
		t.Errorf("세션 카드 = %v, want 3", c.Today)
	}
	if c := cardFor(t, got.Cards, MetricActiveSeconds); c.Today != 120 {
		t.Errorf("활동 시간 카드 = %v, want 120", c.Today)
	}
}

// 상단 바의 "3 agents active" — v3 에는 status 컬럼이 없으므로 ended_at IS NULL 이 근거다.
func TestTodayActiveAgents(t *testing.T) {
	f := newFixture(t)
	f.write(store.Batch{Sessions: []session.Session{
		newSession("s1", testNow.Add(-time.Hour), running),
		newSession("s2", testNow.Add(-30*time.Minute), running),
		newSession("s3", testNow.Add(-20*time.Minute), running, codex),
		newSession("s4", testNow.Add(-3*time.Hour)), // completed
	}})

	got, err := f.reader.Today(context.Background(), seoul)
	if err != nil {
		t.Fatalf("Today: %v", err)
	}
	if len(got.ActiveAgents) != 2 {
		t.Fatalf("ActiveAgents = %v, want 2종 (claude_code, codex)", got.ActiveAgents)
	}
	if got.ActiveAgents[0] != vendorClaude || got.ActiveAgents[1] != vendorCodex {
		t.Errorf("ActiveAgents = %v, want [claude_code codex] (정렬 고정)", got.ActiveAgents)
	}
	if got.ActiveSessions != 3 {
		t.Errorf("ActiveSessions = %d, want 3", got.ActiveSessions)
	}
}
