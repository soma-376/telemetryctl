package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTranscript(t *testing.T, root, project, key string, lines ...string) {
	t.Helper()
	dir := filepath.Join(root, project)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, key+".jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadClaudeAITitle(t *testing.T) {
	root := t.TempDir()
	writeTranscript(t, root, "C--repo", "s1",
		`{"type":"mode","mode":"default","sessionId":"s1"}`,
		`{"type":"user","cwd":"C:\repo","message":{"content":"원문"},"sessionId":"s1"}`,
		`{"type":"ai-title","aiTitle":"트레이 한도 조회 동작 확인","sessionId":"s1"}`,
	)

	got, ok := readClaudeAITitle(root, "s1")
	if !ok {
		t.Fatal("제목을 못 찾았다")
	}
	if got != "트레이 한도 조회 동작 확인" {
		t.Fatalf("제목 = %q", got)
	}
}

// 제목이 여러 번 찍히면 나중 것이 이긴다.
func TestReadClaudeAITitleTakesLast(t *testing.T) {
	root := t.TempDir()
	writeTranscript(t, root, "C--repo", "s1",
		`{"type":"ai-title","aiTitle":"처음","sessionId":"s1"}`,
		`{"type":"ai-title","aiTitle":"나중","sessionId":"s1"}`,
	)
	if got, _ := readClaudeAITitle(root, "s1"); got != "나중" {
		t.Fatalf("제목 = %q, want 나중", got)
	}
}

func TestReadClaudeAITitleMissing(t *testing.T) {
	root := t.TempDir()
	writeTranscript(t, root, "C--repo", "s1", `{"type":"mode","mode":"default","sessionId":"s1"}`)

	if _, ok := readClaudeAITitle(root, "s1"); ok {
		t.Error("제목 레코드가 없는데 찾았다고 한다")
	}
	if _, ok := readClaudeAITitle(root, "없는세션"); ok {
		t.Error("파일이 없는데 찾았다고 한다")
	}
}

// 세션 키는 벤더가 준 값이라 경로로 쓰이면 안 된다.
func TestReadClaudeAITitleRejectsUnsafeKey(t *testing.T) {
	root := t.TempDir()
	writeTranscript(t, root, "C--repo", "s1", `{"type":"ai-title","aiTitle":"제목","sessionId":"s1"}`)

	for _, key := range []string{"../s1", "C--repo/s1", `C--repo\s1`, "*", "s1.jsonl", ""} {
		if _, ok := readClaudeAITitle(root, key); ok {
			t.Errorf("안전하지 않은 키를 통과시켰다: %q", key)
		}
	}
}

// 끝에 있는 제목은 첫 창에서 바로 잡힌다.
func TestReadClaudeAITitleReadsTailFirst(t *testing.T) {
	root := t.TempDir()
	filler := `{"type":"assistant","message":{"content":"` + strings.Repeat("가", 2000) + `"}}`
	lines := make([]string, 0, 201)
	for i := 0; i < 200; i++ { // 첫 창(64KB)을 넉넉히 넘긴다
		lines = append(lines, filler)
	}
	lines = append(lines, `{"type":"ai-title","aiTitle":"끝쪽 제목","sessionId":"s1"}`)
	writeTranscript(t, root, "C--repo", "s1", lines...)

	if got, _ := readClaudeAITitle(root, "s1"); got != "끝쪽 제목" {
		t.Fatalf("제목 = %q, want 끝쪽 제목", got)
	}
}

// 창보다 큰 레코드가 끝에 오면 첫 창에 온전한 줄이 하나도 없다.
// 그때는 상한까지 창을 넓혀서 찾아낸다.
func TestReadClaudeAITitleExpandsPastHugeRecord(t *testing.T) {
	root := t.TempDir()
	huge := `{"type":"file-history-snapshot","snapshot":"` + strings.Repeat("x", 200<<10) + `"}`
	writeTranscript(t, root, "C--repo", "s1",
		`{"type":"ai-title","aiTitle":"거대 레코드 앞의 제목","sessionId":"s1"}`,
		huge,
	)

	if got, _ := readClaudeAITitle(root, "s1"); got != "거대 레코드 앞의 제목" {
		t.Fatalf("제목 = %q — 창을 넓히지 못했다", got)
	}
}

// 상한을 넘어서까지 뒤지지는 않는다.
func TestReadClaudeAITitleStopsAtCap(t *testing.T) {
	root := t.TempDir()
	filler := `{"type":"assistant","message":{"content":"` + strings.Repeat("가", 2000) + `"}}`
	lines := []string{`{"type":"ai-title","aiTitle":"아주 앞쪽 제목","sessionId":"s1"}`}
	for i := 0; i < 400; i++ { // 상한(1MB)을 넘긴다
		lines = append(lines, filler)
	}
	writeTranscript(t, root, "C--repo", "s1", lines...)

	if got, ok := readClaudeAITitle(root, "s1"); ok {
		t.Fatalf("상한 밖의 제목을 읽었다: %q", got)
	}
}

// **프라이버시 경계다.** 트랜스크립트에는 대화 원문이 통째로 들어 있고, 우리가 읽는
// 구조체에는 그 필드가 없어야 한다. 필드를 늘리면 이 테스트가 깨진다.
func TestClaudeTitleRecordDropsConversation(t *testing.T) {
	const secret = "회사 비밀이 담긴 프롬프트 원문"
	line := `{"type":"user","cwd":"C:\repo","gitBranch":"main",` +
		`"message":{"role":"user","content":"` + secret + `"},"sessionId":"s1"}`

	var rec claudeTitleRecord
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Type != "user" {
		t.Fatalf("type = %q", rec.Type)
	}
	out, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), secret) {
		t.Fatalf("원문이 구조체에 남았다: %s", out)
	}
}
