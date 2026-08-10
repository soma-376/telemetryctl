package dashboard

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/event"
	"github.com/your-org/pulsemetry/internal/session"
	"github.com/your-org/pulsemetry/internal/store"
)

// seedSearch 는 세 출처가 각각 다른 세션에서만 걸리도록 데이터를 놓는다.
// 하나라도 빠뜨린 구현은 그 세션을 못 찾는다.
func seedSearch(f *fixture) {
	sec := event.SecFromTime(testNow.Add(-time.Hour))
	sessions := []session.Session{
		// 제목에만 있다
		newSession("s-title", testNow.Add(-3*time.Hour), func(s *session.Session) {
			s.Title = "Collector 전달 프록시 구현"
		}),
		// 파일명에만 있다
		newSession("s-file", testNow.Add(-2*time.Hour), func(s *session.Session) {
			s.Title = "관련 없는 제목"
			s.Files = []session.File{
				{PathHash: "h1", Name: "proxy_handler.go", Ext: "go", Edits: 1, LastTS: sec},
			}
		}),
		// 원문에만 있다
		newSession("s-content", testNow.Add(-time.Hour), func(s *session.Session) {
			s.Title = "다른 작업"
		}),
	}
	f.write(store.Batch{
		Sessions: sessions,
		Events: []store.EventRecord{
			prompt("s-content", testNow.Add(-time.Hour), 1, "인증 토큰 검증 및 프록시 경유 전달을 구현해줘"),
			prompt("s-title", testNow.Add(-3*time.Hour), 2, "전혀 무관한 본문"),
		},
	})
}

func TestSearchCoversThreeSources(t *testing.T) {
	f := newFixture(t)
	seedSearch(f)
	ctx := context.Background()

	tests := []struct {
		name        string
		text        string
		wantSession string
		wantSource  string
	}{
		{name: "제목", text: "Collector", wantSession: "s-title", wantSource: SourceTitle},
		{name: "파일명", text: "proxy_handler", wantSession: "s-file", wantSource: SourceFile},
		{name: "원문 (한글)", text: "인증", wantSession: "s-content", wantSource: SourceContent},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hits, err := f.reader.Search(ctx, SearchQuery{Text: tc.text})
			if err != nil {
				t.Fatalf("Search(%q): %v", tc.text, err)
			}
			var found *Hit
			for i := range hits {
				if hits[i].SessionID == tc.wantSession {
					found = &hits[i]
				}
			}
			if found == nil {
				t.Fatalf("Search(%q) 가 %s 를 못 찾았다 (결과 %d건)", tc.text, tc.wantSession, len(hits))
			}
			if !containsString(found.Sources, tc.wantSource) {
				t.Errorf("Sources = %v, want %q 포함", found.Sources, tc.wantSource)
			}
			if found.Title == "" {
				t.Error("Title 이 비었다 — 세션 메타데이터가 붙지 않았다")
			}
		})
	}
}

