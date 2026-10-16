package dashboard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/session"
	"github.com/your-org/pulsemetry/internal/store"
)

// 이 파일이 **작업 폴더 열기의 보안 계약** 이다 (PROJ-96).
//
// 지켜야 할 것이 셋이다.
//
//  1. 프런트가 임의 경로를 건넬 자리가 **타입에 없다**.
//  2. 열기 전에 존재하는 디렉터리인지 확인한다 — 없는 경로·파일·상대 경로·비정규형은 거절한다.
//  3. 파일 관리자 호출은 argv 다. 셸 문자열 결합이 없으므로 경로의 `;`·`&&`·`$(...)`·백틱은
//     **파일 이름의 일부**로 그대로 전달된다.
//
// 3번이 특히 오해받기 쉽다. 그런 문자를 걸러내는 것이 정답이 아니다 — 그런 이름의
// 디렉터리는 실제로 만들 수 있고 사용자는 그것을 열 권리가 있다. 정답은 셸을 거치지
// 않는 것이고, 그 사실을 붙드는 것이 recordingOpener 의 argv 단언이다.

// recordingOpener 는 진짜 파일 관리자 대신 무엇이 넘어왔는지 기록한다.
type recordingOpener struct {
	calls []string
	err   error
}

func (o *recordingOpener) Open(_ context.Context, path string) error {
	o.calls = append(o.calls, path)
	return o.err
}

// ── API 모양 ────────────────────────────────────────────────────────────────

// 「프런트가 전달한 임의 경로는 열지 않는다」 를 검증이 아니라 **타입**으로 지킨다.
// 경로 인자가 없으면 뚫릴 자리도 없다.
func TestOpenAPIHasNoPathParameter(t *testing.T) {
	cases := []struct {
		name string
		fn   any
	}{
		{"Service.OpenWorkspace", (*Service).OpenWorkspace},
		{"Service.WorkspaceFolder", (*Service).WorkspaceFolder},
		{"Reader.WorkspaceFolder", (*Reader).WorkspaceFolder},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ft := reflect.TypeOf(tc.fn)
			for i := range ft.NumIn() {
				if ft.In(i).Kind() == reflect.String {
					t.Fatalf("%s 의 %d번 인자가 문자열이다 — 프런트가 경로를 건넬 자리가 생긴다", tc.name, i)
				}
			}
			// 세션 식별자 하나만 받는다 (수신자 + ctx + int64).
			if ft.NumIn() != 3 || ft.In(2).Kind() != reflect.Int64 {
				t.Fatalf("%s 의 인자가 (ctx, int64) 가 아니다: %v", tc.name, ft)
			}
		})
	}
}

// 파일 관리자 호출에 셸이 끼어들 자리가 없어야 한다.
func TestFileManagerCommandIsNotAShell(t *testing.T) {
	name, args, ok := fileManagerCommand()
	if !ok {
		t.Skip("이 운영체제에는 파일 관리자 호출 방법이 정의돼 있지 않다")
	}
	for _, banned := range []string{"sh", "bash", "zsh", "cmd", "cmd.exe", "powershell"} {
		if name == banned {
			t.Fatalf("파일 관리자 명령이 셸이다: %q", name)
		}
	}
	for _, a := range args {
		if a == "-c" || a == "/c" {
			t.Fatalf("파일 관리자 인자에 셸 실행 플래그가 있다: %v", args)
		}
	}
}

// ── 경로 검증 ───────────────────────────────────────────────────────────────

