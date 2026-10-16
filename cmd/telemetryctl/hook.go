package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/your-org/pulsemetry/internal/localapi"
	"github.com/your-org/pulsemetry/internal/vendor"
)

// hookEventName 은 어느 lifecycle 훅인지 가르는 최소한의 필드다.
type hookEventName struct {
	HookEventName string `json:"hook_event_name"`
}

func cmdHook(args []string) int { return runHook(os.Stdin, args) }

// runHook 은 벤더 작업을 방해하지 않는 fail-open 브리지다. 오류를 출력하지 않고 항상 0이다.
// http 훅을 지원하지 않는 벤더만 이 경로를 탄다.
func runHook(stdin io.Reader, args []string) int {
	if len(args) == 0 {
		return 0
	}
	vendorID, ok := vendor.Normalize(args[0])
	if !ok {
		return 0
	}
	fs := flag.NewFlagSet("hook "+args[0], flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dataDirFlag := fs.String("data-dir", "", "")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 {
		return 0
	}
	dataDir := strings.TrimSpace(*dataDirFlag)
	if dataDir == "" {
		var err error
		dataDir, err = defaultDataDir()
		if err != nil {
			return 0
		}
	}
	diagnostic := newHookDiagnostic(dataDir)
	diagnostic.record("entry", "vendor="+string(vendorID)+" data_dir="+dataDir)
	defer diagnostic.record("exit", "fail_open")
	body, err := io.ReadAll(io.LimitReader(stdin, 64<<10))
	if err != nil {
		diagnostic.record("input", "read_failed")
		return 0
	}
	var name hookEventName
	if err := json.Unmarshal(body, &name); err != nil {
		diagnostic.record("input", "invalid_json")
		return 0
	}
	var end bool
	switch name.HookEventName {
	case "SessionStart":
	case "SessionEnd":
		end = true
	default:
		diagnostic.record("input", "unsupported_event")
		return 0
	}
	// 본문은 벤더가 준 그대로 넘긴다. 파싱은 데몬이 한다.
	event, err := localapi.DecodeHook(bytes.NewReader(body), string(vendorID), end)
	if err != nil {
		diagnostic.record("input", "invalid_payload")
		return 0
	}
	diagnostic.record("input", "event="+name.HookEventName+" session_id="+event.SessionID)
	client := localapi.NewClient(dataDir)
	client.HookTrace = diagnostic.record
	if err := client.SubmitLifecycle(context.Background(), event, body); err != nil {
		diagnostic.record("result", "failed")
	} else {
		diagnostic.record("result", "ok")
	}
	return 0
}

// currentHookExecutable 은 Codex 설정에 오래 남겨도 되는 현재 바이너리 경로를 돌려준다.
// go run 산출물은 프로세스 종료 뒤 지워지므로 영구 설정에 기록하지 않는다.
func currentHookExecutable() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("Pulsemetry 실행 경로 확인 실패: %w", err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return "", fmt.Errorf("Pulsemetry 절대 실행 경로 확인 실패: %w", err)
	}
	tempDir, err := filepath.Abs(os.TempDir())
	if err == nil {
		rel, relErr := filepath.Rel(tempDir, executable)
		insideTemp := relErr == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
		if insideTemp {
			for _, part := range strings.FieldsFunc(rel, func(r rune) bool { return r == '/' || r == '\\' }) {
				if strings.HasPrefix(strings.ToLower(part), "go-build") {
					if strings.Contains(strings.ToLower(filepath.Base(executable)), ".test") {
						return filepath.Join(filepath.Dir(executable), "pulsemetry.exe"), nil
					}
					return "", errors.New("go run 임시 바이너리는 Codex hook에 등록할 수 없음: task build:cli 후 빌드 산출물로 실행하라")
				}
			}
		}
	}
	return executable, nil
}
