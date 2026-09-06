package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"

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
	if len(args) != 1 {
		return 0
	}
	vendorID, ok := vendor.Normalize(args[0])
	if !ok {
		return 0
	}
	body, err := io.ReadAll(io.LimitReader(stdin, 64<<10))
	if err != nil {
		return 0
	}
	var name hookEventName
	if err := json.Unmarshal(body, &name); err != nil {
		return 0
	}
	var end bool
	switch name.HookEventName {
	case "SessionStart":
	case "SessionEnd":
		end = true
	default:
		return 0
	}
	// 본문은 벤더가 준 그대로 넘긴다. 파싱은 데몬이 한다.
	event, err := localapi.DecodeHook(bytes.NewReader(body), string(vendorID), end)
	if err != nil {
		return 0
	}
	dataDir, err := defaultDataDir()
	if err != nil {
		return 0
	}
	_ = localapi.NewClient(dataDir).SubmitLifecycle(context.Background(), event, body)
	return 0
}
