package dashboard

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/session"
	"github.com/your-org/pulsemetry/internal/store"
)

// ── 보조 ────────────────────────────────────────────────────────────────────

// activityKeys 는 결과 줄을 session_key 로 옮긴다. id 는 저장 시점에 정해져 테스트가
// 미리 알 수 없으므로 단언은 키로 한다.
func activityKeys(rows []ActivityRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.SessionKey)
	}
	return out
}

// drainActivity 는 커서를 따라 끝까지 받아 온다. "더 불러오기" 를 사용자가 반복해 누르는
// 것과 같은 경로다 — 페이지 경계의 중복·누락은 여기서만 드러난다.
//
// pages 는 실제로 돈 페이지 수다. 무한 반복은 t.Fatal 로 잡는다.
func drainActivity(t *testing.T, r *Reader, q ActivityQuery) (rows []ActivityRow, pages int) {
	t.Helper()
	ctx := context.Background()
	for {
		page, err := r.Activity(ctx, q)
		if err != nil {
			t.Fatalf("Activity(%+v): %v", q, err)
		}
		pages++
		rows = append(rows, page.Rows...)
		if !page.HasMore {
			return rows, pages
		}
		if pages > 50 {
			t.Fatalf("페이지를 %d번 넘겼는데 끝나지 않았다 — HasMore 가 마지막을 구분하지 못한다", pages)
		}
		q.Cursor = page.NextCursor
	}
}

// ── 목록·필터 ───────────────────────────────────────────────────────────────

// seedActivity 는 벤더·프로젝트·상태·시작 시각이 서로 다른 세션 셋을 놓는다.
// 필터 하나가 잘못 배선되면 반드시 어느 조합에서 틀린 목록이 나온다.
func seedActivity(f *fixture) {
	at := testNow.Add(-3 * time.Hour)
	f.write(store.Batch{
		Sessions: []session.Session{
			newSession("a-1", at, title("인증 프록시 구현"), func(s *session.Session) {
				s.WorkspacePath = workspaceA
			}),
			newSession("a-2", testNow.Add(-2*time.Hour), title("리팩터링"), func(s *session.Session) {
				codex(s)
				s.WorkspacePath = workspaceB
			}),
			newSession("a-3", testNow.Add(-time.Hour), title("디버깅"), func(s *session.Session) {
				running(s)
				s.WorkspacePath = workspaceB
			}),
		},
		Events: []store.EventRecord{
			promptRecord("a-1", "t-a1", at, 1, "토큰 검증 흐름을 프록시로 옮겨줘"),
			toolRecord("a-1", "t-a1", "call-a1", at.Add(time.Second), 2, toolSpec{
				ToolName: "Edit",
				Target:   workspaceA + "/proxy_handler.go",
				File:     fileChange(workspaceA+"/proxy_handler.go", 4, 1),
			}),
		},
	})
}

