package main

import (
	"strings"
	"testing"
)

func TestDecodeCodexLifecycle(t *testing.T) {
	tests := []struct {
		body    string
		ok, end bool
		source  string
	}{
		{`{"session_id":"thr-1","hook_event_name":"SessionStart","source":"resume","extra":1}`, true, false, "resume"},
		{`{"session_id":"thr-1","hook_event_name":"SessionEnd"}`, true, true, ""},
		{`{"session_id":"","hook_event_name":"SessionStart"}`, false, false, ""},
		{`{"session_id":"thr-1","hook_event_name":"Stop"}`, false, false, ""},
	}
	for _, tt := range tests {
		got, ok := decodeCodexLifecycle(strings.NewReader(tt.body))
		if ok != tt.ok || (ok && (got.End != tt.end || got.Source != tt.source)) {
			t.Errorf("decode(%s) = %+v,%t", tt.body, got, ok)
		}
	}
}

func TestRunHookIsFailOpen(t *testing.T) {
	if code := runHook(strings.NewReader(`broken`), []string{"codex"}); code != 0 {
		t.Fatalf("code=%d", code)
	}
}
