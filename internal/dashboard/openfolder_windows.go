//go:build windows

package dashboard

// 윈도우는 탐색기를 `explorer` 로 부른다.
func fileManagerCommand() (name string, args []string, ok bool) {
	return "explorer", nil, true
}

// acceptExitCode 는 성공으로 볼 종료 코드다.
//
// **explorer.exe 는 창을 정상적으로 열고도 1 을 돌려준다.** 이것을 실패로 보면 윈도우에서는
// 폴더가 열렸는데 화면은 "열지 못했다" 를 띄운다.
func acceptExitCode(code int) bool { return code == 0 || code == 1 }
