package session

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// 제목 휴리스틱 3단계 폴백.
func TestTitleThreeStageFallback(t *testing.T) {
	const start = 1_700_000_000

	tests := []struct {
		name       string
		events     []Input
		wantTitle  string
		wantSource TitleSource
	}{
		{
			name: "1단계 — 첫 프롬프트의 첫 문장",
			events: []Input{
				logEv("s1", "claude_code.user_prompt", start,
					prompt("인증 토큰 검증 프록시를 구현해줘. 재시도는 3번까지. 로그는 남기지 마.")),
				logEv("s1", "claude_code.tool_result", start+1, tool("Edit"), target("/repo/a.go"), success(true)),
			},
			wantTitle:  "인증 토큰 검증 프록시를 구현해줘.",
			wantSource: TitleFromPrompt,
		},
		{
			name: "2단계 — 원문이 없으면 가장 많이 수정된 파일",
			events: []Input{
				logEv("s1", "claude_code.tool_result", start, tool("Edit"), target("/repo/receiver.go"), success(true)),
				logEv("s1", "claude_code.tool_result", start+1, tool("Edit"), target("/repo/receiver.go"), success(true)),
				logEv("s1", "claude_code.tool_result", start+2, tool("Write"), target("/repo/forward.go"), success(true)),
				metricEv("s1", "claude_code.lines_of_code.count", start+60, 12, typ("added")),
			},
			wantTitle:  "receiver.go 외 1개 수정",
			wantSource: TitleFromFiles,
		},
		{
			name: "2단계 — 파일이 하나면 외 0개를 쓰지 않는다",
			events: []Input{
				logEv("s1", "claude_code.tool_result", start, tool("Edit"), target("/repo/receiver.go"), success(true)),
			},
			wantTitle:  "receiver.go 수정",
			wantSource: TitleFromFiles,
		},
		{
			name: "3단계 — 둘 다 없으면 벤더",
			events: []Input{
				logEv("s1", "claude_code.api_request", start),
			},
			wantTitle:  "claude_code 세션",
			wantSource: TitleFromFallback,
		},
		{
			name: "빈 프롬프트는 다음 단계로 내려간다",
			events: []Input{
				logEv("s1", "claude_code.user_prompt", start, prompt("   \n  ")),
			},
			wantTitle:  "claude_code 세션",
			wantSource: TitleFromFallback,
		},
		{
			name: "툴 상세가 꺼져 파일 경로가 없으면 fallback",
			events: []Input{
				logEv("s1", "claude_code.tool_result", start, tool("Edit"), success(true)),
			},
			wantTitle:  "claude_code 세션",
			wantSource: TitleFromFallback,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := New()
			for _, e := range tt.events {
				a.Add(e)
			}
			s, _ := a.Session("s1")
			if s.Title != tt.wantTitle {
				t.Errorf("title = %q, want %q", s.Title, tt.wantTitle)
			}
			if s.TitleSource != tt.wantSource {
				t.Errorf("title_source = %q, want %q", s.TitleSource, tt.wantSource)
			}
		})
	}
}

// 첫 프롬프트가 제목을 정하고 이후 프롬프트가 덮어쓰지 않는다.
func TestFirstPromptWins(t *testing.T) {
	const start = 1_700_000_000
	a := New()
	a.Add(logEv("s1", "claude_code.user_prompt", start, prompt("첫 번째 요청")))
	a.Add(logEv("s1", "claude_code.user_prompt", start+10, prompt("두 번째 요청")))

	s, _ := a.Session("s1")
	if s.Title != "첫 번째 요청" {
		t.Fatalf("title = %q", s.Title)
	}
	if s.Prompts != 2 {
		t.Fatalf("prompts = %d, want 2", s.Prompts)
	}
}

func TestSummaryUsesSecondAndThirdSentences(t *testing.T) {
	const start = 1_700_000_000
	a := New()
	a.Add(logEv("s1", "claude_code.user_prompt", start,
		prompt("프록시를 구현해줘. 재시도는 3번. 로그는 남기지 마. 네 번째 문장은 빼라.")))

	s, _ := a.Session("s1")
	if s.Title != "프록시를 구현해줘." {
		t.Errorf("title = %q", s.Title)
	}
	if s.Summary != "재시도는 3번. 로그는 남기지 마." {
		t.Errorf("summary = %q", s.Summary)
	}
}

func TestSummaryEmptyForSingleSentence(t *testing.T) {
	const start = 1_700_000_000
	a := New()
	a.Add(logEv("s1", "claude_code.user_prompt", start, prompt("한 문장뿐인 요청")))

	if s, _ := a.Session("s1"); s.Summary != "" {
		t.Fatalf("summary = %q, want 빈 값 (제목과 같은 문장이 두 번 보이면 안 된다)", s.Summary)
	}
}

