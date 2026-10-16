package forward

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/otlpdecode"
)

// 이 패키지의 가장 중요한 불변식이다 (계획서 「테스트 전략」 §6).
//
// 성공 경로만 보면 아무것도 증명하지 못한다 — 토큰이 새는 곳은 실패·재시도·폐기 경로다.
// 그래서 한 버퍼에 모든 경로의 로그를 모아 놓고 한 번에 단언한다.
func TestTokenNeverAppearsInLogs(t *testing.T) {
	t.Parallel()

	const (
		staleToken = "TOKEN-AAA-must-never-be-logged"
		freshToken = "TOKEN-BBB-refreshed-secret"
	)
	buf := &syncBuffer{}
	payload := encodePayload(t, logsFixture(), otlpdecode.EncodingProtobuf)

	run := func(name string, o harnessOpts, body []byte, grace time.Duration) {
		o.logTo = buf
		o.privacy = blockAll()
		if o.tokens == nil {
			o.tokens = StaticToken(staleToken)
		}
		h := newHarness(t, o)
		if o.start {
			h.fwd.Enqueue(otlpdecode.PayloadLogs, otlpdecode.EncodingProtobuf, body)
		} else {
			// 워커가 없으니 큐를 넘치게 해 포화 경로를 태운다.
			for i := 0; i < 4; i++ {
				h.fwd.Enqueue(otlpdecode.PayloadLogs, otlpdecode.EncodingProtobuf, body)
			}
		}
		_ = h.shutdown(t, grace)
		t.Logf("경로 %q 통과", name)
	}

	// 1. 성공 경로
	run("2xx 성공", harnessOpts{start: true, maxAttempts: 1}, payload, 5*time.Second)

	// 2. 4xx 즉시 폐기
	run("4xx 폐기", harnessOpts{
		start: true, maxAttempts: 3,
		status: func(int) int { return http.StatusBadRequest },
	}, payload, 5*time.Second)

	// 3. 5xx 재시도 소진
	run("5xx 재시도 소진", harnessOpts{
		start: true, maxAttempts: 3,
		status: func(int) int { return http.StatusInternalServerError },
	}, payload, 5*time.Second)

	// 4. 401 → 토큰 갱신 → 재시도. 두 토큰 모두 로그에 없어야 한다.
	run("401 토큰 갱신", harnessOpts{
		start: true, maxAttempts: 3,
		tokens: &fakeTokens{tokens: []string{staleToken, freshToken}},
		status: func(int) int { return http.StatusUnauthorized },
	}, payload, 5*time.Second)

	// 5. Scrub 실패 폐기
	run("scrub 실패", harnessOpts{start: true, maxAttempts: 1},
		[]byte("OTLP 가 아닌 바이트"), 5*time.Second)

	// 6. 토큰 확보 실패
	run("토큰 확보 실패", harnessOpts{
		start: true, maxAttempts: 2,
		tokens: &fakeTokens{tokens: []string{staleToken}, err: errors.New("키링 잠김")},
	}, payload, 5*time.Second)

	// 7. 큐 포화 (워커 없음)
	run("큐 포화", harnessOpts{start: false, queueSize: 1}, payload, 2*time.Second)

	// 8. 전송 오류 — 죽은 주소
	deadUp := newUpstream(t, nil)
	deadURL := deadUp.srv.URL
	deadUp.srv.Close()
	run("전송 오류", harnessOpts{
		start: true, maxAttempts: 2, endpoint: deadURL,
	}, payload, 5*time.Second)

	// 9. 종료 제한 시간 초과 — 상위가 응답하지 않는 상태
	hang := newHarness(t, harnessOpts{
		logTo: buf, privacy: blockAll(), start: true, maxAttempts: 1, queueSize: 8,
		tokens: StaticToken(staleToken),
	})
	hang.up.block()
	for i := 0; i < 4; i++ {
		hang.fwd.Enqueue(otlpdecode.PayloadLogs, otlpdecode.EncodingProtobuf, payload)
	}
	waitFor(t, "워커가 상위로 요청을 보냄", func() bool { return hang.up.count() > 0 })
	if err := hang.shutdown(t, 100*time.Millisecond); err == nil {
		t.Fatal("종료 제한 시간이 초과되지 않았다 — 경로를 타지 못했다")
	}
	hang.up.release()

	logged := buf.String()
	if strings.TrimSpace(logged) == "" {
		t.Fatal("로그가 비었다 — 이 테스트는 아무것도 증명하지 못한다")
	}
	// 실패 경로가 실제로 로그를 남겼는지 확인한다. 없으면 단언이 공허해진다.
	for _, marker := range []string{"상위 거부", "전달 실패", "정리 실패", "큐 포화", "인증 거부", "token 확보 실패"} {
		if !strings.Contains(logged, marker) {
			t.Errorf("기대한 실패 로그 %q 가 없다 — 경로를 타지 못했을 수 있다", marker)
		}
	}
	for _, secret := range []string{staleToken, freshToken, "Bearer ", "Authorization"} {
		if strings.Contains(logged, secret) {
			t.Errorf("로그에 %q 가 들어 있다:\n%s", secret, logged)
		}
	}
}

