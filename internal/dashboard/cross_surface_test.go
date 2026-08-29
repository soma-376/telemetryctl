package dashboard

// 화면 사이의 합의 (PROJ-97).
//
// 앞선 티켓들의 테스트는 화면마다 **자기 안의** 일관성을 고정했다 — Home 카드가 자기
// 씨앗과 맞는가, Activity 합계가 부풀지 않는가, 세션 상단 값이 턴 합과 같은가.
// 그러나 두 화면을 나란히 놓았을 때 같은 숫자를 말하는지는 어느 파일도 보지 않았다.
// 사용자는 화면을 옮겨 다니며 보고, 어긋난 숫자는 그 자리에서 신뢰를 잃는다.
//
// 여기서는 **한 벌의 데이터에 대해 여러 화면을 동시에 물어** 서로 맞는지 본다.
// 다만 어떤 화면들은 정당하게 다르다 — 자르는 기준이 다르기 때문이다 (home.go 의
// 「합계의 정의」). 그런 자리는 같음이 아니라 **문서가 규정한 관계** 를 단언한다.
//
//	Home 카드      = 사실이 일어난 시각이 선택 날짜 구간에 든 것만의 합
//	Activity 줄    = 그 날 **시작한** 세션. 수치는 세션 생애 전체
//	→ 자정을 넘긴 세션이 있으면 줄 합 > 카드. 목록이 잘리면 줄 합 < 카드.
//	→ 예상 비용(가격표) ≥ 보고 비용 합. 보고값이 없는 호출을 메우기 때문이다.

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/event"
	"github.com/your-org/pulsemetry/internal/session"
	"github.com/your-org/pulsemetry/internal/store"
	"github.com/your-org/pulsemetry/internal/vendorlimit"
)

// crossDay 는 이 파일이 검사하는 "그 날" 이다. testNow(2026-08-10 02:00 UTC =
// 서울 11:00) 기준 어제라, 하루가 통째로 과거이면서 진행 중 세션과 섞이지 않는다.
const crossDay = "2026-08-09"

// seedCleanDay 는 자정을 넘지 않고 잘리지도 않는 하루를 만든다.
//
// 세션 셋이 전부 crossDay 안에서 시작하고 끝난다. 이 상태에서만 "줄 합 = 카드" 가
// 성립한다 — 그 전제가 깨지면 아래 단언은 버그가 아니라 정의를 확인하는 것이 된다.
func seedCleanDay(t *testing.T, f *fixture) {
	t.Helper()
	base := mustTime(t, "2006-01-02 15:04", crossDay+" 09:00", seoul)

	specs := []struct {
		key    string
		at     time.Time
		vendor func(*session.Session)
		llm    llmSpec
	}{
		{key: "cs-a", at: base, llm: llmSpec{Model: "claude-sonnet-4-5", Cost: 0.5, Input: 100, Output: 40, CacheRead: 7}},
		{key: "cs-b", at: base.Add(2 * time.Hour), llm: llmSpec{Model: "claude-opus-4-1", Cost: 1.25, Input: 300, Output: 90}},
		{key: "cs-c", at: base.Add(5 * time.Hour), vendor: codex,
			llm: llmSpec{Vendor: vendorCodex, Model: "gpt-5-codex", Cost: 0.2, Input: 50, Output: 25}},
	}
	for i, s := range specs {
		mods := []func(*session.Session){}
		if s.vendor != nil {
			mods = append(mods, s.vendor)
		}
		sess := newSession(s.key, s.at, mods...)
		vendor := vendorClaude
		if s.llm.Vendor != "" {
			vendor = s.llm.Vendor
		}
		turn := s.key + "-t1"
		f.write(store.Batch{
			Sessions: []session.Session{sess},
			Events: []store.EventRecord{
				promptRecord(s.key, turn, s.at, 1, "인증 토큰 검증 프록시 "+s.key),
				llmRecord(s.key, turn, s.at.Add(time.Minute), 2, s.llm),
				toolRecord(s.key, turn, s.key+"-call-1", s.at.Add(2*time.Minute), 3, toolSpec{
					Vendor: vendor, ToolName: "Edit", Success: event.Some(true),
					Target: workspaceA + "/apply.go",
					File:   fileChange(workspaceA+"/apply.go", int64(10+i), int64(2+i)),
				}),
				toolRecord(s.key, turn, s.key+"-call-2", s.at.Add(3*time.Minute), 4, toolSpec{
					Vendor: vendor, ToolName: "Bash", Success: event.Some(false), ErrorType: "exit_1",
				}),
			},
		})
	}
}

