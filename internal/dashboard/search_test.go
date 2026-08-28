package dashboard

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/session"
	"github.com/your-org/pulsemetry/internal/store"
)

// seedSearch 는 세 출처가 각각 다른 세션에서만 걸리도록 데이터를 놓는다.
// 하나라도 빠뜨린 구현은 그 세션을 못 찾는다.
//
// v3 의 출처는 sessions.title · file_changes.file_path · turns.prompt_text 다 (ADR 0009).
func seedSearch(f *fixture) {
	at := testNow.Add(-time.Hour)
	f.write(store.Batch{
		Sessions: []session.Session{
			// 제목에만 있다
			newSession("s-title", testNow.Add(-3*time.Hour), func(s *session.Session) {
				s.Title = "Collector 전달 프록시 구현"
			}),
			// 파일 경로에만 있다
			newSession("s-file", testNow.Add(-2*time.Hour), func(s *session.Session) {
				s.Title = "관련 없는 제목"
			}),
			// 원문에만 있다
			newSession("s-content", at, func(s *session.Session) {
				s.Title = "다른 작업"
			}),
		},
		Events: []store.EventRecord{
			toolRecord("s-file", "t-file", "call-file", testNow.Add(-2*time.Hour), 1, toolSpec{
				ToolName: "Edit",
				Target:   workspaceA + "/proxy_handler.go",
				File:     fileChange(workspaceA+"/proxy_handler.go", 3, 1),
			}),
			promptRecord("s-content", "t-content", at, 2, "인증 토큰 검증 및 프록시 경유 전달을 구현해줘"),
			promptRecord("s-title", "t-title", testNow.Add(-3*time.Hour), 3, "전혀 무관한 본문"),
		},
	})
}

func TestSearchCoversThreeSources(t *testing.T) {
	f := newFixture(t)
	seedSearch(f)
	ctx := context.Background()

	tests := []struct {
		name       string
		text       string
		wantKey    string
		wantSource string
	}{
		{name: "제목", text: "Collector", wantKey: "s-title", wantSource: SourceTitle},
		{name: "파일 경로", text: "proxy_handler", wantKey: "s-file", wantSource: SourceFile},
		{name: "원문 (한글)", text: "인증", wantKey: "s-content", wantSource: SourceContent},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hits, err := f.reader.Search(ctx, SearchQuery{Text: tc.text})
			if err != nil {
				t.Fatalf("Search(%q): %v", tc.text, err)
			}
			var found *Hit
			for i := range hits {
				if hits[i].SessionKey == tc.wantKey {
					found = &hits[i]
				}
			}
			if found == nil {
				t.Fatalf("Search(%q) 가 %s 를 못 찾았다 (결과 %d건)", tc.text, tc.wantKey, len(hits))
			}
			if !containsString(found.Sources, tc.wantSource) {
				t.Errorf("Sources = %v, want %q 포함", found.Sources, tc.wantSource)
			}
			if found.Title == "" {
				t.Error("Title 이 비었다 — 세션 메타데이터가 붙지 않았다")
			}
			if found.ID <= 0 {
				t.Error("ID 가 비었다 — Session() 에 그대로 넘길 수 있어야 한다")
			}
		})
	}
}

