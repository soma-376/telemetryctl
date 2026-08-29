//go:build darwin

package dashboard

// macOS 는 Finder 를 `open` 으로 부른다. 경로 하나를 인자로 받는다.
func fileManagerCommand() (name string, args []string, ok bool) {
	return "open", nil, true
}

// acceptExitCode 는 성공으로 볼 종료 코드다. macOS 의 `open` 은 성공하면 0 이다.
func acceptExitCode(code int) bool { return code == 0 }
