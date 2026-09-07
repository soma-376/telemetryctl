package codexapp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestClient초기화하고프로세스를재사용한다(t *testing.T) {
	client := New(Options{Command: helperCommand("normal")})
	t.Cleanup(func() { _ = client.Close() })

	first, err := client.RateLimits(context.Background())
	if err != nil {
		t.Fatalf("첫 조회: %v", err)
	}
	second, err := client.RateLimits(context.Background())
	if err != nil {
		t.Fatalf("두 번째 조회: %v", err)
	}
	if first.Primary == nil || first.Primary.UsedPercent != 10 {
		t.Fatalf("첫 응답 = %+v", first)
	}
	if second.Primary == nil || second.Primary.UsedPercent != 20 {
		t.Fatalf("프로세스가 재사용되지 않았다: %+v", second)
	}
}

func TestClient는컨텍스트취소를따른다(t *testing.T) {
	client := New(Options{Command: helperCommand("hang")})
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := client.RateLimits(ctx)
	if err == nil || ctx.Err() == nil {
		t.Fatalf("취소 오류 = %v", err)
	}
}

func TestClient는깨진응답원문을오류에싣지않는다(t *testing.T) {
	client := New(Options{Command: helperCommand("malformed")})
	t.Cleanup(func() { _ = client.Close() })
	_, err := client.RateLimits(context.Background())
	if err == nil {
		t.Fatal("실패해야 한다")
	}
	if got := err.Error(); got == "" || strings.Contains(got, "SECRET-CANARY") {
		t.Fatalf("오류 문자열 = %q", got)
	}
}

func TestClient는실행파일부재를분류한다(t *testing.T) {
	client := New(Options{Command: []string{"pulsemetry-codex-does-not-exist"}})
	_, err := client.RateLimits(context.Background())
	if err == nil {
		t.Fatal("실행 파일이 없는데 성공했다")
	}
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("오류 = %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("시작 실패 뒤 Close = %v", err)
	}
}

func helperCommand(mode string) []string {
	return []string{os.Args[0], "-test.run=TestCodexAppHelperProcess", "--", mode}
}

func TestCodexAppHelperProcess(t *testing.T) {
	if len(os.Args) < 2 {
		return
	}
	mode := os.Args[len(os.Args)-1]
	if mode != "normal" && mode != "hang" && mode != "malformed" {
		return
	}
	s := bufio.NewScanner(os.Stdin)
	requests := 0
	for s.Scan() {
		var req struct {
			ID     *int64 `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal(s.Bytes(), &req)
		if req.ID == nil {
			continue
		}
		switch req.Method {
		case "initialize":
			fmt.Printf("{\"id\":%d,\"result\":{}}\n", *req.ID)
		case "account/rateLimits/read":
			if mode == "hang" {
				for {
					time.Sleep(time.Hour)
				}
			}
			if mode == "malformed" {
				fmt.Println(`{"id":"SECRET-CANARY"`)
				continue
			}
			requests++
			fmt.Printf("{\"id\":%d,\"result\":{\"rateLimits\":{\"planType\":\"plus\",\"primary\":{\"usedPercent\":%d,\"windowDurationMins\":300,\"resetsAt\":2000000000}}}}\n", *req.ID, requests*10)
		}
	}
	os.Exit(0)
}