// 한 세션이 여러 출처에 걸려도 결과는 한 줄이다. 세 줄로 나오면 결과 수가 부풀고
// 클릭 대상도 같아 사용자에게 아무 정보가 없다.
func TestSearchMergesSourcesPerSession(t *testing.T) {
	f := newFixture(t)
	at := testNow.Add(-time.Hour)
	f.write(store.Batch{
		Sessions: []session.Session{
			newSession("s-all", at, func(s *session.Session) { s.Title = "프록시 구현" }),
		},
		Events: []store.EventRecord{
			promptRecord("s-all", "t-all", at, 1, "프록시 코드를 고쳐줘"),
			toolRecord("s-all", "t-all", "call-all", at.Add(time.Second), 2, toolSpec{
				ToolName: "Edit",
				Target:   workspaceA + "/프록시.go",
				File:     fileChange(workspaceA+"/프록시.go", 2, 0),
			}),
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

// 작업 폴더 경로도 검색 출처다 (PROJ-90). 제목·원문 어디에도 없는 낱말이라도 그 세션이
// 어느 폴더에서 돌았는지로 되찾을 수 있어야 한다.
func TestSearchCoversWorkspacePath(t *testing.T) {
	f := newFixture(t)
	f.write(store.Batch{Sessions: []session.Session{
		newSession("s-ws-a", testNow.Add(-2*time.Hour), func(s *session.Session) {
			s.Title = "제목에는 없는 낱말"
			s.WorkspacePath = workspaceB
		}),
		newSession("s-ws-b", testNow.Add(-time.Hour), func(s *session.Session) {
			s.Title = "제목에는 없는 낱말"
			s.WorkspacePath = workspaceA
		}),
	}})

	hits, err := f.reader.Search(context.Background(), SearchQuery{Text: "pulsemetry-backend"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || hits[0].SessionKey != "s-ws-a" {
		t.Fatalf("결과 = %+v, want s-ws-a 하나", hits)
	}
	if !containsString(hits[0].Sources, SourceWorkspace) {
		t.Errorf("Sources = %v, want %q 포함", hits[0].Sources, SourceWorkspace)
	}
}

// LIKE 의 오른쪽은 질의 언어가 아니라 그냥 문자열이라 FTS5 시절의 문법 오류는 사라졌다.
// 그래도 와일드카드와 SQL 주입 입력은 여전히 값 바인딩과 escape 로 막아야 한다.
func TestSearchSurvivesHostileInput(t *testing.T) {
	f := newFixture(t)
	seedSearch(f)
	ctx := context.Background()

	inputs := []string{
		`"`, `""`, `*`, `AND`, `OR`, `NOT`, `NEAR(`, `NEAR("a" b)`,
		`인증 AND 토큰`, `토큰 OR`, `^prefix`, `(unbalanced`, `a-b`,
		`"인증 토큰"`, `{}`, `:`, `%_\`, `인증*`,
		`'; DROP TABLE sessions;--`,
		`%`, `_`, `\`,
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

// 연산자처럼 보이는 낱말은 낱말로 취급돼야 한다. LIKE 는 애초에 연산자를 해석하지 않으므로
// 부분 문자열이 그대로 맞아야 한다.
func TestSearchTreatsOperatorsAsWords(t *testing.T) {
	f := newFixture(t)
	at := testNow.Add(-time.Hour)
	f.write(store.Batch{
		Sessions: []session.Session{newSession("s-op", at)},
		Events: []store.EventRecord{
			promptRecord("s-op", "t-op-1", at, 1, "AND 게이트 회로를 설명해줘"),
			promptRecord("s-op", "t-op-2", at.Add(time.Second), 2, "관련 없는 본문"),
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

// 부분 일치 — 검색창은 타이핑 중에도 결과를 보여야 한다.
// FTS5 의 접두 검색(`*`)이 사라진 자리를 LIKE '%...%' 가 대신한다.
func TestSearchMatchesSubstring(t *testing.T) {
	f := newFixture(t)
	at := testNow.Add(-time.Hour)
	f.write(store.Batch{
		Sessions: []session.Session{newSession("s-prefix", at)},
		Events: []store.EventRecord{
			promptRecord("s-prefix", "t-prefix", at, 1, "Collector 전달 프록시 구현"),
		},
	})

	hits, err := f.reader.Search(context.Background(), SearchQuery{Text: "프록"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || !containsString(hits[0].Sources, SourceContent) {
		t.Fatalf("부분 일치 결과 = %+v, want s-prefix 의 원문 히트", hits)
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
	if len(hits) != 1 || hits[0].SessionKey != "s-a" {
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

// snippet() 이 v3 와 함께 사라져서 Go 가 발췌를 만든다. 룬 경계를 지키지 못하면 한글
// 한 글자 중간에서 끊겨 화면에 U+FFFD 가 뜬다.
func TestSnippetOf(t *testing.T) {
	long := strings.Repeat("가", 100) + "인증토큰" + strings.Repeat("나", 100)

	tests := []struct {
		name       string
		body       string
		needle     string
		want       string
		wantPrefix string
		wantSuffix string
	}{
		{
			name: "짧은 본문은 통째로", body: "인증 토큰 검증", needle: "토큰",
			want: "인증 토큰 검증",
		},
		{
			name: "대소문자 무시", body: "Collector 전달", needle: "collector",
			want: "Collector 전달",
		},
		{
			name: "빈 본문", body: "", needle: "토큰", want: "",
		},
		{
			name: "양쪽이 잘리면 말줄임표", body: long, needle: "인증토큰",
			wantPrefix: "…", wantSuffix: "…",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := snippetOf(tc.body, tc.needle)
			if tc.want != "" && got != tc.want {
				t.Fatalf("snippetOf = %q, want %q", got, tc.want)
			}
			if tc.wantPrefix != "" && !strings.HasPrefix(got, tc.wantPrefix) {
				t.Errorf("발췌가 %q 로 시작하지 않는다: %q", tc.wantPrefix, got)
			}
			if tc.wantSuffix != "" && !strings.HasSuffix(got, tc.wantSuffix) {
				t.Errorf("발췌가 %q 로 끝나지 않는다: %q", tc.wantSuffix, got)
			}
			if strings.ContainsRune(got, '\uFFFD') {
				t.Errorf("발췌에 깨진 룬이 있다: %q", got)
			}
			if tc.name == "양쪽이 잘리면 말줄임표" {
				if !strings.Contains(got, "인증토큰") {
					t.Errorf("발췌에 일치 지점이 없다: %q", got)
				}
				if n := len([]rune(got)); n > 2*snippetContextRunes+len([]rune(tc.needle))+2 {
					t.Errorf("발췌 길이 = %d룬, 상한을 넘었다", n)
				}
			}
		})
	}
}

func TestCapRunes(t *testing.T) {
	if got := capRunes("가나다라", 2); got != "가나" {
		t.Errorf("capRunes = %q, want 가나", got)
	}
	if got := capRunes("abc", 10); got != "abc" {
		t.Errorf("capRunes = %q, want abc", got)
	}
	if got := capRunes(strings.Repeat("가", 500), maxSearchRunes); len([]rune(got)) != maxSearchRunes {
		t.Errorf("capRunes 길이 = %d, want %d", len([]rune(got)), maxSearchRunes)
	}
}

// shortName 은 t.Run 의 부분 테스트 이름을 짧게 만든다.
func shortName(s string) string {
	s = strings.ReplaceAll(s, " ", "_")
	if r := []rune(s); len(r) > 12 {
		return string(r[:12])
	}
	if s == "" {
		return "빈입력"
	}
	return s
}
