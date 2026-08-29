package dashboard

// 작업 폴더 열기 (PROJ-96).
//
// # 프런트가 경로를 건네는 길이 없다
//
// 이 파일의 공개 입구는 세션 식별자 하나만 받는다 — `WorkspaceFolder(ctx, sessionID int64)`.
// **경로를 담을 매개변수가 타입에 존재하지 않는다.** 검증으로 막는 것과 인자가 없는 것은
// 다르다. 검증은 언젠가 조건 하나가 느슨해지면 뚫리지만, 없는 인자는 뚫릴 자리가 없다.
// 열 경로는 언제나 우리가 `sessions.workspace_path` 에서 직접 읽은 값이다 (ADR 0010).
//
// 새 진입점을 만들 때도 이 성질을 유지해야 한다. "사용자가 고른 폴더를 연다" 같은 요구가
// 생기면 그것은 이 함수의 인자를 늘리는 일이 아니라 별도 결정이다.
//
// # 셸을 거치지 않는다
//
// 파일 관리자 호출은 `exec.CommandContext(name, argv...)` 다. 셸(`sh -c`)을 띄우지 않고
// 문자열 결합도 하지 않으므로 경로에 `;` · `&&` · `$(...)` · 백틱이 들어 있어도 그것은
// **파일 이름의 일부**로 전달된다. 실제로 그런 이름의 디렉터리는 만들 수 있고, 우리는
// 그것을 정상적으로 열어야 한다 — 거부가 아니라 정확한 전달이 여기서의 정답이다.
//
// 그래서 경로 검증의 목적은 "메타문자 걸러내기" 가 아니다. 존재하지 않는 것·디렉터리가
// 아닌 것·절대 경로가 아닌 것을 거르는 것이고, 그것이 인자로 넘어갔을 때 의미가 달라질
// 수 있는 값(제어 문자·NUL)만 추가로 막는다.
//
// # 왜 Reader 위에 있는가
//
// 경로의 출처가 로컬 DB 뿐이기 때문이다. ADR 0004 가 정한 대로 스키마 지식은 이 패키지
// 밖으로 나가지 않으므로, GUI 가 세션 id 로 경로를 물어 스스로 여는 구조를 만들 수 없다.

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// OpenReason 은 작업 폴더 열기가 거절된 기계 판독 가능한 사유다.
//
// 화면 문구는 이 값에서 파생시키고 Detail 문자열을 파싱하지 않는다 (vendorlimit.Reason 과
// 같은 규약). 값이 늘어날 수는 있어도 기존 값의 의미는 바뀌지 않는다.
type OpenReason string

const (
	// OpenReasonNone 은 거절 사유가 없다는 뜻이다 — 열 수 있다.
	OpenReasonNone OpenReason = ""
	// OpenReasonSessionNotFound 는 그 id 의 세션이 없다는 뜻이다. DB 가 아직 없을 때도 이것이다.
	OpenReasonSessionNotFound OpenReason = "session_not_found"
	// OpenReasonPathMissing 은 세션은 있으나 작업 경로가 기록되지 않았다는 뜻이다.
	// 벤더가 경로를 알려주지 않은 세션이 실제로 있다 (sessions.workspace_path 는 NULL 가능).
	OpenReasonPathMissing OpenReason = "path_missing"
	// OpenReasonPathNotAbsolute 는 기록된 경로가 절대 경로가 아니라는 뜻이다. 상대 경로는
	// 우리 프로세스의 작업 디렉터리 기준으로 해석되어 사용자가 의도하지 않은 곳을 가리킨다.
	OpenReasonPathNotAbsolute OpenReason = "path_not_absolute"
	// OpenReasonPathNotClean 은 경로가 정규형이 아니라는 뜻이다 — `..` 나 `.` 조각,
	// 중복 구분자, 끝의 구분자. 정규형만 받으면 "무엇을 열었는가" 가 값 하나로 확정된다.
	OpenReasonPathNotClean OpenReason = "path_not_clean"
	// OpenReasonPathUnsafe 는 경로에 제어 문자(개행·NUL 등)가 들어 있다는 뜻이다.
	// argv 로 넘기므로 셸 주입은 애초에 성립하지 않지만, 이런 값은 로그·오류 문자열을
	// 조작하고 NUL 은 exec 호출 자체를 실패시킨다.
	OpenReasonPathUnsafe OpenReason = "path_unsafe"
	// OpenReasonPathNotFound 는 그 경로가 지금 존재하지 않는다는 뜻이다 — 프로젝트를
	// 옮겼거나 지운 흔한 상태다.
	OpenReasonPathNotFound OpenReason = "path_not_found"
	// OpenReasonPathNotDirectory 는 그 자리에 있는 것이 디렉터리가 아니라는 뜻이다.
	// 파일을 파일 관리자에 넘기면 연결 프로그램이 그 파일을 **실행**할 수 있다.
	OpenReasonPathNotDirectory OpenReason = "path_not_directory"
	// OpenReasonPathUnreadable 은 경로를 확인할 권한이 없다는 뜻이다.
	OpenReasonPathUnreadable OpenReason = "path_unreadable"
	// OpenReasonUnsupportedPlatform 은 이 운영체제의 파일 관리자 호출 방법을 모른다는 뜻이다.
	OpenReasonUnsupportedPlatform OpenReason = "unsupported_platform"
	// OpenReasonOpenFailed 는 검증은 통과했으나 파일 관리자 호출이 실패했다는 뜻이다.
	OpenReasonOpenFailed OpenReason = "open_failed"
)

