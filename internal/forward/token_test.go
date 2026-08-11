package forward

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/contract"
	"github.com/your-org/pulsemetry/internal/credential"
)

func newTestTokenSource(
	load func() (*credential.Credential, error),
	refresh func(string, string) (*contract.TelemetryTokenResponse, error),
) *telemetryTokenSource {
	return &telemetryTokenSource{
		serverURL: "https://enroll.example.com",
		timeout:   time.Second,
		load:      load,
		refresh:   refresh,
	}
}

func fixedCredential() func() (*credential.Credential, error) {
	return func() (*credential.Credential, error) {
		return &credential.Credential{InstallationID: "inst-1", InstallationToken: "INSTALL-SECRET"}, nil
	}
}

func TestStaticToken(t *testing.T) {
	t.Parallel()
	src := StaticToken("abc")
	got, err := src.Token(context.Background())
	if err != nil || got != "abc" {
		t.Fatalf("Token = %q, %v", got, err)
	}
	src.Invalidate("abc") // 갱신 경로가 없으므로 아무 일도 없어야 한다.
	if got, _ := src.Token(context.Background()); got != "abc" {
		t.Fatalf("Invalidate 후 Token = %q, want abc", got)
	}
	if _, err := StaticToken("").Token(context.Background()); err == nil {
		t.Fatal("빈 토큰이 통과했다")
	}
}

// 토큰은 한 번만 받아 캐시하고, Invalidate 후에만 다시 받는다.
func TestTelemetryTokenSourceCachesUntilInvalidated(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	calls := 0
	src := newTestTokenSource(fixedCredential(), func(string, string) (*contract.TelemetryTokenResponse, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		return &contract.TelemetryTokenResponse{
			InstallationID: "inst-1",
			TelemetryToken: fmt.Sprintf("TELEMETRY-%d", calls),
		}, nil
	})

	for i := 0; i < 3; i++ {
		got, err := src.Token(context.Background())
		if err != nil {
			t.Fatalf("Token: %v", err)
		}
		if got != "TELEMETRY-1" {
			t.Fatalf("Token = %q, want TELEMETRY-1 (캐시되지 않았다)", got)
		}
	}

	// 다른 토큰을 무효화해도 캐시는 그대로다.
	src.Invalidate("어딘가의-다른-토큰")
	if got, _ := src.Token(context.Background()); got != "TELEMETRY-1" {
		t.Fatalf("남의 토큰 무효화로 캐시가 날아갔다: %q", got)
	}

	src.Invalidate("TELEMETRY-1")
	got, err := src.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if got != "TELEMETRY-2" {
		t.Fatalf("Invalidate 후 Token = %q, want TELEMETRY-2", got)
	}
}

func TestTelemetryTokenSourceMissingCredential(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		load func() (*credential.Credential, error)
	}{
		{"자격증명 없음", func() (*credential.Credential, error) { return nil, nil }},
		{"토큰이 빈 자격증명", func() (*credential.Credential, error) { return &credential.Credential{}, nil }},
		{"키링 오류", func() (*credential.Credential, error) { return nil, errors.New("키링 잠김") }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			src := newTestTokenSource(tc.load, func(string, string) (*contract.TelemetryTokenResponse, error) {
				t.Fatal("자격증명이 없는데 재발급을 호출했다")
				return nil, nil
			})
			if _, err := src.Token(context.Background()); err == nil {
				t.Fatal("오류가 나야 한다")
			}
		})
	}
}

func TestTelemetryTokenSourceRejectsEmptyResponse(t *testing.T) {
	t.Parallel()
	src := newTestTokenSource(fixedCredential(), func(string, string) (*contract.TelemetryTokenResponse, error) {
		return &contract.TelemetryTokenResponse{InstallationID: "inst-1"}, nil
	})
	if _, err := src.Token(context.Background()); err == nil {
		t.Fatal("빈 telemetry_token 이 통과했다")
	}
}

// 재발급 오류에는 서버 응답 본문이 섞여 온다. 설치 토큰이 되비쳐 와도 로그로 나가면 안 된다.
func TestTelemetryTokenSourceStripsSecretFromError(t *testing.T) {
	t.Parallel()
	src := newTestTokenSource(fixedCredential(), func(_, installToken string) (*contract.TelemetryTokenResponse, error) {
		return nil, fmt.Errorf("telemetry token 재발급 거부 (HTTP 401): {\"echo\":%q}", installToken)
	})
	_, err := src.Token(context.Background())
	if err == nil {
		t.Fatal("오류가 나야 한다")
	}
	if strings.Contains(err.Error(), "INSTALL-SECRET") {
		t.Fatalf("오류 메시지에 설치 토큰이 남아 있다: %v", err)
	}
	if !strings.Contains(err.Error(), "<redacted>") {
		t.Fatalf("가려진 흔적이 없다: %v", err)
	}
}

// RefreshTelemetryToken 은 ctx 를 받지 않는다. 서버가 응답하지 않아도 워커가 영원히
// 잠기지 않아야 한다.
func TestTelemetryTokenSourceRefreshTimeout(t *testing.T) {
	t.Parallel()
	stuck := make(chan struct{})
	t.Cleanup(func() { close(stuck) })

	src := newTestTokenSource(fixedCredential(), func(string, string) (*contract.TelemetryTokenResponse, error) {
		<-stuck
		return nil, errors.New("늦게 도착")
	})
	src.timeout = 50 * time.Millisecond

	done := make(chan error, 1)
	start := time.Now()
	go func() { _, err := src.Token(context.Background()); done <- err }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("시간 초과인데 성공했다")
		}
		if elapsed := time.Since(start); elapsed > 3*time.Second {
			t.Fatalf("%s 만에 돌아왔다 — 상한이 없다", elapsed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Token 이 무한정 잠겼다")
	}
}

func TestTelemetryTokenSourceHonorsContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	src := newTestTokenSource(fixedCredential(), func(string, string) (*contract.TelemetryTokenResponse, error) {
		t.Fatal("취소된 ctx 로 재발급을 호출했다")
		return nil, nil
	})
	if _, err := src.Token(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestNewTelemetryTokenSourceRequiresServerURL(t *testing.T) {
	t.Parallel()
	if _, err := NewTelemetryTokenSource("  "); err == nil {
		t.Fatal("빈 server_url 이 통과했다")
	}
	src, err := NewTelemetryTokenSource("https://enroll.example.com")
	if err != nil || src == nil {
		t.Fatalf("NewTelemetryTokenSource: %v", err)
	}
}

func TestStripSecret(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		err    error
		secret string
		want   string
	}{
		{"nil 오류", nil, "s", ""},
		{"빈 비밀", errors.New("abc"), "", "abc"},
		{"비밀 없음", errors.New("abc"), "xyz", "abc"},
		{"비밀 제거", errors.New("token=SECRET 실패"), "SECRET", "token=<redacted> 실패"},
		{"여러 번 등장", errors.New("S S S"), "S", "<redacted> <redacted> <redacted>"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := stripSecret(tc.err, tc.secret)
			if tc.err == nil {
				if got != nil {
					t.Fatalf("stripSecret(nil) = %v", got)
				}
				return
			}
			if got.Error() != tc.want {
				t.Fatalf("stripSecret = %q, want %q", got.Error(), tc.want)
			}
		})
	}
}