func TestActivityFilterCombinations(t *testing.T) {
	f := newFixture(t)
	seedActivity(f)

	tests := []struct {
		name string
		q    ActivityQuery
		want []string
	}{
		{name: "필터 없음은 최근 순 전체", q: ActivityQuery{}, want: []string{"a-3", "a-2", "a-1"}},
		{name: "벤더", q: ActivityQuery{Vendors: []string{vendorCodex}}, want: []string{"a-2"}},
		{
			name: "벤더 다중 선택은 OR",
			q:    ActivityQuery{Vendors: []string{vendorCodex, vendorClaude}},
			want: []string{"a-3", "a-2", "a-1"},
		},
		{name: "프로젝트", q: ActivityQuery{Projects: []string{workspaceB}}, want: []string{"a-3", "a-2"}},
		{name: "진행 상태", q: ActivityQuery{Status: []string{StatusRunning}}, want: []string{"a-3"}},
		{
			name: "완료 상태",
			q:    ActivityQuery{Status: []string{StatusCompleted}},
			want: []string{"a-2", "a-1"},
		},
		{
			name: "날짜 Since 는 포함",
			q:    ActivityQuery{Since: testNow.Add(-2 * time.Hour).Unix()},
			want: []string{"a-3", "a-2"},
		},
		{
			name: "날짜 Until 은 배타",
			q:    ActivityQuery{Until: testNow.Add(-2 * time.Hour).Unix()},
			want: []string{"a-1"},
		},
		{
			name: "날짜·벤더·프로젝트·상태 네 조건 조합",
			q: ActivityQuery{
				Since:    testNow.Add(-90 * time.Minute).Unix(),
				Vendors:  []string{vendorClaude},
				Projects: []string{workspaceB},
				Status:   []string{StatusRunning},
			},
			want: []string{"a-3"},
		},
		{
			name: "조합이 서로 배타면 빈 목록",
			q:    ActivityQuery{Vendors: []string{vendorCodex}, Status: []string{StatusRunning}},
			want: []string{},
		},
		{
			name: "빈 문자열 값은 거르지 않는다",
			q:    ActivityQuery{Vendors: []string{""}, Projects: []string{""}},
			want: []string{"a-3", "a-2", "a-1"},
		},
		{
			// ADR 0009: 어휘에는 남지만 어떤 행에도 부여되지 않는다.
			name: "v3 가 산출하지 않는 상태는 빈 목록",
			q:    ActivityQuery{Status: []string{"abandoned", "handoff"}},
			want: []string{},
		},
		{
			name: "검색어와 필터를 함께",
			q:    ActivityQuery{Text: "프록시", Vendors: []string{vendorClaude}},
			want: []string{"a-1"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			page, err := f.reader.Activity(context.Background(), tc.q)
			if err != nil {
				t.Fatalf("Activity: %v", err)
			}
			if got := activityKeys(page.Rows); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("목록 = %v, want %v", got, tc.want)
			}
			if page.Rows == nil {
				t.Error("Rows 가 nil — JSON 에서 null 이 되어 프런트엔드가 터진다")
			}
		})
	}
}

// 목록 한 줄이 화면이 요구하는 값을 실제로 담고 있는지 — 시작·경로·소요·토큰·비용·상태.
func TestActivityRowCarriesScreenColumns(t *testing.T) {
	f := newFixture(t)
	at := testNow.Add(-time.Hour)
	f.write(store.Batch{
		Sessions: []session.Session{newSession("a-cols", at)},
		Events: []store.EventRecord{
			llmRecord("a-cols", "t-cols", at, 1, llmSpec{Cost: 1.5, Input: 100, Output: 20}),
		},
	})

	page, err := f.reader.Activity(context.Background(), ActivityQuery{})
	if err != nil {
		t.Fatalf("Activity: %v", err)
	}
	if len(page.Rows) != 1 {
		t.Fatalf("목록 = %d건, want 1", len(page.Rows))
	}
	row := page.Rows[0]
	switch {
	case row.StartedAt != at.Unix():
		t.Errorf("StartedAt = %d, want %d", row.StartedAt, at.Unix())
	case row.WorkspacePath != workspaceA:
		t.Errorf("WorkspacePath = %q", row.WorkspacePath)
	case row.ProjectName != "telemetryctl":
		t.Errorf("ProjectName = %q", row.ProjectName)
	case row.DurationMS != 600*1000:
		t.Errorf("DurationMS = %d", row.DurationMS)
	case row.InputTokens != 100 || row.OutputTokens != 20:
		t.Errorf("토큰 = %d/%d", row.InputTokens, row.OutputTokens)
	case row.CostUSD != 1.5:
		t.Errorf("CostUSD = %v", row.CostUSD)
	case row.Status != StatusCompleted:
		t.Errorf("Status = %q", row.Status)
	case row.ID <= 0:
		t.Error("ID 가 비었다 — Session() 에 그대로 넘길 수 있어야 한다")
	}
}