// WorkspaceFolder 는 세션 하나의 작업 폴더 열기 결과다.
//
// Openable 은 **검증 결과**이고 Opened 는 **호출 결과**다. 둘을 하나로 뭉치면 화면이
// "경로가 잘못됐다" 와 "파일 관리자가 없다" 를 구분해 안내하지 못한다.
type WorkspaceFolder struct {
	SessionID int64 `json:"session_id"`
	// Openable 이 true 면 검증을 통과했다는 뜻이다.
	Openable bool `json:"openable"`
	// Opened 는 파일 관리자 호출까지 성공했다는 뜻이다. 검증만 한 조회에서는 항상 false 다.
	Opened bool       `json:"opened"`
	Reason OpenReason `json:"reason"`
	// Detail 은 사람이 읽는 짧은 설명이다. 화면 분기는 Reason 으로 한다.
	Detail string `json:"detail"`
	// Path 는 **검증을 통과했을 때만** 채운다. 거절된 값을 되돌려 주면 화면이 그것을
	// 다시 어딘가로 넘길 수 있고, 그 순간 "경로는 우리가 정한다" 는 성질이 깨진다.
	Path string `json:"path"`
	// ProjectName 은 Path 의 basename 이다. 화면이 확인 문구에 쓴다.
	ProjectName string `json:"project_name"`
}

// reject 는 거절 결과를 만든다. Path 는 비운다.
func rejectFolder(sessionID int64, reason OpenReason, detail string) WorkspaceFolder {
	return WorkspaceFolder{SessionID: sessionID, Reason: reason, Detail: detail}
}

// workspacePathSQL 은 세션 하나의 작업 경로다. 원경로를 저장하는 것은 ADR 0010 의 결정이고
// 그 값의 유일한 소비자가 이 기능이다.
const workspacePathSQL = `SELECT COALESCE(workspace_path,'') FROM sessions WHERE id = ?`

// WorkspaceFolder 는 세션의 작업 폴더가 지금 열 수 있는 상태인지 판정한다. 열지는 않는다.
//
// **경로를 인자로 받지 않는다.** 열 대상은 언제나 sessionID 로 조회한 값이다.
// DB 가 없으면 에러가 아니라 session_not_found 다 — 미설치는 정상 상태다 (ADR 0004).
func (r *Reader) WorkspaceFolder(ctx context.Context, sessionID int64) (WorkspaceFolder, error) {
	db, ok := r.db()
	if !ok {
		return rejectFolder(sessionID, OpenReasonSessionNotFound, "로컬 데이터가 아직 없다"), nil
	}

	var raw string
	err := db.QueryRowContext(ctx, workspacePathSQL, sessionID).Scan(&raw)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return rejectFolder(sessionID, OpenReasonSessionNotFound, "해당 세션이 없다"), nil
	case err != nil:
		return WorkspaceFolder{}, queryErr("작업 폴더 경로 조회", err)
	}
	return validateWorkspacePath(sessionID, raw), nil
}