// homeAndActivity 는 같은 날을 두 화면으로 동시에 조회한다.
func homeAndActivity(t *testing.T, f *fixture, date string) (HomeSummary, ActivityPage) {
	t.Helper()
	ctx := context.Background()
	home, err := f.reader.Home(ctx, HomeQuery{TZ: seoul, Date: date})
	if err != nil {
		t.Fatalf("Home(%s): %v", date, err)
	}
	page, err := f.reader.Activity(ctx, ActivityQuery{
		Since: home.StartAt, Until: home.EndAt, Limit: maxActivityLimit,
	})
	if err != nil {
		t.Fatalf("Activity(%s): %v", date, err)
	}
	if page.HasMore {
		t.Fatalf("Activity 가 잘렸다 — 이 파일의 단언은 한 페이지에 다 들어오는 것을 전제한다")
	}
	return home, page
}

// activitySums 는 Activity 줄들의 합이다.
type activitySums struct {
	Rows        int
	Tokens      int64
	ToolCalls   int64
	ToolErrors  int64
	ToolRejects int64
	CostUSD     float64
	Lines       int64
}

func sumActivity(rows []ActivityRow) activitySums {
	var s activitySums
	s.Rows = len(rows)
	for _, r := range rows {
		s.Tokens += r.InputTokens + r.OutputTokens
		s.ToolCalls += r.ToolCalls
		s.ToolErrors += r.ToolErrors
		s.ToolRejects += r.ToolRejects
		s.CostUSD += r.CostUSD
		s.Lines += r.LinesAdded + r.LinesRemoved
	}
	return s
}

// ── Home ↔ Activity ─────────────────────────────────────────────────────────

// TestCrossSurface_HomeDayTotalsEqualActivityRowSums 는 자정을 넘긴 세션도 잘린 목록도
// 없는 하루에서 두 화면의 합이 정확히 같은지 본다.
//
// 이 단언이 깨지는 방식은 둘 중 하나다 — 한쪽이 구간을 다르게 자르거나, 한쪽이 조인으로
// 행을 부풀린다. 두 화면 중 어느 쪽이 틀렸는지는 각 화면의 자기 일관성 테스트가 가른다.
func TestCrossSurface_HomeDayTotalsEqualActivityRowSums(t *testing.T) {
	f := newFixture(t)
	seedCleanDay(t, f)

	home, page := homeAndActivity(t, f, crossDay)
	got := sumActivity(page.Rows)

	cases := []struct {
		name string
		home int64
		acti int64
	}{
		{"세션 수", home.Totals.SessionsStarted, int64(got.Rows)},
		{"토큰", home.Totals.Tokens(), got.Tokens},
		{"툴 호출", home.Totals.ToolCalls, got.ToolCalls},
		{"툴 거부", home.Totals.ToolRejects, got.ToolRejects},
		{"라인", home.Totals.LinesAdded + home.Totals.LinesRemoved, got.Lines},
	}
	for _, tc := range cases {
		if tc.home != tc.acti {
			t.Errorf("%s: Home 카드 = %d, Activity 줄 합 = %d", tc.name, tc.home, tc.acti)
		}
	}
	if got.Rows == 0 {
		t.Fatal("Activity 줄이 없다 — 이 테스트는 아무것도 검증하지 못했다")
	}
	// 보고 비용도 같은 구간을 본다. float 합산이라 마지막 자리만 허용한다.
	if diff := home.Totals.CostUSD - got.CostUSD; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("보고 비용: Home = %v, Activity 줄 합 = %v", home.Totals.CostUSD, got.CostUSD)
	}
}

