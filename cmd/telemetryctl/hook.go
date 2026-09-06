package main

import (
	"context"
	"encoding/json"
	"io"
	"os"

	"github.com/your-org/pulsemetry/internal/localapi"
)

// codexHookInput 은 Codex가 command hook의 stdin으로 주는 필드 중 필요한 것만 받는다.
type codexHookInput struct {
	SessionID     string `json:"session_id"`
	HookEventName string `json:"hook_event_name"`
	Source        string `json:"source"`
}

func cmdHook(args []string) int { return runHook(os.Stdin, args) }

func decodeCodexLifecycle(r io.Reader) (localapi.LifecycleEvent, bool) {
	var in codexHookInput
	if err := json.NewDecoder(io.LimitReader(r, 64<<10)).Decode(&in); err != nil {
		return localapi.LifecycleEvent{}, false
	}
	event := localapi.LifecycleEvent{Vendor: "codex", SessionID: in.SessionID, Source: in.Source}
	switch in.HookEventName {
	case "SessionStart":
	case "SessionEnd":
		event.End = true
	default:
		return localapi.LifecycleEvent{}, false
	}
	return event, event.Validate() == nil
}

// runHook 은 Codex 작업을 방해하지 않는 fail-open 브리지다. 오류를 출력하지 않고 항상 0이다.
func runHook(stdin io.Reader, args []string) int {
	if len(args) != 1 || args[0] != "codex" {
		return 0
	}
	event, ok := decodeCodexLifecycle(stdin)
	if !ok {
		return 0
	}
	dataDir, err := defaultDataDir()
	if err != nil {
		return 0
	}
	_ = localapi.NewClient(dataDir).SubmitLifecycle(context.Background(), event)
	return 0
}