// ── 페이지네이션 ────────────────────────────────────────────────────────────

// 인수조건: 동일 시작 시각에서도 중복·누락이 없다.
//
// started_at 만으로 정렬하면 같은 초에 시작한 세션들의 순서가 질의마다 달라져 커서가
// 가리키는 위치가 흔들린다. 2순위 id 가 그것을 고정한다.
func TestActivityPaginationCoversEveryRowExactlyOnce(t *testing.T) {
	const total = 7

	tests := []struct {
		name  string
		same  bool // 모든 세션의 started_at 이 같은가
		limit int
		pages int
	}{
		{name: "시작 시각이 모두 같다", same: true, limit: 2, pages: 4},
		{name: "시작 시각이 모두 같고 한 페이지에 다 들어간다", same: true, limit: 7, pages: 1},
		{name: "시작 시각이 모두 다르다", same: false, limit: 3, pages: 3},
		{name: "한 줄씩 넘긴다", same: true, limit: 1, pages: 7},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			sessions := make([]session.Session, 0, total)
			for i := range total {
				at := testNow.Add(-time.Hour)
				if !tc.same {
					at = testNow.Add(-time.Duration(i+1) * time.Hour)
				}
				sessions = append(sessions, newSession(fmt.Sprintf("p-%d", i), at))
			}
			f.write(store.Batch{Sessions: sessions})

			rows, pages := drainActivity(t, f.reader, ActivityQuery{Limit: tc.limit})
			if pages != tc.pages {
				t.Errorf("페이지 = %d, want %d", pages, tc.pages)
			}
			if len(rows) != total {
				t.Fatalf("총 %d건을 받았다, want %d — 페이지 경계에서 새거나 겹쳤다", len(rows), total)
			}

			seen := map[string]int{}
			for _, r := range rows {
				seen[r.SessionKey]++
			}
			for i := range total {
				key := fmt.Sprintf("p-%d", i)
				if seen[key] != 1 {
					t.Errorf("%s 가 %d번 나왔다, want 1회", key, seen[key])
				}
			}
			// 정렬이 started_at DESC, id DESC 로 유지돼야 커서가 의미를 갖는다.
			for i := 1; i < len(rows); i++ {
				prev, cur := rows[i-1], rows[i]
				if prev.StartedAt < cur.StartedAt ||
					(prev.StartedAt == cur.StartedAt && prev.ID <= cur.ID) {
					t.Fatalf("정렬이 깨졌다: (%d,%d) 다음에 (%d,%d)",
						prev.StartedAt, prev.ID, cur.StartedAt, cur.ID)
				}
			}
		})
	}
}