// endpoint 에 userinfo 가 있으면 *url.Error 가 그것을 통째로 담아 온다.
// 전송 오류를 그대로 %v 로 찍으면 자격증명이 로그에 남는다.
func TestTransportErrorDoesNotLeakEndpointCredentials(t *testing.T) {
	t.Parallel()

	// 확실히 죽은 loopback 주소를 만든다. manifest 계약상 http 는 localhost 만 허용된다.
	up := newUpstream(t, nil)
	dead := up.srv.URL
	up.srv.Close()
	hostport := strings.TrimPrefix(dead, "http://")

	buf := &syncBuffer{}
	fwd, err := New(Options{
		Manifest:    testManifest("http://leaked-user:LEAKED-ENDPOINT-SECRET@"+hostport, blockAll()),
		Tokens:      StaticToken("TOKEN-CCC"),
		Logger:      log.New(buf, "", 0),
		MaxAttempts: 2,
		BaseBackoff: time.Millisecond,
		MaxBackoff:  2 * time.Millisecond,
		Timeout:     time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	fwd.Start()
	fwd.Enqueue(otlpdecode.PayloadLogs, otlpdecode.EncodingProtobuf, encodePayload(t, logsFixture(), otlpdecode.EncodingProtobuf))
	if err := fwd.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	logged := buf.String()
	if !strings.Contains(logged, "전송 오류") {
		t.Fatalf("전송 오류 경로를 타지 못했다:\n%s", logged)
	}
	for _, secret := range []string{"LEAKED-ENDPOINT-SECRET", "leaked-user", "TOKEN-CCC"} {
		if strings.Contains(logged, secret) {
			t.Errorf("로그에 %q 가 들어 있다:\n%s", secret, logged)
		}
	}
}

// sanitizeErr 는 *url.Error 를 벗겨 URL 을 버린다.
func TestSanitizeErr(t *testing.T) {
	t.Parallel()
	cause := errors.New("connection refused")
	wrapped := &url.Error{
		Op:  "Post",
		URL: "https://user:secret@collector.example.com/v1/logs",
		Err: cause,
	}
	got := sanitizeErr(wrapped)
	if strings.Contains(got.Error(), "secret") {
		t.Fatalf("URL 이 남아 있다: %v", got)
	}
	if !errors.Is(got, cause) {
		t.Fatalf("원인이 사라졌다: %v", got)
	}
	// url.Error 가 아니면 그대로 돌려준다.
	plain := errors.New("그냥 오류")
	if sanitizeErr(plain) != plain {
		t.Fatal("url.Error 가 아닌 오류가 변형됐다")
	}
	if sanitizeErr(nil) != nil {
		t.Fatal("nil 이 변형됐다")
	}
}
