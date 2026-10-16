package vendor

import "testing"

func TestNormalize(t *testing.T) {
	cases := []struct {
		raw  string
		want ID
		ok   bool
	}{
		{"claude-code", ClaudeCode, true},
		{"claude_code", ClaudeCode, true},
		{"Claude", ClaudeCode, true},
		{"codex", Codex, true},
		{"codex-cli", Codex, true},
		{"codex_cli_rs", Codex, true},
		// codex_exec 가 빠져 있어서 세션이 화면에서 "Others" 로 떨어졌다.
		{"codex_exec", Codex, true},
		{"codex-exec", Codex, true},
		// 데스크톱 앱이 CLI 와 다른 이름을 보낸다. 갈라지면 같은 대화가 두 세션이 된다.
		{"claude_code_desktop", ClaudeCode, true},
		{"claude-code-desktop", ClaudeCode, true},
		{" CODEX_EXEC ", Codex, true},
		{"gemini-cli", GeminiCLI, true},
		{"cursor", Cursor, true},
		{"opencode", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		got, ok := Normalize(tc.raw)
		if got != tc.want || ok != tc.ok {
			t.Errorf("Normalize(%q) = %q,%v want %q,%v", tc.raw, got, ok, tc.want, tc.ok)
		}
	}
}

// 별칭이 우리가 아는 벤더로만 떨어져야 한다. 오타가 새 벤더를 조용히 만들어 내면
// 화면에 카드가 하나 더 생기고 아무도 눈치채지 못한다.
func TestAliasesResolveToKnownVendors(t *testing.T) {
	known := map[ID]bool{}
	for _, id := range All() {
		known[id] = true
	}
	for alias, id := range aliases {
		if !known[id] {
			t.Errorf("별칭 %q 가 모르는 벤더 %q 로 간다", alias, id)
		}
	}
	// 정식 ID 자신도 별칭 표에 있어야 한다. 없으면 자기 이름으로 온 이벤트가 Fallback 을 탄다.
	for _, id := range All() {
		if got, ok := Normalize(string(id)); !ok || got != id {
			t.Errorf("정식 ID %q 가 별칭 표에 없다", id)
		}
	}
}

func TestFallbackNormalizesShape(t *testing.T) {
	if got := Fallback(" Open-Code "); got != "open_code" {
		t.Errorf("Fallback = %q, want open_code", got)
	}
}