// 캡은 룬 기준이다. 바이트로 자르면 한글이 중간에서 끊겨 U+FFFD 가 화면에 뜬다.
func TestTitleCapCountsRunesNotBytes(t *testing.T) {
	const start = 1_700_000_000
	long := strings.Repeat("한글", 100) // 200 룬, 600 바이트

	a := New()
	a.Add(logEv("s1", "claude_code.user_prompt", start, prompt(long+"\n"+long)))

	s, _ := a.Session("s1")
	if n := utf8.RuneCountInString(s.Title); n != TitleRuneCap {
		t.Fatalf("title 룬 수 = %d, want %d", n, TitleRuneCap)
	}
	if !strings.HasSuffix(s.Title, "…") {
		t.Errorf("잘렸는데 말줄임 표식이 없음: %q", s.Title)
	}
	if strings.ContainsRune(s.Title, '�') {
		t.Errorf("룬이 중간에서 끊김: %q", s.Title)
	}
	if n := utf8.RuneCountInString(s.Summary); n != SummaryRuneCap {
		t.Fatalf("summary 룬 수 = %d, want %d", n, SummaryRuneCap)
	}
}

func TestCapRunes(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		limit int
		want  string
	}{
		{"캡 이하는 그대로", "짧다", 10, "짧다"},
		{"정확히 캡", "12345", 5, "12345"},
		{"캡 초과는 말줄임", "123456", 5, "1234…"},
		{"말줄임 앞 공백 제거", "가나다 라마", 5, "가나다…"},
		{"캡 1", "가나다", 1, "…"},
		{"캡 0", "가나다", 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := capRunes(tt.in, tt.limit); got != tt.want {
				t.Fatalf("capRunes(%q, %d) = %q, want %q", tt.in, tt.limit, got, tt.want)
			}
		})
	}
}

// 문장 분리. 한국어 프롬프트에는 파일명과 소수점이 그대로 들어와서 '.' 만 보고 끊으면
// 제목이 "internal/session/event." 가 된다.
func TestSplitSentences(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"기본", "첫 문장. 둘째 문장.", []string{"첫 문장.", "둘째 문장."}},
		{"종결 부호 없음", "끝에 부호가 없다", []string{"끝에 부호가 없다"}},
		{"파일명의 점은 경계가 아니다", "internal/session/event.go 를 고쳐줘.", []string{"internal/session/event.go 를 고쳐줘."}},
		{"소수점은 경계가 아니다", "타임아웃을 3.5초로 바꿔줘.", []string{"타임아웃을 3.5초로 바꿔줘."}},
		{"개행이 경계", "다음을 구현해줘:\n- A\n- B", []string{"다음을 구현해줘:", "- A", "- B"}},
		{"CRLF", "첫 줄\r\n둘째 줄", []string{"첫 줄", "둘째 줄"}},
		{"연속 부호는 한 번만 끊는다", "정말?! 그래.", []string{"정말?!", "그래."}},
		{"말줄임은 통째로 남는다", "음... 그래서?", []string{"음...", "그래서?"}},
		{"전각 부호", "그렇다。다음。", []string{"그렇다。", "다음。"}},
		{"연속 공백은 하나로", "여러   공백이   있다", []string{"여러 공백이 있다"}},
		{"탭과 앞뒤 공백", "  \t앞뒤 공백\t ", []string{"앞뒤 공백"}},
		{"빈 문자열", "", nil},
		{"공백뿐", "  \n\n  ", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitSentences(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("splitSentences(%q) = %q, want %q", tt.in, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("splitSentences(%q)[%d] = %q, want %q", tt.in, i, got[i], tt.want[i])
				}
			}
		})
	}
}

// 제목에는 개행이 남지 않아야 한다 — 목록 화면의 한 줄이 깨진다.
func TestTitleHasNoNewline(t *testing.T) {
	const start = 1_700_000_000
	a := New()
	a.Add(logEv("s1", "claude_code.user_prompt", start, prompt("제목 줄\n본문 줄")))

	s, _ := a.Session("s1")
	if strings.ContainsAny(s.Title, "\n\r") {
		t.Fatalf("제목에 개행이 남음: %q", s.Title)
	}
	if s.Title != "제목 줄" {
		t.Fatalf("title = %q", s.Title)
	}
}

func TestFallbackTitleWithoutVendor(t *testing.T) {
	if got := fallbackTitle(""); got != "세션" {
		t.Fatalf("fallbackTitle(\"\") = %q", got)
	}
}