// validateWorkspacePath 는 DB 에서 읽은 경로가 열어도 되는 값인지 본다.
//
// 순서가 의미를 갖는다. 파일 시스템을 두드리기 전에 값의 모양부터 확정한다 — 모양이
// 이상한 값으로 Stat 을 부르면 그 자체가 부작용이 될 수 있는 경로(예: 자동 마운트)가 있다.
func validateWorkspacePath(sessionID int64, raw string) WorkspaceFolder {
	path := strings.TrimSpace(raw)
	if path == "" {
		return rejectFolder(sessionID, OpenReasonPathMissing, "이 세션에는 작업 경로가 기록되지 않았다")
	}
	if hasControlRune(path) {
		return rejectFolder(sessionID, OpenReasonPathUnsafe, "작업 경로에 제어 문자가 들어 있다")
	}
	if !filepath.IsAbs(path) {
		return rejectFolder(sessionID, OpenReasonPathNotAbsolute, "작업 경로가 절대 경로가 아니다")
	}
	if filepath.Clean(path) != path {
		// `..` 를 여기서 편다면 "우리가 연 곳" 과 "DB 에 적힌 곳" 이 달라진다.
		// 정규형만 받아 그 둘을 언제나 같게 둔다.
		return rejectFolder(sessionID, OpenReasonPathNotClean, "작업 경로가 정규형이 아니다")
	}

	info, err := os.Stat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return rejectFolder(sessionID, OpenReasonPathNotFound, "작업 폴더가 더 이상 존재하지 않는다")
	case err != nil:
		return rejectFolder(sessionID, OpenReasonPathUnreadable, "작업 폴더를 확인할 권한이 없다")
	case !info.IsDir():
		return rejectFolder(sessionID, OpenReasonPathNotDirectory, "작업 경로가 디렉터리가 아니다")
	}

	return WorkspaceFolder{
		SessionID:   sessionID,
		Openable:    true,
		Path:        path,
		ProjectName: baseName(path),
	}
}

// hasControlRune 은 문자열에 제어 문자가 있는지 본다.
//
// unicode.IsControl 을 쓰지 않고 범위로 판단하는 이유는 UTF-8 이 아닌 바이트열도 경로가
// 될 수 있어서다 — 그런 값은 rune 으로 디코드하면 U+FFFD 가 되어 검사를 빠져나간다.
func hasControlRune(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return true
		}
	}
	return false
}

// ── 파일 관리자 호출 seam ────────────────────────────────────────────────────

// FolderOpener 는 운영체제 파일 관리자를 부르는 자리다.
//
// 인터페이스로 둔 이유는 테스트가 진짜 파일 관리자를 띄우지 않게 하기 위해서다. CI 에서
// `open`·`xdg-open` 을 실제로 부르면 창이 뜨거나(로컬) 표시할 디스플레이가 없어 실패한다
// (CI). 그리고 이 seam 이 있어야 "무엇이 argv 로 넘어갔는가" 를 단언할 수 있다 —
// 셸을 거치지 않는다는 이 기능의 핵심 성질이 그 단언으로만 회귀 검증된다.
type FolderOpener interface {
	// Open 은 path 를 파일 관리자에 넘긴다. path 는 검증을 통과한 절대 경로다.
	Open(ctx context.Context, path string) error
}

// errUnsupportedPlatform 은 파일 관리자 호출 방법을 모르는 운영체제다.
var errUnsupportedPlatform = errors.New("dashboard: 이 운영체제의 파일 관리자 호출 방법을 모른다")

// execOpener 는 운영 경로다.
//
// **셸을 띄우지 않는다.** exec.CommandContext 는 argv 를 그대로 execve 에 넘기므로
// 경로에 무엇이 들어 있든 그것은 하나의 인자다. 문자열 서식(%s)으로 명령을 조립하는
// 코드가 이 파일에 들어오면 그 순간 이 성질이 사라진다.
type execOpener struct{}

func (execOpener) Open(ctx context.Context, path string) error {
	name, args, ok := fileManagerCommand()
	if !ok {
		return errUnsupportedPlatform
	}
	// 경로는 언제나 **마지막** 인자다. 절대 경로만 여기 오므로 `-` 로 시작해 옵션으로
	// 오인될 값이 들어올 수 없다.
	argv := make([]string, 0, len(args)+1)
	argv = append(argv, args...)
	argv = append(argv, path)

	cmd := exec.CommandContext(ctx, name, argv...)
	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && acceptExitCode(exitErr.ExitCode()) {
		return nil
	}
	return err
}

// openWorkspace 는 검증 후 파일 관리자를 부른다.
//
// 검증에서 걸리면 호출하지 않는다. 호출 실패는 검증 결과를 뒤집지 않는다 — Openable 은
// "경로가 멀쩡하다", Opened 는 "실제로 열렸다" 로 각각 남는다.
func openWorkspace(ctx context.Context, r *Reader, opener FolderOpener, sessionID int64) (WorkspaceFolder, error) {
	folder, err := r.WorkspaceFolder(ctx, sessionID)
	if err != nil || !folder.Openable {
		return folder, err
	}
	if opener == nil {
		opener = execOpener{}
	}
	if oerr := opener.Open(ctx, folder.Path); oerr != nil {
		folder.Reason = OpenReasonOpenFailed
		folder.Detail = "파일 관리자를 실행하지 못했다"
		if errors.Is(oerr, errUnsupportedPlatform) {
			folder.Reason = OpenReasonUnsupportedPlatform
			folder.Detail = "이 운영체제에서는 작업 폴더 열기를 지원하지 않는다"
		}
		return folder, nil
	}
	folder.Opened = true
	return folder, nil
}
