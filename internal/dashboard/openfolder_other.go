//go:build !darwin && !linux && !windows

package dashboard

// 그 밖의 운영체제에는 부를 명령이 정의돼 있지 않다. 아무거나 추측해서 실행하는 것보다
// unsupported_platform 으로 정직하게 거절하는 편이 낫다.
func fileManagerCommand() (name string, args []string, ok bool) {
	return "", nil, false
}

func acceptExitCode(code int) bool { return code == 0 }