// 인수조건: 더 불러오기가 마지막 페이지를 명확히 구분한다.
//
// 줄 수가 Limit 에 딱 맞아떨어질 때가 함정이다. "받은 게 Limit 개면 더 있다" 로 판정하면
// 마지막 페이지에서 한 번 더 부르게 되고, 그 빈 응답이 화면에 깜빡임으로 남는다.
func TestActivityLastPageIsDistinguishable(t *testing.T) {
	f := newFixture(t)
	sessions := make([]session.Session, 0, 4)
	for i := range 4 {
		sessions = append(sessions, newSession(fmt.Sprintf("m-%d", i), testNow.Add(-time.Hour)))
	}
	f.write(store.Batch{Sessions: sessions})
	ctx := context.Background()

	tests := []struct {
		name        string
		limit       int
		wantRows    int
		wantHasMore bool
	}{
		{name: "더 있다", limit: 2, wantRows: 2, wantHasMore: true},
		{name: "딱 맞아떨어지면 더 없다", limit: 4, wantRows: 4, wantHasMore: false},
		{name: "남는다", limit: 10, wantRows: 4, wantHasMore: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			page, err := f.reader.Activity(ctx, ActivityQuery{Limit: tc.limit})
			if err != nil {
				t.Fatalf("Activity: %v", err)
			}
			if len(page.Rows) != tc.wantRows {
				t.Errorf("Rows = %d건, want %d", len(page.Rows), tc.wantRows)
			}
			if page.HasMore != tc.wantHasMore {
				t.Errorf("HasMore = %v, want %v", page.HasMore, tc.wantHasMore)
			}
			// 마지막 페이지여도 커서는 마지막 줄을 가리킨다. 0 으로 비우면 HasMore 를
			// 보지 않는 호출자가 그것을 "첫 페이지" 로 읽어 처음부터 다시 받는다.
			last := page.Rows[len(page.Rows)-1]
			if page.NextCursor != (ActivityCursor{StartedAt: last.StartedAt, ID: last.ID}) {
				t.Errorf("NextCursor = %+v, want 마지막 줄 (%d,%d)",
					page.NextCursor, last.StartedAt, last.ID)
			}
		})
	}

	// 마지막 커서를 한 번 더 따라가도 빈 페이지일 뿐 처음으로 되돌아가지 않는다.
	t.Run("마지막 커서 다음은 빈 페이지", func(t *testing.T) {
		page, err := f.reader.Activity(ctx, ActivityQuery{Limit: 10})
		if err != nil {
			t.Fatalf("Activity: %v", err)
		}
		next, err := f.reader.Activity(ctx, ActivityQuery{Limit: 10, Cursor: page.NextCursor})
		if err != nil {
			t.Fatalf("Activity(cursor): %v", err)
		}
		if len(next.Rows) != 0 || next.HasMore {
			t.Errorf("마지막 커서 다음 = %d건 (HasMore=%v), want 0건", len(next.Rows), next.HasMore)
		}
	})
}

// 커서는 필터와 함께 살아 있어야 한다. 페이지를 넘길 때 필터가 풀리면 두 번째 페이지에
// 첫 페이지에서 걸러졌던 세션이 섞여 들어온다.
func TestActivityPaginationKeepsFilters(t *testing.T) {
	f := newFixture(t)
	sessions := make([]session.Session, 0, 6)
	for i := range 6 {
		key := fmt.Sprintf("k-%d", i)
		sessions = append(sessions, newSession(key, testNow.Add(-time.Hour), func(s *session.Session) {
			if i%2 == 0 {
				codex(s)
			}
		}))
	}
	f.write(store.Batch{Sessions: sessions})

	rows, _ := drainActivity(t, f.reader, ActivityQuery{Vendors: []string{vendorCodex}, Limit: 1})
	if len(rows) != 3 {
		t.Fatalf("codex 세션 = %d건, want 3", len(rows))
	}
	for _, r := range rows {
		if r.Vendor != vendorCodex {
			t.Errorf("%s 의 벤더가 %q — 페이지를 넘기며 필터가 풀렸다", r.SessionKey, r.Vendor)
		}
	}
}

// ── 검색 ────────────────────────────────────────────────────────────────────