// TestCrossSurface_HomeRecentMatchesActivityRows 는 Home 의 「최근 활동」과 Activity
// 목록이 같은 세션을 같은 순서로 보는지 본다. 두 화면이 다른 순서를 그리면 사용자는
// 같은 목록의 두 판본을 보게 된다.
func TestCrossSurface_HomeRecentMatchesActivityRows(t *testing.T) {
	f := newFixture(t)
	seedCleanDay(t, f)

	home, page := homeAndActivity(t, f, crossDay)
	if len(home.Recent) != len(page.Rows) {
		t.Fatalf("Home 최근 = %d건, Activity 줄 = %d건", len(home.Recent), len(page.Rows))
	}
	if home.RecentTruncated {
		t.Fatal("Home 목록이 잘렸다 — 이 단언의 전제가 깨진다")
	}
	for i := range home.Recent {
		recent, row := home.Recent[i], page.Rows[i]
		if recent.ID != row.ID {
			t.Errorf("[%d] 세션이 다르다: Home = %d, Activity = %d", i, recent.ID, row.ID)
			continue
		}
		if recent.Tokens != row.InputTokens+row.OutputTokens {
			t.Errorf("[%d] 토큰이 다르다: Home = %d, Activity = %d",
				i, recent.Tokens, row.InputTokens+row.OutputTokens)
		}
		if recent.Status != row.Status || recent.Vendor != row.Vendor {
			t.Errorf("[%d] 상태·벤더가 다르다: Home = %s/%s, Activity = %s/%s",
				i, recent.Status, recent.Vendor, row.Status, row.Vendor)
		}
		// RecentSession.Cost 는 가격표 기준이고 SessionRow.CostUSD 는 보고값 합이다.
		// 보고값이 없는 호출을 메우므로 전자가 작을 수 없다.
		if recent.Cost.Total.USD < row.CostUSD-1e-9 {
			t.Errorf("[%d] 예상 비용 %v 이 보고 비용 %v 보다 작다",
				i, recent.Cost.Total.USD, row.CostUSD)
		}
	}
}

// TestCrossSurface_MidnightCrossingMakesRowsExceedCards 는 **정당한 불일치** 를 고정한다.
//
// 자정을 넘겨 이어진 세션의 다음 날 몫은 Activity 줄(생애 전체)에는 들어 있고 그 날의
// Home 카드(구간 안의 사실만)에는 없다. 이 관계가 뒤집히면 둘 중 하나가 구간을 잘못
// 자른 것이다 (home.go 「합계의 정의」).
func TestCrossSurface_MidnightCrossingMakesRowsExceedCards(t *testing.T) {
	f := newFixture(t)
	seedCleanDay(t, f)

	// 23:30 에 시작해 다음 날 00:30 에 호출이 하나 더 있는 세션.
	late := mustTime(t, "2006-01-02 15:04", crossDay+" 23:30", seoul)
	f.write(store.Batch{
		Sessions: []session.Session{newSession("cs-midnight", late)},
		Events: []store.EventRecord{
			promptRecord("cs-midnight", "cs-midnight-t1", late, 1, "자정을 넘긴 작업"),
			llmRecord("cs-midnight", "cs-midnight-t1", late.Add(10*time.Minute), 2,
				llmSpec{Model: "claude-sonnet-4-5", Cost: 0.1, Input: 20, Output: 10}),
			// 다음 날로 넘어간 호출.
			llmRecord("cs-midnight", "cs-midnight-t1", late.Add(time.Hour), 3,
				llmSpec{Model: "claude-sonnet-4-5", Cost: 0.9, Input: 500, Output: 200}),
		},
	})

	home, page := homeAndActivity(t, f, crossDay)
	got := sumActivity(page.Rows)

	if got.Tokens <= home.Totals.Tokens() {
		t.Errorf("줄 합 %d ≤ 카드 %d — 자정을 넘긴 몫이 어느 쪽에도 반영되지 않았다",
			got.Tokens, home.Totals.Tokens())
	}
	// 넘어간 몫은 정확히 다음 날 카드에 있다. 사실은 사라지지 않고 옮겨 갈 뿐이다.
	next, err := f.reader.Home(context.Background(), HomeQuery{TZ: seoul, Date: "2026-08-10"})
	if err != nil {
		t.Fatalf("Home(다음 날): %v", err)
	}
	if next.Totals.Tokens() != 700 {
		t.Errorf("다음 날 카드 토큰 = %d, want 700 (자정 이후 호출)", next.Totals.Tokens())
	}
	// 그 세션은 다음 날 목록에는 없다 — 시작한 날에만 줄이 선다.
	nextPage, err := f.reader.Activity(context.Background(), ActivityQuery{
		Since: next.StartAt, Until: next.EndAt, Limit: maxActivityLimit,
	})
	if err != nil {
		t.Fatalf("Activity(다음 날): %v", err)
	}
	if len(nextPage.Rows) != 0 {
		t.Errorf("다음 날 Activity 줄 = %d, want 0 (그 날 시작한 세션이 없다)", len(nextPage.Rows))
	}
}