// 존재하지 않는 경로·파일 경로·경로 인젝션 입력이 거부되는지 본다 (인수조건).
func TestValidateWorkspacePathRejections(t *testing.T) {
	dir := t.TempDir()

	realDir := filepath.Join(dir, "workspace")
	if err := os.Mkdir(realDir, 0o700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	realFile := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(realFile, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	sep := string(filepath.Separator)
	cases := []struct {
		name string
		path string
		want OpenReason
	}{
		{"정상 디렉터리", realDir, OpenReasonNone},
		{"빈 경로", "", OpenReasonPathMissing},
		{"공백만 있는 경로", "   ", OpenReasonPathMissing},
		{"상대 경로", "dev/telemetryctl", OpenReasonPathNotAbsolute},
		{"현재 디렉터리 상대 경로", "./dev", OpenReasonPathNotAbsolute},
		// filepath.Join 은 경로를 정규화하므로 여기서는 문자열로 직접 만든다 — DB 에 그런
		// 값이 들어 있는 상황을 재현하는 것이 이 케이스의 목적이다.
		{"상위 디렉터리 탈출", realDir + sep + ".." + sep + ".." + sep + "etc", OpenReasonPathNotClean},
		{"현재 디렉터리 조각", realDir + sep + "." + sep + "src", OpenReasonPathNotClean},
		{"끝에 구분자", realDir + sep, OpenReasonPathNotClean},
		{"중복 구분자", dir + sep + sep + "workspace", OpenReasonPathNotClean},
		{"개행 주입", realDir + "\nrm -rf /", OpenReasonPathUnsafe},
		{"캐리지리턴 주입", realDir + "\rmalicious", OpenReasonPathUnsafe},
		{"NUL 주입", realDir + "\x00/etc/passwd", OpenReasonPathUnsafe},
		{"존재하지 않는 경로", filepath.Join(dir, "사라진프로젝트"), OpenReasonPathNotFound},
		{"디렉터리가 아니라 파일", realFile, OpenReasonPathNotDirectory},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := validateWorkspacePath(7, tc.path)
			if got.Reason != tc.want {
				t.Fatalf("Reason = %q, want %q (%+v)", got.Reason, tc.want, got)
			}
			if tc.want == OpenReasonNone {
				if !got.Openable || got.Path != tc.path {
					t.Fatalf("정상 경로가 열리지 않는다: %+v", got)
				}
				return
			}
			if got.Openable {
				t.Error("거절된 경로인데 Openable = true")
			}
			// 거절된 값을 화면에 되돌려 주면 그것을 다시 어딘가로 넘길 수 있다.
			if got.Path != "" {
				t.Errorf("거절된 경로가 응답에 실렸다: %q", got.Path)
			}
			if got.SessionID != 7 {
				t.Errorf("SessionID = %d, want 7", got.SessionID)
			}
		})
	}
}

// 셸 메타문자가 든 **실재하는** 디렉터리는 거절 대상이 아니다. 정확히 그 이름 하나로
// 전달되는 것이 정답이다 — argv 라서 성립한다.
func TestOpenWorkspacePassesShellMetacharactersAsOneArgument(t *testing.T) {
	names := []string{
		"proj; rm -rf ~",
		"proj && curl evil.example",
		"proj $(whoami)",
		"proj `id`",
		"proj | tee /tmp/x",
		"proj > out",
		"proj 'quoted' \"double\"",
		"proj*?[]",
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, name)
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Skipf("이 파일 시스템은 그 이름을 만들지 못한다: %v", err)
			}

			f := newFixture(t)
			f.write(store.Batch{Sessions: []session.Session{
				newSession("s-meta", testNow.Add(-time.Hour), workspace(path)),
			}})
			id := f.sessionID(vendorClaude, "s-meta")

			opener := &recordingOpener{}
			got, err := openWorkspace(context.Background(), f.reader, opener, id)
			if err != nil {
				t.Fatalf("openWorkspace: %v", err)
			}
			if !got.Openable || !got.Opened {
				t.Fatalf("실재하는 디렉터리를 열지 못했다: %+v", got)
			}
			// argv 는 정확히 한 개이고 그것이 경로 전체다. 셸을 거쳤다면 여기서 쪼개졌을 것이다.
			if len(opener.calls) != 1 || opener.calls[0] != path {
				t.Fatalf("파일 관리자에 넘어간 인자 = %q, want [%q]", opener.calls, path)
			}
		})
	}
}

