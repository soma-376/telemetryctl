package forward

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/your-org/pulsemetry/internal/contract"
	"github.com/your-org/pulsemetry/internal/credential"
	"github.com/your-org/pulsemetry/internal/enrollment"
)

// defaultRefreshTimeout 은 telemetry token 재발급에 허용하는 시간이다.
//
// enrollment.RefreshTelemetryToken 은 http.DefaultClient 를 쓰고 타임아웃이 없다. 서버가
// 응답하지 않으면 워커가 영원히 잠기고 그 뒤 텔레메트리가 전부 큐에서 버려진다. 여기서 자른다.
const defaultRefreshTimeout = 10 * time.Second

// TokenSource 는 상위 전달의 Authorization 헤더에 쓸 telemetry token 공급자다.
//
// **토큰은 메모리에만 산다.** 어떤 구현도 디스크에 쓰지 않고, 로그·오류 메시지에도 담지 않는다.
type TokenSource interface {
	// Token 은 현재 유효하다고 믿는 토큰을 준다. 없으면 발급받는다.
	Token(ctx context.Context) (string, error)
	// Invalidate 는 상위가 401·403 으로 거부한 토큰을 캐시에서 버린다.
	// stale 이 현재 캐시와 다르면(다른 시도가 이미 갱신했으면) 아무 일도 하지 않는다.
	Invalidate(stale string)
}

// StaticToken 은 이미 손에 든 토큰을 그대로 쓰는 공급자다.
// 갱신 경로가 없으므로 Invalidate 는 아무 일도 하지 않는다.
func StaticToken(token string) TokenSource { return staticToken(token) }

type staticToken string

func (s staticToken) Token(context.Context) (string, error) {
	if s == "" {
		return "", errors.New("forward: telemetry token 이 비었음")
	}
	return string(s), nil
}

func (staticToken) Invalidate(string) {}

// telemetryTokenSource 는 키링의 설치 자격증명을 telemetry token 으로 교환해 캐시한다.
type telemetryTokenSource struct {
	serverURL string
	timeout   time.Duration

	// 두 훅은 테스트에서 키링·네트워크를 대신하기 위한 자리다. 운영에서는 항상
	// credential.LoadInstallation · enrollment.RefreshTelemetryToken 이다.
	load    func() (*credential.Credential, error)
	refresh func(serverURL, installationToken string) (*contract.TelemetryTokenResponse, error)

	mu    sync.Mutex
	token string
}

// NewTelemetryTokenSource 는 키링의 설치 자격증명으로 telemetry token 을 받아 오는 공급자다.
//
// 설치 자격증명은 교환할 때만 읽고 즉시 버린다. 발급받은 telemetry token 은 프로세스 메모리에만
// 두며 state.json 에도 로그에도 남기지 않는다 (§4.5).
func NewTelemetryTokenSource(serverURL string) (TokenSource, error) {
	if strings.TrimSpace(serverURL) == "" {
		return nil, errors.New("forward: telemetry token 공급자에 server_url 이 필요함")
	}
	return &telemetryTokenSource{
		serverURL: serverURL,
		timeout:   defaultRefreshTimeout,
		load:      credential.LoadInstallation,
		refresh:   enrollment.RefreshTelemetryToken,
	}, nil
}

func (s *telemetryTokenSource) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" {
		return s.token, nil
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	cred, err := s.load()
	if err != nil {
		return "", fmt.Errorf("forward: 설치 자격증명 조회 실패: %w", err)
	}
	if cred == nil || cred.InstallationToken == "" {
		return "", errors.New("forward: 설치 자격증명이 없음 — enroll 이 필요하다")
	}

	resp, err := s.exchange(ctx, cred.InstallationToken)
	if err != nil {
		// 재발급 오류에는 서버 응답 본문이 섞여 온다. 설치 토큰이 되비쳐 오는 경우까지 막는다.
		return "", fmt.Errorf("forward: telemetry token 재발급 실패: %w", stripSecret(err, cred.InstallationToken))
	}
	if resp == nil || resp.TelemetryToken == "" {
		return "", errors.New("forward: 재발급 응답에 telemetry_token 이 없음")
	}
	s.token = resp.TelemetryToken
	return s.token, nil
}

// exchange 는 재발급 호출에 시간 상한을 씌운다.
//
// RefreshTelemetryToken 은 ctx 를 받지 않으므로 고루틴으로 돌리고 결과를 기다린다. 시간이
// 다하면 결과를 버리고 돌아온다 — 버려진 고루틴은 자기 HTTP 호출이 끝나면 정리되고,
// 버퍼 채널로 보내므로 새지 않는다. 응답을 여기서만 읽으므로 경합도 없다.
func (s *telemetryTokenSource) exchange(ctx context.Context, installationToken string) (*contract.TelemetryTokenResponse, error) {
	type result struct {
		resp *contract.TelemetryTokenResponse
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := s.refresh(s.serverURL, installationToken)
		done <- result{resp: resp, err: err}
	}()

	timer := time.NewTimer(s.timeout)
	defer timer.Stop()
	select {
	case r := <-done:
		return r.resp, r.err
	case <-timer.C:
		return nil, fmt.Errorf("telemetry token 재발급이 %s 안에 끝나지 않음", s.timeout)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *telemetryTokenSource) Invalidate(stale string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if stale != "" && s.token == stale {
		s.token = ""
	}
}

// stripSecret 은 오류 메시지에서 비밀 문자열을 지운다.
//
// 우리가 만든 오류에는 토큰을 넣지 않지만, 남의 오류를 %w 로 감쌀 때는 안에 무엇이 들어 있는지
// 보장할 수 없다. RefreshTelemetryToken 은 실패 시 서버 응답 본문을 메시지에 담는다.
func stripSecret(err error, secret string) error {
	if err == nil || secret == "" {
		return err
	}
	msg := err.Error()
	if !strings.Contains(msg, secret) {
		return err
	}
	return errors.New(strings.ReplaceAll(msg, secret, "<redacted>"))
}