// TestCrossSurface_TruncatedRecentIsSmallerThanCards 는 반대 방향의 정당한 불일치다.
// 목록이 RecentLimit 에서 잘리면 행 합은 카드보다 작고, RecentTruncated 가 그 사실을 알린다.
func TestCrossSurface_TruncatedRecentIsSmallerThanCards(t *testing.T) {
	f := newFixture(t)
	seedCleanDay(t, f)

	home, err := f.reader.Home(context.Background(),
		HomeQuery{TZ: seoul, Date: crossDay, RecentLimit: 1})
	if err != nil {
		t.Fatalf("Home: %v", err)
	}
	if !home.RecentTruncated {
		t.Fatal("RecentTruncated = false — 세 세션을 1개로 잘랐는데 알리지 않는다")
	}
	if len(home.Recent) != 1 {
		t.Fatalf("최근 세션 = %d건, want 1", len(home.Recent))
	}
	if home.Recent[0].Tokens >= home.Totals.Tokens() {
		t.Errorf("잘린 목록의 합 %d ≥ 카드 %d — 자른 만큼 작아야 한다",
			home.Recent[0].Tokens, home.Totals.Tokens())
	}
	// 카드는 자르기와 무관하다. 상한을 바꿔도 같은 값이어야 한다.
	full, err := f.reader.Home(context.Background(), HomeQuery{TZ: seoul, Date: crossDay})
	if err != nil {
		t.Fatalf("Home(기본 상한): %v", err)
	}
	if full.Totals != home.Totals {
		t.Errorf("RecentLimit 이 카드를 바꿨다:\n상한 1 = %+v\n기본  = %+v", home.Totals, full.Totals)
	}
}

// ── Activity ↔ Session Detail ↔ SessionMetrics ──────────────────────────────

// TestCrossSurface_SessionDetailMatchesItsActivityRow 는 목록에서 클릭해 들어간 상세가
// 목록의 그 줄과 같은 세션·같은 숫자인지 본다.
func TestCrossSurface_SessionDetailMatchesItsActivityRow(t *testing.T) {
	f := newFixture(t)
	seedCleanDay(t, f)
	ctx := context.Background()

	_, page := homeAndActivity(t, f, crossDay)
	if len(page.Rows) == 0 {
		t.Fatal("Activity 줄이 없다")
	}
	for _, row := range page.Rows {
		detail, err := f.reader.Session(ctx, row.ID)
		if err != nil {
			t.Fatalf("Session(%d): %v", row.ID, err)
		}
		if !detail.Found {
			t.Errorf("세션 %d: 목록에는 있는데 상세가 Found=false 다", row.ID)
			continue
		}
		// 목록 줄과 상세 머리말은 같은 구조체다. 다르면 둘 중 하나가 다른 질의를 쓴 것이다.
		// EndedAt 이 포인터라 == 는 주소를 본다. 값 비교여야 한다.
		if !reflect.DeepEqual(detail.Session, row.SessionRow) {
			t.Errorf("세션 %d 의 줄과 상세가 다르다:\n목록 = %+v\n상세 = %+v",
				row.ID, row.SessionRow, detail.Session)
		}

		metrics, err := f.reader.SessionMetrics(ctx, SessionMetricsQuery{SessionID: row.ID})
		if err != nil {
			t.Fatalf("SessionMetrics(%d): %v", row.ID, err)
		}
		if !metrics.Found {
			t.Errorf("세션 %d: SessionMetrics 가 Found=false 다", row.ID)
			continue
		}
		checks := []struct {
			name string
			a, b int64
		}{
			{"툴 호출", metrics.Totals.ToolCalls, row.ToolCalls},
			{"툴 실패", metrics.Totals.ToolErrors, row.ToolErrors},
			{"툴 거부", metrics.Totals.ToolRejects, row.ToolRejects},
			{"LLM 호출", metrics.Totals.LLMCalls, row.APIRequests},
			{"입력 토큰", metrics.Totals.Tokens.Input, row.InputTokens},
			{"출력 토큰", metrics.Totals.Tokens.Output, row.OutputTokens},
			{"캐시 읽기", metrics.Totals.Tokens.CacheRead, row.CacheReadTokens},
			{"캐시 쓰기", metrics.Totals.Tokens.CacheWrite, row.CacheCreationTokens},
		}
		for _, c := range checks {
			if c.a != c.b {
				t.Errorf("세션 %d %s: SessionMetrics = %d, Activity 줄 = %d",
					row.ID, c.name, c.a, c.b)
			}
		}
		// 지표 화면의 비용은 가격표 기준이라 보고값 합보다 작을 수 없다.
		if metrics.Totals.Cost.Total.USD < row.CostUSD-1e-9 {
			t.Errorf("세션 %d: 예상 비용 %v < 보고 비용 %v",
				row.ID, metrics.Totals.Cost.Total.USD, row.CostUSD)
		}
	}
}

