package main

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf16"
)

func removeTemporaryExecutable(exe, dir string) {
	// Windows는 실행 중 파일을 지울 수 없다. 종료를 기다리는 자식이 두 경로만 정리한다.
	// 단일 인용부호와 LiteralPath로 경로의 변수·와일드카드 해석을 막는다.
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	script := fmt.Sprintf("Wait-Process -Id %d -Timeout 120 -ErrorAction SilentlyContinue; Remove-Item -LiteralPath %s -Force -ErrorAction SilentlyContinue; [IO.Directory]::Delete(%s)", os.Getpid(), quote(exe), quote(dir))
	units := utf16.Encode([]rune(script))
	data := make([]byte, len(units)*2)
	for i, unit := range units {
		binary.LittleEndian.PutUint16(data[i*2:], unit)
	}
	cmd := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(data))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if cmd.Start() == nil {
		_ = cmd.Process.Release()
	}
}