// 한 세션이 여러 출처에 걸려도 결과는 한 줄이다. 세 줄로 나오면 결과 수가 부풀고
// 클릭 대상도 같아 사용자에게 아무 정보가 없다.
func TestSearchMergesSourcesPerSession(t *testing.T) {
	f := newFixture(t)
	sec := event.SecFromTime(testNow.Add(-time.Hour))
	f.write(store.Batch{
		Sessions: []session.Session{
			newSession("s-all", testNow.Add(-time.Hour), func(s *session.Session) {
				s.Title = "프록시 구현"
				s.Files = []session.File{{PathHash: "h", Name: "프록시.go", Edits: 1, LastTS: sec}}
			}),
		},
		Events: []store.EventRecord{
			prompt("s-all", testNow.Add(-time.Hour), 1, "프록시 코드를 고쳐줘"),
		},
	})

	hits, err := f.reader.Search(context.Background(), SearchQuery{Text: "프록시"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("결과 = %d건, want 1 (세션 단위 병합)", len(hits))
	}
	if len(hits[0].Sources) != 3 {
		t.Errorf("Sources = %v, want 3종", hits[0].Sources)
	}
	if hits[0].Snippet == "" {
		t.Error("Snippet 이 비었다 — 원문 발췌가 붙어야 한다")
	}
	if len(hits[0].MatchedFiles) != 1 || hits[0].MatchedFiles[0] != "프록시.go" {
		t.Errorf("MatchedFiles = %v", hits[0].MatchedFiles)
	}
}

// FTS5 의 MATCH 오른쪽은 질의 언어다. 사용자 입력을 그대로 넣으면 문법 오류나 연산자 해석이
// 일어나고, 그 에러가 Promise reject 로 화면에 그대로 뜬다.
func TestSearchSurvivesFTSMetacharacters(t *testing.T) {
	f := newFixture(t)
	seedSearch(f)
	ctx := context.Background()

	inputs := []string{
		`"`,
		`""`,
		`*`,
		`AND`,
		`OR`,
		`NOT`,
		`NEAR(`,
		`NEAR("a" b)`,
		`인증 AND 토큰`,
		`토큰 OR`,
		`^prefix`,
		`(unbalanced`,
		`a-b`,
		`"인증 토큰"`,
		`{}`,
		`:`,
		`%_\`,
		`인증*`,
		`'; DROP TABLE sessions;--`,
		strings.Repeat("가", 500),
		strings.Repeat("a b ", 200),
	}
	for _, in := range inputs {
		t.Run(shortName(in), func(t *testing.T) {
			if _, err := f.reader.Search(ctx, SearchQuery{Text: in}); err != nil {
				t.Fatalf("Search(%q) 가 실패했다: %v", in, err)
			}
		})
	}

	// 데이터가 그대로 남아 있는지 — SQL 주입 입력이 아무것도 지우지 않았어야 한다.
	rows, err := f.reader.Sessions(ctx, SessionQuery{})
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("세션 = %d건, want 3 — 검색 입력이 데이터를 건드렸다", len(rows))
	}
}

