package autostart

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolveExecPath 는 서비스 파일에 적을 실행 파일의 절대 경로를 정한다.
//
// # 유닛 파일에는 os.Executable() 결과를 **해석하지 않고** 적는다
//
// EvalSymlinks 는 휘발성 판정에만 쓰고 기록하는 값은 아니다. Homebrew·asdf 류 설치에서는
// 심볼릭 링크(/opt/homebrew/bin/telemetryctl)가 업그레이드를 가로질러 **안정적인 이름**이고
// 그 대상(.../Cellar/pulsemetry/1.2.3/bin/…)은 버전마다 무효화된다. 해석해서 적으면
// 다음 업그레이드에서 등록이 통째로 죽는다.
//
// **비대칭 주의:** 리눅스에서 os.Executable() 은 /proc/self/exe 를 읽으므로 이미 완전히
// 해석돼 있어 심볼릭 링크를 보존할 수 없다. 즉 여기 규칙은 "항상 심볼릭 링크를 보존한다"
// 가 아니라 **"OS 가 준 것 이상으로 해석하지 않는다"** 다. 그래서 리눅스에서는 업그레이드
// 후 재enroll 없이 바이너리만 갈면 드리프트가 남고, Status 가 그것을 보고한다.
//
// # 임시 디렉터리는 하드 오류다
//
// `go run` 이 만든 바이너리는 프로세스가 끝나면 사라진다. 사라질 경로를 재시작 정책과
// 함께 등록하면 **영구 크래시 루프**가 되고, 그것은 등록을 거부하는 것보다 훨씬 나쁘다.
// 탈출구는 Options.ExecPath(CLI 의 --exec-path)이고 그것이 곧 패키저·CI 의 통로다.
func resolveExecPath() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("실행 파일 경로를 알 수 없습니다: %w", err)
	}
	if volatileExecPath(p) {
		// 경로도 함께 돌려준다. Status 는 휘발성이어도 드리프트 비교에 쓴다.
		return p, fmt.Errorf("%w: %s", ErrExecPathVolatile, p)
	}
	return p, nil
}

// volatileExecPath 는 경로가 사라질 자리인지 본다. 원본과 심볼릭 링크 해석 결과를 모두 본다 —
// go run 은 링크를 쓰지 않지만, 안정적인 이름이 임시 대상을 가리키는 배치도 똑같이 위험하다.
func volatileExecPath(p string) bool {
	candidates := []string{p}
	if resolved, err := filepath.EvalSymlinks(p); err == nil && resolved != p {
		candidates = append(candidates, resolved)
	}
	for _, c := range candidates {
		if isVolatilePath(c) {
			return true
		}
	}
	return false
}

// isVolatilePath 의 판정 근거는 셋이다.
//
//  1. os.TempDir() 아래 — TMPDIR 을 존중하므로 macOS 의 /var/folders/… 도 여기 걸린다.
//  2. "go-build" 를 포함 — `go run` 과 `go test` 의 빌드 캐시 산출물이다.
//  3. darwin 의 /private/var/folders 와 /var/folders — TMPDIR 이 지워졌거나 다른 값일 때의
//     보루다 (/var/folders 는 /private/var/folders 의 심볼릭 링크라 둘 다 본다).
func isVolatilePath(p string) bool {
	if p == "" {
		return false
	}
	if tmp := os.TempDir(); tmp != "" && underDir(p, tmp) {
		return true
	}
	if strings.Contains(filepath.ToSlash(p), "/go-build") {
		return true
	}
	for _, prefix := range []string{"/private/var/folders", "/var/folders"} {
		if underDir(p, prefix) {
			return true
		}
	}
	return false
}

// underDir 는 p 가 dir 아래에 있는지 본다. 문자열 접두사만 보면 /tmpfoo 가 /tmp 아래로
// 잘못 걸리므로 구분자까지 맞춘다.
func underDir(p, dir string) bool {
	dir = strings.TrimRight(filepath.Clean(dir), string(filepath.Separator))
	if dir == "" {
		return false
	}
	return strings.HasPrefix(filepath.Clean(p), dir+string(filepath.Separator))
}