// ── 세션 id 로만 경로를 정한다 ──────────────────────────────────────────────

func TestWorkspaceFolderResolvesPathFromSessionID(t *testing.T) {
	root := t.TempDir()
	good := filepath.Join(root, "telemetryctl")
	if err := os.Mkdir(good, 0o700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	gone := filepath.Join(root, "옮겨간프로젝트")
	file := filepath.Join(root, "README.md")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	f := newFixture(t)
	f.write(store.Batch{Sessions: []session.Session{
		newSession("s-good", testNow.Add(-3*time.Hour), workspace(good)),
		newSession("s-gone", testNow.Add(-2*time.Hour), workspace(gone)),
		newSession("s-file", testNow.Add(-time.Hour), workspace(file)),
		newSession("s-none", testNow.Add(-30*time.Minute), workspace("")),
	}})

	cases := []struct {
		name string
		key  string
		want OpenReason
	}{
		{"실재하는 작업 폴더", "s-good", OpenReasonNone},
		{"지워진 작업 폴더", "s-gone", OpenReasonPathNotFound},
		{"파일을 가리키는 세션", "s-file", OpenReasonPathNotDirectory},
		{"경로가 없는 세션", "s-none", OpenReasonPathMissing},
	}
	ctx := context.Background()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := f.sessionID(vendorClaude, tc.key)
			got, err := f.reader.WorkspaceFolder(ctx, id)
			if err != nil {
				t.Fatalf("WorkspaceFolder: %v", err)
			}
			if got.Reason != tc.want {
				t.Fatalf("Reason = %q, want %q (%+v)", got.Reason, tc.want, got)
			}
			if tc.want == OpenReasonNone && got.ProjectName != "telemetryctl" {
				t.Errorf("ProjectName = %q, want telemetryctl", got.ProjectName)
			}
			// 조회는 열지 않는다.
			if got.Opened {
				t.Error("WorkspaceFolder 가 폴더를 열었다 — 판정만 해야 한다")
			}
		})
	}

	t.Run("없는 세션 id", func(t *testing.T) {
		got, err := f.reader.WorkspaceFolder(ctx, 999999)
		if err != nil {
			t.Fatalf("WorkspaceFolder: %v", err)
		}
		if got.Reason != OpenReasonSessionNotFound {
			t.Fatalf("Reason = %q, want session_not_found", got.Reason)
		}
	})
}

// 거절된 경로에는 파일 관리자를 부르지 않는다. 부르고 나서 실패를 보고하는 것과
// 아예 부르지 않는 것은 보안적으로 다르다.
func TestOpenWorkspaceNeverInvokesOpenerOnRejection(t *testing.T) {
	root := t.TempDir()
	gone := filepath.Join(root, "없는폴더")
	file := filepath.Join(root, "a.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	f := newFixture(t)
	f.write(store.Batch{Sessions: []session.Session{
		newSession("s-gone", testNow.Add(-2*time.Hour), workspace(gone)),
		newSession("s-file", testNow.Add(-time.Hour), workspace(file)),
		newSession("s-rel", testNow.Add(-30*time.Minute), workspace("relative/path")),
	}})

	opener := &recordingOpener{}
	ctx := context.Background()
	for _, key := range []string{"s-gone", "s-file", "s-rel"} {
		got, err := openWorkspace(ctx, f.reader, opener, f.sessionID(vendorClaude, key))
		if err != nil {
			t.Fatalf("openWorkspace(%s): %v", key, err)
		}
		if got.Opened || got.Openable {
			t.Errorf("%s: 거절돼야 하는데 %+v", key, got)
		}
	}
	if len(opener.calls) != 0 {
		t.Fatalf("거절된 경로로 파일 관리자를 불렀다: %q", opener.calls)
	}
	// DB 가 없는 Reader 도 마찬가지다.
	empty, err := Open(store.PathIn(t.TempDir()))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { empty.Close() }) //nolint:errcheck // 테스트 정리
	if _, err := openWorkspace(ctx, empty, opener, 1); err != nil {
		t.Fatalf("openWorkspace(미설치): %v", err)
	}
	if len(opener.calls) != 0 {
		t.Fatalf("미설치 상태에서 파일 관리자를 불렀다: %q", opener.calls)
	}
}