// TestCrossSurface_FileChangesMatchSessionDetailFiles 는 파일 변경 화면과 세션 상세의
// 파일 줄이 같은 사실을 보는지 본다.
//
// 두 화면은 **정당하게 다르다** — 상세는 목록용이라 미관측 줄 수를 0 으로 눕히고
// (sessionFilesSQL 의 COALESCE), 파일 변경 화면은 미관측을 null 로 보존한다.
// 그래서 같음이 아니라 "눕힌 값이 서로 맞는가" 를 본다.
func TestCrossSurface_FileChangesMatchSessionDetailFiles(t *testing.T) {
	f := newFixture(t)
	at := testNow.Add(-3 * time.Hour)

	// 줄 수를 관측한 변경과 관측하지 못한 변경을 섞는다.
	unobserved := session.FileChange{
		Path:      workspaceA + "/README.md",
		Operation: session.OperationModify,
	}
	f.write(store.Batch{
		Sessions: []session.Session{newSession("cs-files", at)},
		Events: []store.EventRecord{
			promptRecord("cs-files", "cs-files-t1", at, 1, "파일 변경 대조"),
			toolRecord("cs-files", "cs-files-t1", "cs-files-c1", at.Add(time.Minute), 2, toolSpec{
				ToolName: "Edit", Success: event.Some(true),
				Target: workspaceA + "/apply.go",
				File:   fileChange(workspaceA+"/apply.go", 12, 3),
			}),
			toolRecord("cs-files", "cs-files-t1", "cs-files-c2", at.Add(2*time.Minute), 3, toolSpec{
				ToolName: "Edit", Success: event.Some(true),
				Target: workspaceA + "/apply.go",
				File:   fileChange(workspaceA+"/apply.go", 4, 1),
			}),
			toolRecord("cs-files", "cs-files-t1", "cs-files-c3", at.Add(3*time.Minute), 4, toolSpec{
				ToolName: "Write", Success: event.Some(true),
				Target: workspaceA + "/README.md",
				File:   unobserved,
			}),
		},
	})

	ctx := context.Background()
	id := f.sessionID(vendorClaude, "cs-files")

	detail, err := f.reader.Session(ctx, id)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	changes, err := f.reader.FileChanges(ctx, id)
	if err != nil {
		t.Fatalf("FileChanges: %v", err)
	}
	if !detail.Found || !changes.Found {
		t.Fatalf("Found: detail=%v changes=%v", detail.Found, changes.Found)
	}
	if int64(len(detail.Files)) != changes.Totals.Files {
		t.Fatalf("파일 수가 다르다: 상세 = %d, 파일변경 = %d", len(detail.Files), changes.Totals.Files)
	}

	byPath := map[string]FileChangeSummary{}
	for _, s := range changes.Files {
		byPath[s.FilePath] = s
	}
	var edits int64
	for _, file := range detail.Files {
		sum, ok := byPath[file.FilePath]
		if !ok {
			t.Errorf("상세의 %q 가 파일 변경 화면에 없다", file.FilePath)
			continue
		}
		edits += file.Edits
		if file.Edits != sum.Changes {
			t.Errorf("%q 변경 건수: 상세 = %d, 파일변경 = %d", file.FilePath, file.Edits, sum.Changes)
		}
		if file.LinesAdded != sum.Additions.Or(0) || file.LinesRemoved != sum.Deletions.Or(0) {
			t.Errorf("%q 줄 수: 상세 = +%d/-%d, 파일변경 = %v/%v",
				file.FilePath, file.LinesAdded, file.LinesRemoved, sum.Additions, sum.Deletions)
		}
		if file.LastTS != sum.LastTS {
			t.Errorf("%q 마지막 시각: 상세 = %d, 파일변경 = %d", file.FilePath, file.LastTS, sum.LastTS)
		}
	}
	if edits != changes.Totals.Changes {
		t.Errorf("변경 건수 합: 상세 = %d, 파일변경 합계 = %d", edits, changes.Totals.Changes)
	}

	// 미관측은 상세에서 0 으로 눕고 파일 변경 화면에서는 null 로 남는다. 그 차이가
	// 사라지면 화면이 "0줄을 바꿨다" 고 단정하게 된다 (LineCount 주석).
	readme := byPath[workspaceA+"/README.md"]
	if readme.Additions.Observed() {
		t.Errorf("README.md 의 추가 줄이 관측된 것으로 나온다: %v", readme.Additions)
	}
	if readme.UnobservedAdditions != 1 {
		t.Errorf("미관측 건수 = %d, want 1", readme.UnobservedAdditions)
	}

	// 세션 줄의 라인 수도 같은 원본에서 온다.
	if detail.Session.LinesAdded != changes.Totals.Additions.Or(0) {
		t.Errorf("세션 줄 추가 = %d, 파일변경 합계 = %v",
			detail.Session.LinesAdded, changes.Totals.Additions)
	}
}

