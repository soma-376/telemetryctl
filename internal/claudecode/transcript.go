package claudecode

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

const (
	tailBytes    = 64 << 10
	maxTailBytes = 1 << 20
)

// titleRecord는 대화 원문을 보관하지 않고 제목 필드만 읽는다 (ADR 0003).
type titleRecord struct {
	Type    string `json:"type"`
	AITitle string `json:"aiTitle"`
}

const aiTitleType = "ai-title"

// TranscriptRoot는 ~/.claude/projects 경로다. 홈을 찾지 못하면 빈 값을 반환한다.
func TranscriptRoot() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

// 세션 키로 디렉터리 탐색이나 글로브 패턴을 주입하지 못하게 한다.
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

func findTranscript(root, sessionKey string) (string, bool) {
	if root == "" || !safeSessionKey(sessionKey) {
		return "", false
	}
	matches, err := filepath.Glob(filepath.Join(root, "*", sessionKey+".jsonl"))
	if err != nil || len(matches) == 0 {
		return "", false
	}
	return matches[0], true
}

// 꼬리에서 읽은 첫 줄이 잘렸으면 버린다.
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
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			buf = buf[i+1:]
		} else {
			buf = nil
		}
	}
	return buf, nil
}

func scanAITitle(window []byte) (string, bool) {
	title := ""
	for _, line := range strings.Split(string(window), "\n") {
		if !strings.Contains(line, aiTitleType) {
			continue
		}
		var rec titleRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if rec.Type == aiTitleType && rec.AITitle != "" {
			title = rec.AITitle
		}
	}
	return title, title != ""
}

// ReadAITitle은 ai-title을 파일 꼬리 64KiB부터 최대 1MiB까지 탐색한다.
// 제목을 찾지 못하면 다음 조회 기회에 재시도할 수 있도록 false를 반환한다.
func ReadAITitle(root, sessionKey string) (string, bool) {
	path, ok := findTranscript(root, sessionKey)
	if !ok {
		return "", false
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	for n := int64(tailBytes); ; n *= 2 {
		window, err := readFileTail(path, n)
		if err != nil {
			return "", false
		}
		if title, ok := scanAITitle(window); ok {
			return title, true
		}
		if n >= info.Size() || n >= maxTailBytes {
			return "", false
		}
	}
}
