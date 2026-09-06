package main

import (
	"strings"
	"testing"
)

func TestRunHookIsFailOpen(t *testing.T) {
	tests := []struct {
		name string
		body string
		args []string
	}{
		{"본문이 JSON 이 아니다", `broken`, []string{"codex"}},
		{"lifecycle 훅이 아니다", `{"session_id":"thr-1","hook_event_name":"Stop"}`, []string{"codex"}},
		{"session_id 가 없다", `{"session_id":"","hook_event_name":"SessionStart"}`, []string{"codex"}},
		{"모르는 벤더다", `{"session_id":"thr-1","hook_event_name":"SessionEnd"}`, []string{"nope"}},
		{"사용자 데이터 경로", `{"session_id":"thr-1","hook_event_name":"SessionEnd"}`, []string{"codex", "--data-dir", t.TempDir()}},
		{"잘못된 데이터 경로 옵션", `{}`, []string{"codex", "--data-dir"}},
		{"인자가 없다", `{}`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if code := runHook(strings.NewReader(tt.body), tt.args); code != 0 {
				t.Fatalf("code=%d", code)
			}
		})
	}
}