// 검증 통과와 실제 열기 성공은 다른 사실이다. 화면이 둘을 구분해 안내할 수 있어야 한다.
func TestOpenWorkspaceReportsOpenerFailure(t *testing.T) {
	root := t.TempDir()
	good := filepath.Join(root, "ws")
	if err := os.Mkdir(good, 0o700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	f := newFixture(t)
	f.write(store.Batch{Sessions: []session.Session{
		newSession("s-ok", testNow.Add(-time.Hour), workspace(good)),
	}})
	id := f.sessionID(vendorClaude, "s-ok")

	cases := []struct {
		name string
		err  error
		want OpenReason
	}{
		{"파일 관리자 실행 실패", errors.New("exec: \"xdg-open\": not found"), OpenReasonOpenFailed},
		{"지원하지 않는 운영체제", errUnsupportedPlatform, OpenReasonUnsupportedPlatform},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opener := &recordingOpener{err: tc.err}
			got, err := openWorkspace(context.Background(), f.reader, opener, id)
			if err != nil {
				t.Fatalf("openWorkspace: %v", err)
			}
			if got.Opened {
				t.Error("Opened = true — 열지 못했는데 열렸다고 한다")
			}
			if !got.Openable {
				t.Error("Openable = false — 경로 검증은 통과했다")
			}
			if got.Reason != tc.want {
				t.Fatalf("Reason = %q, want %q", got.Reason, tc.want)
			}
			// 실패 설명에 호출 명령이 그대로 실리면 사용자에게 아무 도움이 안 된다.
			if strings.Contains(got.Detail, "exec") {
				t.Errorf("Detail 에 내부 사정이 실렸다: %q", got.Detail)
			}
		})
	}
}

// 응답 타입의 json 태그가 snake_case 여야 프런트엔드가 필드를 찾는다 (ADR 0004).
func TestOpenFolderResponseTagsAreSnakeCase(t *testing.T) {
	assertSnakeCaseTags(t, WorkspaceFolder{})
}

// GUI 가 실제로 쓰는 경로다. 서비스가 Reader 의 판정을 건너뛰거나 다른 값을 열면 안 된다.
func TestServiceOpenWorkspaceGoesThroughValidation(t *testing.T) {
	root := t.TempDir()
	good := filepath.Join(root, "telemetryctl")
	if err := os.Mkdir(good, 0o700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	f := newFixture(t)
	f.write(store.Batch{Sessions: []session.Session{
		newSession("s-ok", testNow.Add(-2*time.Hour), workspace(good)),
		newSession("s-bad", testNow.Add(-time.Hour), workspace(filepath.Join(root, "없음"))),
	}})

	svc := NewService(f.path)
	if err := svc.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { svc.Stop() }) //nolint:errcheck // 테스트 정리
	opener := &recordingOpener{}
	svc.opener = opener

	ctx := context.Background()
	got, err := svc.OpenWorkspace(ctx, f.sessionID(vendorClaude, "s-ok"))
	if err != nil {
		t.Fatalf("OpenWorkspace: %v", err)
	}
	if !got.Opened || len(opener.calls) != 1 || opener.calls[0] != good {
		t.Fatalf("열린 경로 = %q (%+v)", opener.calls, got)
	}

	bad, err := svc.OpenWorkspace(ctx, f.sessionID(vendorClaude, "s-bad"))
	if err != nil {
		t.Fatalf("OpenWorkspace: %v", err)
	}
	if bad.Opened || bad.Reason != OpenReasonPathNotFound {
		t.Fatalf("거절되지 않았다: %+v", bad)
	}
	if len(opener.calls) != 1 {
		t.Fatalf("거절된 세션으로도 파일 관리자를 불렀다: %q", opener.calls)
	}
}
