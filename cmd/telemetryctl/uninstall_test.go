package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

func TestUninstallCLIRequiresConsentAndDryRunIsReadOnly(t *testing.T) {
	for _, mode := range []string{"dry", "cancel", "execute"} {
		t.Run(mode, func(t *testing.T) {
			f := newCLIFixture(t)
			before, err := os.ReadFile(f.claudePath)
			if err != nil {
				t.Fatal(err)
			}
			args := append([]string{}, f.args...)
			if mode == "dry" {
				args = append(args, "--dry-run")
			}
			if mode == "execute" {
				args = append(args, "--yes")
			}
			called := false
			var out, stderr bytes.Buffer
			code := runUninstall(strings.NewReader("n\n"), &out, &stderr, args, func(context.Context, string) error { called = true; return nil })
			if code != 0 {
				t.Fatalf("code=%d error=%s", code, stderr.String())
			}
			after, _ := os.ReadFile(f.claudePath)
			if mode != "execute" && (called || !bytes.Equal(before, after)) {
				t.Fatal("확인 전 상태 변경")
			}
			if mode == "execute" && !called {
				t.Fatal("데몬 종료 단계 누락")
			}
			if strings.Contains(out.String(), "pit_secret") || strings.Contains(out.String(), "company-telemetry-token") {
				t.Fatal("비밀 출력")
			}
		})
	}
}
