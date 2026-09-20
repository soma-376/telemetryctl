package main

import (
	"os"
	"path/filepath"
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
			if len(tt.args) == 1 {
				tt.args = append(tt.args, "--data-dir", t.TempDir())
			}
			if code := runHook(strings.NewReader(tt.body), tt.args); code != 0 {
				t.Fatalf("code=%d", code)
			}
		})
	}
}

func TestHookDiagnosticFailureAndSize(t *testing.T) {
	dir := t.TempDir()
	input := `{"hook_event_name":"SessionEnd","session_id":"probe","private":"secret-body"}`
	if code := runHook(strings.NewReader(input), []string{"codex", "--data-dir", dir}); code != 0 {
		t.Fatal(code)
	}
	path := filepath.Join(dir, "hook-bridge.log")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"stage=entry", "session_id=probe", "missing_or_unreadable", "stage=result", "stage=exit"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("missing %s: %s", want, data)
		}
	}
	if strings.Contains(string(data), "secret-body") {
		t.Fatal("본문이 진단 로그에 노출됨")
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", hookLogMaxBytes)), 0600); err != nil {
		t.Fatal(err)
	}
	newHookDiagnostic(dir).record("entry", "rotation")
	info, err := os.Stat(path)
	if err != nil || info.Size() >= hookLogMaxBytes {
		t.Fatalf("크기 제한 실패: %v %v", info, err)
	}
}