func TestActivitySearchCoversFourSources(t *testing.T) {
	f := newFixture(t)
	at := testNow.Add(-time.Hour)
	f.write(store.Batch{
		Sessions: []session.Session{
			newSession("s-title", testNow.Add(-4*time.Hour), title("Collector 전달 구현"), func(s *session.Session) {
				s.WorkspacePath = workspaceA
			}),
			newSession("s-ws", testNow.Add(-3*time.Hour), title("무관한 제목 갑"), func(s *session.Session) {
				s.WorkspacePath = workspaceB
			}),
			newSession("s-file", testNow.Add(-2*time.Hour), title("무관한 제목 을"), func(s *session.Session) {
				s.WorkspacePath = workspaceA
			}),
			newSession("s-content", at, title("무관한 제목 병"), func(s *session.Session) {
				s.WorkspacePath = workspaceA
			}),
		},
		Events: []store.EventRecord{
			toolRecord("s-file", "t-file", "call-file", testNow.Add(-2*time.Hour), 1, toolSpec{
				ToolName: "Edit",
				Target:   workspaceA + "/proxy_handler.go",
				File:     fileChange(workspaceA+"/proxy_handler.go", 3, 1),
			}),
			promptRecord("s-content", "t-content", at, 2, "인증 토큰 검증을 붙여줘"),
		},
	})

	tests := []struct {
		name       string
		text       string
		wantKey    string
		wantSource string
	}{
		{name: "제목", text: "Collector", wantKey: "s-title", wantSource: SourceTitle},
		{name: "작업 폴더 경로", text: "pulsemetry-backend", wantKey: "s-ws", wantSource: SourceWorkspace},
		{name: "파일 경로", text: "proxy_handler", wantKey: "s-file", wantSource: SourceFile},
		{name: "원문 (한글)", text: "인증", wantKey: "s-content", wantSource: SourceContent},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			page, err := f.reader.Activity(context.Background(), ActivityQuery{Text: tc.text})
			if err != nil {
				t.Fatalf("Activity(%q): %v", tc.text, err)
			}
			if got := activityKeys(page.Rows); !reflect.DeepEqual(got, []string{tc.wantKey}) {
				t.Fatalf("목록 = %v, want [%s]", got, tc.wantKey)
			}
			if !containsString(page.Rows[0].MatchedSources, tc.wantSource) {
				t.Errorf("MatchedSources = %v, want %q 포함", page.Rows[0].MatchedSources, tc.wantSource)
			}
		})
	}

	t.Run("검색어가 없으면 출처도 없다", func(t *testing.T) {
		page, err := f.reader.Activity(context.Background(), ActivityQuery{})
		if err != nil {
			t.Fatalf("Activity: %v", err)
		}
		for _, r := range page.Rows {
			if len(r.MatchedSources) != 0 {
				t.Errorf("%s 의 MatchedSources = %v, want 빈 슬라이스", r.SessionKey, r.MatchedSources)
			}
			if r.MatchedSources == nil {
				t.Errorf("%s 의 MatchedSources 가 nil — JSON 에서 null 이 된다", r.SessionKey)
			}
		}
	})
}

