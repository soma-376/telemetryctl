package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Claude Code 는 세션을 ~/.claude/projects/<작업폴더-인코딩>/<session_id>.jsonl 에 남긴다.
// **파일 이름이 곧 OTel 의 session.id** 라 세션 키로 바로 찾을 수 있다(실측으로 확인).
//
// 그 안의 ai-title 레코드가 Claude Code 가 만든 세션 제목이다. 첫 사용자 프롬프트 직후에
// 정해지고 그 뒤로 바뀌지 않으며, 같은 값이 파일 전체에 반복해서 다시 찍힌다. 그래서
// **끝에서 조금만 읽어도 잡힌다** — 세션이 아무리 길어도 비용이 일정하다.
//
// 다만 레코드 하나가 창보다 클 수 있다. 실측에서 EOF 와 마지막 ai-title 사이는 최대 30KB
// 였지만 단일 줄이 700KB 를 넘는 경우가 있었다(파일 이력 스냅샷·긴 응답). 그런 줄이 끝에
// 오면 첫 창 안에 온전한 줄이 하나도 없다. 못 찾으면 상한까지 창을 두 배씩 넓힌다.
const (
	claudeTranscriptTailBytes    = 64 << 10
	claudeTranscriptMaxTailBytes = 1 << 20
)

// claudeTitleRecord 는 트랜스크립트에서 읽는 것 **전부**다.
//
// 필드가 이 둘뿐인 것이 프라이버시 경계다. 파일에는 대화 원문이 통째로 들어 있고
// encoding/json 은 구조체에 없는 필드를 버린다. 여기에 message 를 더하는 순간 원문이
// 딸려 들어온다 (ADR 0003). 필드를 늘릴 때는 그것이 원문인지 먼저 따져야 한다.
type claudeTitleRecord struct {
	Type    string `json:"type"`
	AITitle string `json:"aiTitle"`
}

const claudeAITitleType = "ai-title"

// claudeTranscriptRoot 는 Claude Code 의 세션 저장 위치다. 홈을 못 찾으면 빈 문자열이고,
// 호출자는 그것을 "이 기능을 끈다" 로 다룬다.
func claudeTranscriptRoot() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

// safeSessionKey 는 세션 키가 경로로 쓰여도 안전한지 본다.
//
// 키는 **벤더가 준 값**이라 신뢰할 수 없다. 구분자나 글로브 메타문자가 들어오면 의도한
// 디렉터리 밖을 읽게 되므로, 실제 형태(UUID)만 통과시킨다.
func safeSessionKey(key string) bool {
	if key == "" || len(key) > 128 {
		return false
	}
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// findClaudeTranscript 는 세션 키에 해당하는 트랜스크립트 경로를 찾는다.
// 작업 폴더별로 디렉터리가 갈리므로 한 단계만 훑는다.
func findClaudeTranscript(root, sessionKey string) (string, bool) {
	if root == "" || !safeSessionKey(sessionKey) {
		return "", false
	}
	matches, err := filepath.Glob(filepath.Join(root, "*", sessionKey+".jsonl"))
	if err != nil || len(matches) == 0 {
		return "", false
	}
	// 같은 세션이 두 곳에 있을 이유는 없지만, 있어도 결과가 흔들리지 않게 첫 번째로 고정한다.
	return matches[0], true
}

// readFileTail 은 파일 끝에서 최대 max 바이트를 읽는다. 잘린 첫 줄은 버린다 —
// 중간부터 시작한 줄은 JSON 이 아니다.
func readFileTail(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	offset := int64(0)
	if info.Size() > max {
		offset = info.Size() - max
	}
	if _, err := f.Seek(offset, 0); err != nil {
		return nil, err
	}
	buf := make([]byte, info.Size()-offset)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return nil, err
	}
	buf = buf[:n]
	if offset > 0 {
		if i := indexNewline(buf); i >= 0 {
			buf = buf[i+1:]
		} else {
			buf = nil
		}
	}
	return buf, nil
}

func indexNewline(b []byte) int {
	for i, c := range b {
		if c == '\n' {
			return i
		}
	}
	return -1
}

// scanClaudeAITitle 은 창 안의 마지막 ai-title 값을 돌려준다.
//
// 앞에서부터 훑고 마지막 것을 남긴다. 제목은 바뀌지 않으므로 어느 것을 잡아도 같지만,
// 혹시 바뀌었다면 나중 값이 맞다.
func scanClaudeAITitle(window []byte) (string, bool) {
	title := ""
	for _, line := range strings.Split(string(window), "\n") {
		// 줄마다 JSON 파싱을 돌리지 않기 위한 값싼 사전 거르기다. 창이 64KB 라 이것만으로
		// 파싱 횟수가 수천에서 수십으로 줄어든다.
		if !strings.Contains(line, claudeAITitleType) {
			continue
		}
		var rec claudeTitleRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if rec.Type == claudeAITitleType && rec.AITitle != "" {
			title = rec.AITitle
		}
	}
	return title, title != ""
}

// readClaudeAITitle 은 세션 키로 트랜스크립트를 찾아 Claude Code 가 만든 제목을 읽는다.
//
// 못 찾는 것은 **오류가 아니다.** 아직 제목이 안 만들어졌거나(첫 프롬프트 전), 파일이
// 없거나, 포맷이 바뀐 것이다. 셋 다 "지금 없다" 로 다루고 다음 기회에 다시 본다.
//
// 사용자가 직접 붙인 제목(custom-title 레코드)은 아직 읽지 않는다. 둘 다 벤더가 만들어
// 둔 제목이라 저장 규칙은 같지만, 우선순위를 정하는 것은 별도 판단이다.
func readClaudeAITitle(root, sessionKey string) (string, bool) {
	path, ok := findClaudeTranscript(root, sessionKey)
	if !ok {
		return "", false
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	for n := int64(claudeTranscriptTailBytes); ; n *= 2 {
		window, err := readFileTail(path, n)
		if err != nil {
			return "", false
		}
		if title, ok := scanClaudeAITitle(window); ok {
			return title, true
		}
		// 창이 파일 전체를 덮었거나 상한에 닿았으면 정말 없는 것이다.
		if n >= info.Size() || n >= claudeTranscriptMaxTailBytes {
			return "", false
		}
	}
}
