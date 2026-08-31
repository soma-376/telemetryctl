package vendorlimit

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// maxCredentialBytes 는 자격증명 파일에서 읽어들일 최대 바이트다. 자격증명 파일은 수 KB 를
// 넘지 않는다. 상한이 없으면 심볼릭 링크가 거대한 파일을 가리키는 것만으로 데몬 메모리가 찬다.
const maxCredentialBytes = 1 << 20

// claudeCredentialPath 는 Claude Code 의 provider-owned 자격증명 파일 경로다.
//
// hostenv 는 벤더 **설정** 파일(settings.json·config.toml) 경로만 안다. 자격증명 파일은
// 설정과 성격이 다르고(우리가 절대 쓰지 않는다) 이 패키지 밖에서 쓸 일이 없어서 여기 둔다.
// hostenv 를 넓히면 토큰 파일 경로가 레포 전역에서 손닿는 곳에 놓이게 된다.
func claudeCredentialPath(home string) string {
	return filepath.Join(home, ".claude", ".credentials.json")
}

// credentialError 는 자격증명 로드 실패를 Reason 과 함께 나른다. 호출자가 문자열을
// 뜯어보지 않고 그대로 Result 로 옮길 수 있어야 한다.
type credentialError struct {
	reason Reason
	detail string
}

func (e *credentialError) Error() string { return e.detail }

func credErr(reason Reason, format string, args ...any) error {
	return &credentialError{reason: reason, detail: fmt.Sprintf(format, args...)}
}

// reasonOf 는 오류에서 Reason 을 뽑는다. 우리가 붙인 Reason 이 없으면 fallback 이다.
func reasonOf(err error, fallback Reason) Reason {
	var ce *credentialError
	if errors.As(err, &ce) {
		return ce.reason
	}
	return fallback
}

// displayPath 는 화면·오류에 나갈 경로 표기다. 홈 디렉터리를 ~ 로 접는다.
//
// 사용자 이름이 박힌 전체 경로는 로컬에만 두는 값이다 (ADR 0003). Result.Detail 은 GUI 로
// 그대로 나가므로 여기서 접지 않으면 화면 스크린샷에 홈 경로가 실린다.
func displayPath(home, path string) string {
	if home != "" && strings.HasPrefix(path, home) {
		return "~" + filepath.ToSlash(strings.TrimPrefix(path, home))
	}
	return filepath.Base(path)
}

// readCredentialFile 은 자격증명 파일을 **읽기 전용으로만** 연다.
//
// 실패 종류를 Reason 으로 갈라 두는 것이 이 함수의 일이다. "파일 없음"(로그인 안 함)과
// "권한 없음"(설치가 깨졌거나 다른 사용자 소유)은 사용자가 할 일이 전혀 달라서, 화면이
// 한 덩어리로 뭉뚱그리면 안 된다.
func readCredentialFile(home, path string) ([]byte, error) {
	shown := displayPath(home, path)

	f, err := os.Open(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, credErr(ReasonCredentialMissing, "%s 가 없다 — 해당 도구에 로그인하지 않았을 수 있다", shown)
	case errors.Is(err, fs.ErrPermission):
		return nil, credErr(ReasonCredentialUnreadable, "%s 를 읽을 권한이 없다", shown)
	case err != nil:
		return nil, credErr(ReasonCredentialUnreadable, "%s 를 열지 못했다", shown)
	}
	defer f.Close()

	b, err := io.ReadAll(io.LimitReader(f, maxCredentialBytes))
	if err != nil {
		// 원인 오류를 그대로 싣지 않는다. 무엇이 섞여 오는지 보장할 수 없다.
		return nil, credErr(ReasonCredentialUnreadable, "%s 를 읽지 못했다", shown)
	}
	if len(b) == 0 {
		return nil, credErr(ReasonCredentialMalformed, "%s 가 비어 있다", shown)
	}
	return b, nil
}

// claudeCredential 은 Claude 자격증명 파일에서 우리가 쓰는 값 전부다.
type claudeCredential struct {
	token Token
	// expiresAt 은 만료 시각이다. 영값이면 파일이 알려주지 않았다는 뜻이며,
	// 그때는 만료 판정을 상위의 401 에 맡긴다.
	expiresAt time.Time
	// plan 은 파일에 적힌 구독 종류다. 사용량 API 가 플랜을 알려주지 않을 때의 대안이다.
	plan string
}

// claudeCredentialFile 은 자격증명 파일의 **관측된** 모양이다.
//
// # 가정과 확인 방법
//
// Claude Code 는 OAuth 자격증명을 `{"claudeAiOauth": {...}}` 아래에 둔다. accessToken 은
// 문자열이고 expiresAt 은 **unix 밀리초** 정수다. macOS 에서는 Keychain 에 저장되어 이
// 파일이 없을 수 있고, 그때는 ReasonCredentialMissing 으로 떨어진다 (아래 주석 참고).
// 사람이 확인하려면 로그인된 장비에서 `cat ~/.claude/.credentials.json | jq 'keys'` 와
// `jq '.claudeAiOauth | keys'` 를 본다.
//
// **DisallowUnknownFields 를 쓰지 않는다.** enroll 응답 파싱과 정반대의 선택이다
// (AGENTS.md). 저기서는 서버가 우리와 계약을 맺은 상대라 필드가 늘면 즉시 알아야 하지만,
// 여기서는 우리가 남의 파일을 훔쳐보는 관측자다. 벤더가 필드를 하나 추가했다고 사용 한도
// 화면이 통째로 죽으면 안 된다.
type claudeCredentialFile struct {
	OAuth *struct {
		AccessToken      string `json:"accessToken"`
		ExpiresAt        int64  `json:"expiresAt"`
		SubscriptionType string `json:"subscriptionType"`
	} `json:"claudeAiOauth"`
}

// loadClaudeCredential 은 Claude 액세스 토큰을 메모리로만 읽는다.
//
// # macOS Keychain 에 대하여
//
// macOS 의 Claude Code 는 자격증명을 Keychain 에 두는 경우가 있어 이 파일이 없을 수 있다.
// 그래도 Keychain 을 뒤지지 않는다 — 남의 도구의 Keychain 항목을 읽으면 사용자에게 승인
// 대화상자가 뜨고, 데몬이 백그라운드에서 그 대화상자를 띄우는 것은 받아들일 수 없다.
// 파일이 없으면 조용히 unavailable 이다.
func loadClaudeCredential(home string) (claudeCredential, error) {
	path := claudeCredentialPath(home)
	b, err := readCredentialFile(home, path)
	if err != nil {
		return claudeCredential{}, err
	}
	shown := displayPath(home, path)

	var file claudeCredentialFile
	if err := json.Unmarshal(b, &file); err != nil {
		// 파싱 오류 메시지에는 실패 지점의 원문 조각이 섞일 수 있다. 토큰 파일에서 그
		// 조각은 곧 토큰이므로 원인을 싣지 않는다.
		return claudeCredential{}, credErr(ReasonCredentialMalformed, "%s 가 JSON 이 아니다", shown)
	}
	if file.OAuth == nil || strings.TrimSpace(file.OAuth.AccessToken) == "" {
		return claudeCredential{}, credErr(ReasonCredentialMalformed, "%s 에 claudeAiOauth.accessToken 이 없다", shown)
	}

	cred := claudeCredential{
		token: newToken(file.OAuth.AccessToken),
		plan:  strings.TrimSpace(file.OAuth.SubscriptionType),
	}
	if file.OAuth.ExpiresAt > 0 {
		cred.expiresAt = time.UnixMilli(file.OAuth.ExpiresAt).UTC()
	}
	return cred, nil
}