// 연산자처럼 보이는 낱말은 낱말로 취급돼야 한다.
func TestSearchTreatsOperatorsAsWords(t *testing.T) {
	f := newFixture(t)
	f.write(store.Batch{
		Sessions: []session.Session{newSession("s-op", testNow.Add(-time.Hour))},
		Events: []store.EventRecord{
			prompt("s-op", testNow.Add(-time.Hour), 1, "AND 게이트 회로를 설명해줘"),
			prompt("s-op", testNow.Add(-time.Hour), 2, "관련 없는 본문"),
		},
	})

	hits, err := f.reader.Search(context.Background(), SearchQuery{Text: "AND 게이트"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("결과 = %d건, want 1", len(hits))
	}
}

// 접두 검색 — 검색창은 타이핑 중에도 결과를 보여야 한다.
func TestSearchMatchesPrefix(t *testing.T) {
	f := newFixture(t)
	f.write(store.Batch{
		Sessions: []session.Session{newSession("s-prefix", testNow.Add(-time.Hour))},
		Events: []store.EventRecord{
			prompt("s-prefix", testNow.Add(-time.Hour), 1, "Collector 전달 프록시 구현"),
		},
	})

	hits, err := f.reader.Search(context.Background(), SearchQuery{Text: "프록"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || !containsString(hits[0].Sources, SourceContent) {
		t.Fatalf("접두 검색 결과 = %+v, want s-prefix 의 원문 히트", hits)
	}
}

// LIKE 의 와일드카드가 이스케이프되지 않으면 `_` 가 "아무 글자 하나" 로 읽혀 엉뚱한 세션이 걸린다.
func TestSearchEscapesLikeWildcards(t *testing.T) {
	f := newFixture(t)
	f.write(store.Batch{Sessions: []session.Session{
		newSession("s-a", testNow.Add(-2*time.Hour), func(s *session.Session) { s.Title = "a_b 처리" }),
		newSession("s-b", testNow.Add(-time.Hour), func(s *session.Session) { s.Title = "axb 처리" }),
	}})

	hits, err := f.reader.Search(context.Background(), SearchQuery{Text: "a_b"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || hits[0].SessionID != "s-a" {
		t.Fatalf("결과 = %+v, want s-a 만 (a_b 의 _ 가 와일드카드로 새면 axb 도 걸린다)", hits)
	}
}

func TestSearchEmptyQueryIsEmptyResult(t *testing.T) {
	f := newFixture(t)
	seedSearch(f)

	for _, text := range []string{"", "   ", "\t\n"} {
		hits, err := f.reader.Search(context.Background(), SearchQuery{Text: text})
		if err != nil {
			t.Fatalf("Search(%q): %v", text, err)
		}
		if len(hits) != 0 {
			t.Errorf("Search(%q) = %d건, want 0", text, len(hits))
		}
		if hits == nil {
			t.Errorf("Search(%q) 가 nil 을 돌려줬다 — JSON 에서 null 이 된다", text)
		}
	}
}

func TestSearchLimitAndTimeFilter(t *testing.T) {
	f := newFixture(t)
	seedSearch(f)
	ctx := context.Background()

	all, err := f.reader.Search(ctx, SearchQuery{Text: "프록"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(all) < 2 {
		t.Fatalf("사전 조건 실패: 결과 = %d건", len(all))
	}
	// 최근 세션이 먼저다.
	for i := 1; i < len(all); i++ {
		if all[i-1].StartedAt < all[i].StartedAt {
			t.Fatalf("정렬이 내림차순이 아니다: %d < %d", all[i-1].StartedAt, all[i].StartedAt)
		}
	}

	limited, err := f.reader.Search(ctx, SearchQuery{Text: "프록", Limit: 1})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("Limit=1 결과 = %d건", len(limited))
	}

	// Since 가 모든 세션보다 미래면 결과가 없어야 한다.
	future, err := f.reader.Search(ctx, SearchQuery{Text: "프록", Since: testNow.Add(time.Hour).Unix()})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(future) != 0 {
		t.Fatalf("미래 Since 결과 = %d건, want 0", len(future))
	}
}

func TestFTSQuerySanitization(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "단일 낱말은 접두 질의", in: "토큰", want: `"토큰"*`},
		{name: "여러 낱말은 암묵적 AND", in: "인증 토큰", want: `"인증" "토큰"*`},
		{name: "연산자는 낱말로 인용", in: "a AND b", want: `"a" "AND" "b"*`},
		{name: "따옴표는 사라진다", in: `"인증 토큰"`, want: `"인증" "토큰"*`},
		{name: "NEAR 문법은 낱말로 분해", in: `NEAR("a" b)`, want: `"NEAR" "a" "b"*`},
		{name: "별표는 구분자", in: "인증*", want: `"인증"*`},
		{name: "밑줄은 구분자 (unicode61 과 같다)", in: "a_b", want: `"a" "b"*`},
		{name: "문장부호만이면 빈 질의", in: `*"()^:`, want: ""},
		{name: "빈 입력", in: "", want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ftsQuery(tc.in); got != tc.want {
				t.Fatalf("ftsQuery(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestFTSQueryBoundsInput(t *testing.T) {
	long := ftsQuery(strings.Repeat("가나다 ", 100))
	if strings.Count(long, `"`) > 2*maxFTSTokens {
		t.Fatalf("토큰 수가 상한 %d 를 넘었다: %q", maxFTSTokens, long)
	}
	huge := ftsQuery(strings.Repeat("가", 500))
	if len([]rune(huge)) > maxFTSTokenRunes+3 {
		t.Fatalf("토큰 길이가 상한 %d 를 넘었다 (%d)", maxFTSTokenRunes, len([]rune(huge)))
	}
}

func TestLikePatternEscapes(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"abc", "%abc%"},
		{"a_b", `%a\_b%`},
		{"50%", `%50\%%`},
		{`c:\tmp`, `%c:\\tmp%`},
	}
	for _, tc := range tests {
		if got := likePattern(tc.in); got != tc.want {
			t.Errorf("likePattern(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// shortName 은 t.Run 의 부분 테스트 이름을 짧게 만든다.
func shortName(s string) string {
	s = strings.ReplaceAll(s, " ", "_")
	if len(s) > 24 {
		return s[:24]
	}
	if s == "" {
		return "빈입력"
	}
	return s
}