// ── Home ↔ Breakdown ↔ Tray ─────────────────────────────────────────────────

// TestCrossSurface_BreakdownVendorRowsSumToHomeTotals 는 축별 집계의 합이 그 날 카드와
// 같은지 본다. 축을 나눠도 전체는 변하지 않아야 한다.
func TestCrossSurface_BreakdownVendorRowsSumToHomeTotals(t *testing.T) {
	f := newFixture(t)
	seedCleanDay(t, f)
	ctx := context.Background()

	home, err := f.reader.Home(ctx, HomeQuery{TZ: seoul, Date: crossDay})
	if err != nil {
		t.Fatalf("Home: %v", err)
	}
	for _, dim := range []Dim{DimVendor, DimModel, DimProject} {
		t.Run(string(dim), func(t *testing.T) {
			rows, err := f.reader.Breakdown(ctx, BreakdownQuery{
				Dim: dim, TZ: seoul, From: home.StartAt, To: home.EndAt, Limit: 100,
			})
			if err != nil {
				t.Fatalf("Breakdown(%s): %v", dim, err)
			}
			if len(rows) == 0 {
				t.Fatalf("%s 행이 없다 — 이 하위 테스트는 아무것도 검증하지 못했다", dim)
			}
			var tokens int64
			for _, r := range rows {
				tokens += r.Tokens()
			}
			if tokens != home.Totals.Tokens() {
				t.Errorf("%s 행 토큰 합 = %d, Home 카드 = %d", dim, tokens, home.Totals.Tokens())
			}
		})
	}
}