// 인수조건: 검색 특수문자.
//
// `%` 와 `_` 는 LIKE 의 와일드카드고 `\` 는 escape 문자다. 셋 중 하나라도 그대로 새면
// 사용자가 친 낱말이 "아무 글자" 로 읽혀 엉뚱한 세션이 걸린다 — 그리고 `%` 한 글자만 친
// 사용자는 **전부** 를 보게 된다.
func TestActivitySearchEscapesLikeWildcards(t *testing.T) {
	f := newFixture(t)
	f.write(store.Batch{Sessions: []session.Session{
		newSession("w-pct", testNow.Add(-6*time.Hour), title("완료율 100% 달성")),
		newSession("w-plain", testNow.Add(-5*time.Hour), title("완료율 100X 달성")),
		newSession("w-us", testNow.Add(-4*time.Hour), title("a_b 처리")),
		newSession("w-x", testNow.Add(-3*time.Hour), title("axb 처리")),
		newSession("w-bs", testNow.Add(-2*time.Hour), title(`경로 c:\tmp\build`)),
		newSession("w-nobs", testNow.Add(-time.Hour), title("경로 c:tmp build")),
	}})

	tests := []struct {
		name string
		text string
		want []string
	}{
		{name: "퍼센트는 리터럴", text: "100%", want: []string{"w-pct"}},
		{name: "퍼센트 한 글자가 전체를 부르지 않는다", text: "%", want: []string{"w-pct"}},
		{name: "밑줄은 리터럴", text: "a_b", want: []string{"w-us"}},
		{name: "밑줄 한 글자", text: "_", want: []string{"w-us"}},
		{name: "역슬래시는 리터럴", text: `c:\tmp`, want: []string{"w-bs"}},
		{name: "역슬래시 한 글자", text: `\`, want: []string{"w-bs"}},
		{name: "escape 문자와 와일드카드가 붙어도 리터럴", text: `\%`, want: []string{}},
		{name: "이스케이프가 매칭 의미를 바꾸지 않는다", text: "100", want: []string{"w-plain", "w-pct"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			page, err := f.reader.Activity(context.Background(), ActivityQuery{Text: tc.text})
			if err != nil {
				t.Fatalf("Activity(%q): %v", tc.text, err)
			}
			if got := activityKeys(page.Rows); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Activity(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

// 파일 경로 검색도 같은 escape 를 거친다. 경로에는 `_` 가 흔해서 여기서 새면 무관한
// 세션이 대량으로 딸려 온다.
func TestActivitySearchEscapesWildcardsInFilePath(t *testing.T) {
	f := newFixture(t)
	at := testNow.Add(-time.Hour)
	f.write(store.Batch{
		Sessions: []session.Session{
			newSession("fp-us", at, title("파일 세션 갑")),
			newSession("fp-x", at.Add(time.Second), title("파일 세션 을")),
		},
		Events: []store.EventRecord{
			toolRecord("fp-us", "t-us", "call-us", at, 1, toolSpec{
				ToolName: "Edit",
				Target:   workspaceA + "/under_score.go",
				File:     fileChange(workspaceA+"/under_score.go", 1, 0),
			}),
			toolRecord("fp-x", "t-x", "call-x", at, 2, toolSpec{
				ToolName: "Edit",
				Target:   workspaceA + "/underXscore.go",
				File:     fileChange(workspaceA+"/underXscore.go", 1, 0),
			}),
		},
	})

	page, err := f.reader.Activity(context.Background(), ActivityQuery{Text: "under_score"})
	if err != nil {
		t.Fatalf("Activity: %v", err)
	}
	if got := activityKeys(page.Rows); !reflect.DeepEqual(got, []string{"fp-us"}) {
		t.Fatalf("목록 = %v, want [fp-us] (경로의 _ 가 와일드카드로 새면 underXscore 도 걸린다)", got)
	}
}

// 인수조건: 콘텐츠 저장 비활성화 시에도 제목·경로 검색은 동작한다.
//
// --no-store-content 는 turns.prompt_text 를 통째로 버린다. 원문 출처 하나가 없다고 검색
// 자체가 죽으면 프라이버시 모드를 켠 사용자는 Activity 를 쓸 수 없다.
func TestActivitySearchWithoutContentStorage(t *testing.T) {
	f := newFixture(t, store.WithContentStorage(false))
	at := testNow.Add(-time.Hour)
	f.write(store.Batch{
		Sessions: []session.Session{
			newSession("nc-1", at, title("인증 프록시 구현"), func(s *session.Session) {
				s.WorkspacePath = workspaceA
			}),
		},
		Events: []store.EventRecord{
			promptRecord("nc-1", "t-nc", at, 1, "고유단어프롬프트를 남긴다"),
			toolRecord("nc-1", "t-nc", "call-nc", at.Add(time.Second), 2, toolSpec{
				ToolName: "Edit",
				Target:   workspaceA + "/proxy_handler.go",
				File:     fileChange(workspaceA+"/proxy_handler.go", 2, 0),
			}),
		},
	})

	tests := []struct {
		name       string
		text       string
		wantRows   int
		wantSource string
	}{
		{name: "제목", text: "인증", wantRows: 1, wantSource: SourceTitle},
		{name: "작업 폴더 경로", text: "telemetryctl", wantRows: 1, wantSource: SourceWorkspace},
		{name: "파일 경로", text: "proxy_handler", wantRows: 1, wantSource: SourceFile},
		{name: "원문은 저장되지 않아 걸리지 않는다", text: "고유단어프롬프트", wantRows: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			page, err := f.reader.Activity(context.Background(), ActivityQuery{Text: tc.text})
			if err != nil {
				t.Fatalf("Activity(%q): %v", tc.text, err)
			}
			if len(page.Rows) != tc.wantRows {
				t.Fatalf("Activity(%q) = %d건, want %d", tc.text, len(page.Rows), tc.wantRows)
			}
			if tc.wantSource == "" {
				return
			}
			if !containsString(page.Rows[0].MatchedSources, tc.wantSource) {
				t.Errorf("MatchedSources = %v, want %q 포함", page.Rows[0].MatchedSources, tc.wantSource)
			}
			if containsString(page.Rows[0].MatchedSources, SourceContent) {
				t.Errorf("MatchedSources 에 %q 가 있다 — 저장하지 않은 원문이 걸렸다", SourceContent)
			}
		})
	}
}

// 검색 입력은 값으로 바인딩되고 와일드카드만 escape 된다. 어떤 입력도 질의를 깨거나
// 데이터를 건드리지 못한다.
func TestActivitySurvivesHostileInput(t *testing.T) {
	f := newFixture(t)
	seedActivity(f)
	ctx := context.Background()

	inputs := []string{
		`"`, `""`, `*`, `AND`, `OR`, `NOT`, `%_\`, `\\`, `[]`, `{}`, `:`,
		`'; DROP TABLE sessions;--`, `1) OR (1=1`, "\x00", "\t\n",
		strings.Repeat("가", 500),
		strings.Repeat("a b ", 200),
	}
	for _, in := range inputs {
		t.Run(shortName(in), func(t *testing.T) {
			if _, err := f.reader.Activity(ctx, ActivityQuery{Text: in}); err != nil {
				t.Fatalf("Activity(%q) 가 실패했다: %v", in, err)
			}
		})
	}

	page, err := f.reader.Activity(ctx, ActivityQuery{})
	if err != nil {
		t.Fatalf("Activity: %v", err)
	}
	if len(page.Rows) != 3 {
		t.Fatalf("세션 = %d건, want 3 — 검색 입력이 데이터를 건드렸다", len(page.Rows))
	}
}

// ── 집계 정확성 ─────────────────────────────────────────────────────────────

// 승격 테이블 셋을 한 질의에서 JOIN 하면 한 세션이 자식 행 수의 곱만큼 복제돼 SUM 이
// 그 배수로 부풀어 오른다. 검색 술어를 EXISTS 안에 가둔 이유가 이것이다.
//
// 같은 세션을 Session() 으로도 읽어 두 경로의 수치가 같은지 본다 — 화면 두 곳이 다른
// 비용을 보여 주면 어느 쪽을 믿어야 하는지 아무도 모른다.
func TestActivityDoesNotInflateAggregates(t *testing.T) {
	f := newFixture(t)
	at := testNow.Add(-time.Hour)
	f.write(store.Batch{
		Sessions: []session.Session{newSession("agg", at)},
		Events: []store.EventRecord{
			llmRecord("agg", "t-agg", at, 1, llmSpec{Cost: 1, Input: 10, Output: 2}),
			llmRecord("agg", "t-agg", at.Add(time.Second), 2, llmSpec{Cost: 1, Input: 10, Output: 2}),
			toolRecord("agg", "t-agg", "c-1", at.Add(2*time.Second), 3, toolSpec{
				ToolName: "Edit",
				Target:   workspaceA + "/proxy_handler.go",
				File:     fileChange(workspaceA+"/proxy_handler.go", 5, 1),
			}),
			toolRecord("agg", "t-agg", "c-2", at.Add(3*time.Second), 4, toolSpec{
				ToolName: "Edit",
				Target:   workspaceA + "/proxy_client.go",
				File:     fileChange(workspaceA+"/proxy_client.go", 5, 1),
			}),
		},
	})
	ctx := context.Background()

	// 검색어가 있을 때와 없을 때 모두 같은 수치여야 한다. 검색 술어가 행을 늘리면 여기서
	// 두 값이 갈린다.
	for _, text := range []string{"", "proxy_handler"} {
		name := "검색어 없음"
		if text != "" {
			name = "검색어 있음"
		}
		t.Run(name, func(t *testing.T) {
			page, err := f.reader.Activity(ctx, ActivityQuery{Text: text})
			if err != nil {
				t.Fatalf("Activity: %v", err)
			}
			if len(page.Rows) != 1 {
				t.Fatalf("목록 = %d건, want 1", len(page.Rows))
			}
			row := page.Rows[0]
			switch {
			case row.CostUSD != 2:
				t.Errorf("CostUSD = %v, want 2 (2회 × 1)", row.CostUSD)
			case row.InputTokens != 20:
				t.Errorf("InputTokens = %d, want 20", row.InputTokens)
			case row.APIRequests != 2:
				t.Errorf("APIRequests = %d, want 2", row.APIRequests)
			case row.ToolCalls != 2:
				t.Errorf("ToolCalls = %d, want 2", row.ToolCalls)
			case row.LinesAdded != 10 || row.LinesRemoved != 2:
				t.Errorf("변경량 = +%d/-%d, want +10/-2", row.LinesAdded, row.LinesRemoved)
			}

			detail, err := f.reader.Session(ctx, row.ID)
			if err != nil {
				t.Fatalf("Session: %v", err)
			}
			if !reflect.DeepEqual(detail.Session, row.SessionRow) {
				t.Errorf("Activity 와 Session 의 수치가 다르다:\n목록 = %+v\n상세 = %+v",
					row.SessionRow, detail.Session)
			}
		})
	}
}

// ── PROJ-92 이음매 ──────────────────────────────────────────────────────────

// 작업 유형은 별도 턴 분류 작업(PROJ-92)의 결과다. 그 전까지 이 열은 **빈 문자열**이어야
// 한다 — 그럴듯한 기본값을 채워 두면 화면은 그것을 분류 결과로 표시하고, 사용자는 없는
// 근거를 믿게 된다.
func TestActivityWorkTypeAwaitsTurnClassification(t *testing.T) {
	f := newFixture(t)
	seedActivity(f)

	page, err := f.reader.Activity(context.Background(), ActivityQuery{})
	if err != nil {
		t.Fatalf("Activity: %v", err)
	}
	if len(page.Rows) == 0 {
		t.Fatal("사전 조건 실패: 목록이 비었다")
	}
	for _, r := range page.Rows {
		if r.WorkType != "" {
			t.Errorf("%s 의 WorkType = %q — PROJ-92 전에는 채우지 않는다", r.SessionKey, r.WorkType)
		}
	}
}

// ── 계약 ────────────────────────────────────────────────────────────────────

func TestActivityTypesUseSnakeCaseTags(t *testing.T) {
	for _, v := range []any{ActivityQuery{}, ActivityRow{}, ActivityPage{}, ActivityCursor{}} {
		assertSnakeCaseTags(t, v)
	}
}

// Wails 서비스와 CLI 가 같은 결과를 봐야 한다 (ADR 0004).
func TestActivityServiceMatchesReader(t *testing.T) {
	f := newFixture(t)
	seedActivity(f)

	svc := NewService(f.path)
	if err := svc.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { svc.Stop() }) //nolint:errcheck // 테스트 정리
	svc.Reader().now = func() time.Time { return testNow }

	ctx := context.Background()
	q := ActivityQuery{Text: "프록시", Limit: 2}
	want, err := f.reader.Activity(ctx, q)
	if err != nil {
		t.Fatalf("Reader.Activity: %v", err)
	}
	got, err := svc.Activity(ctx, q)
	if err != nil {
		t.Fatalf("Service.Activity: %v", err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("결과가 다르다:\nReader  = %+v\nService = %+v", want, got)
	}
}
