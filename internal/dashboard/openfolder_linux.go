//go:build linux

package dashboard

// 리눅스는 데스크탑 환경마다 파일 관리자가 다르므로 freedesktop 의 `xdg-open` 에 맡긴다.
// 없는 환경(헤드리스 서버)에서는 실행 자체가 실패하고 그 사실이 open_failed 로 올라간다.
func fileManagerCommand() (name string, args []string, ok bool) {
	return "xdg-open", nil, true
}

// acceptExitCode 는 성공으로 볼 종료 코드다. xdg-open 은 성공하면 0 이다.
func acceptExitCode(code int) bool { return code == 0 }