// TestCrossSurface_TrayRepeatsHomeNumbers 는 트레이가 본 화면과 같은 숫자를 말하는지 본다.
// 트레이는 자기 SQL 을 쓰지 않고 Status·Home 을 그대로 부르는 것이 계약이다 (tray.go).
func TestCrossSurface_TrayRepeatsHomeNumbers(t *testing.T) {
	f := newFixture(t)
	// 트레이는 "오늘" 을 본다. testNow 기준 오늘에 세션을 하나 둔다.
	at := testNow.Add(-30 * time.Minute)
	f.write(store.Batch{
		Sessions: []session.Session{newSession("cs-tray", at, running)},
		Events: []store.EventRecord{
			promptRecord("cs-tray", "cs-tray-t1", at, 1, "트레이 대조"),
			llmRecord("cs-tray", "cs-tray-t1", at, 2, llmSpec{Model: "claude-sonnet-4-5", Cost: 0.3, Input: 60, Output: 20}),
		},
	})

	ctx := context.Background()
	m, _, _ := newTestMonitor(f.reader, vendorlimit.Snapshot{
		Results:    []vendorlimit.Result{availableResult(vendorlimit.VendorClaudeCode)},
		ObservedAt: "2026-08-10T02:00:00Z",
	})
	snap, err := m.Snapshot(ctx, TrayQuery{TZ: seoul})
	if err != nil {
		t.Fatalf("Tray: %v", err)
	}
	home, err := f.reader.Home(ctx, HomeQuery{TZ: seoul})
	if err != nil {
		t.Fatalf("Home: %v", err)
	}
	if snap.Date != home.Date || snap.TZ != home.TZ {
		t.Errorf("날짜·시간대가 다르다: tray = %s/%s, home = %s/%s",
			snap.Date, snap.TZ, home.Date, home.TZ)
	}
	if snap.ActiveSessions != home.ActiveSessions {
		t.Errorf("활성 세션: tray = %d, home = %d", snap.ActiveSessions, home.ActiveSessions)
	}
	if len(snap.Recent) != len(home.Recent) {
		t.Fatalf("최근 세션: tray = %d건, home = %d건", len(snap.Recent), len(home.Recent))
	}
	for i := range snap.Recent {
		if !reflect.DeepEqual(snap.Recent[i], home.Recent[i]) {
			t.Errorf("[%d] 최근 세션이 다르다:\ntray = %+v\nhome = %+v",
				i, snap.Recent[i], home.Recent[i])
		}
	}

	// Status 도 같은 근거를 본다.
	st, err := f.reader.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if snap.Monitoring.RunningSessions != st.RunningSessions {
		t.Errorf("진행 중 세션: tray = %d, status = %d",
			snap.Monitoring.RunningSessions, st.RunningSessions)
	}
	if snap.Monitoring.LastEventAt != st.NewestEventAt {
		t.Errorf("마지막 이벤트: tray = %d, status = %d",
			snap.Monitoring.LastEventAt, st.NewestEventAt)
	}
}

// ── Classifier ↔ Session ────────────────────────────────────────────────────

// TestCrossSurface_ClassifierCoversTheSessionsTurns 는 분류 결과의 턴 수가 지표 화면의
// 턴 수와 같은지 본다. 분류가 일부 턴만 보면 화면의 비율이 조용히 왜곡된다.
func TestCrossSurface_ClassifierCoversTheSessionsTurns(t *testing.T) {
	f := newFixture(t)
	seedCleanDay(t, f)
	ctx := context.Background()

	id := f.sessionID(vendorClaude, "cs-a")
	metrics, err := f.reader.SessionMetrics(ctx, SessionMetricsQuery{SessionID: id})
	if err != nil {
		t.Fatalf("SessionMetrics: %v", err)
	}
	got, err := NewClassifier(f.reader).Session(ctx, id)
	if err != nil {
		t.Fatalf("Classifier.Session: %v", err)
	}

	// 분류 입력은 **실제 턴** 이다. 가상 턴(turn_index IS NULL)은 세션 수준 이벤트의
	// 자리라 분류의 단위가 아니다 — 그래서 PromptTurns 와 맞춘다.
	if int64(got.TurnCount) != metrics.Totals.PromptTurns {
		t.Errorf("분류 턴 = %d, 지표의 실제 턴 = %d", got.TurnCount, metrics.Totals.PromptTurns)
	}
	if len(got.Turns) != got.TurnCount {
		t.Errorf("turns 목록 = %d건, TurnCount = %d", len(got.Turns), got.TurnCount)
	}
	// 단계는 턴을 빠짐없이 덮는다.
	var covered int
	for _, p := range got.Phases {
		covered += p.TurnCount
	}
	if covered != got.TurnCount {
		t.Errorf("단계가 덮은 턴 = %d, 전체 = %d", covered, got.TurnCount)
	}
}
