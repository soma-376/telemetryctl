package forward

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/otlpdecode"
)

func TestDeliverySnapshotCountsPendingAndRecordsSuccessTime(t *testing.T) {
	h := newHarness(t, harnessOpts{queueSize: 1})
	now := time.Date(2026, 10, 29, 1, 2, 3, 0, time.UTC)
	h.fwd.now = func() time.Time { return now }
	if !h.fwd.Enqueue(otlpdecode.PayloadLogs, otlpdecode.EncodingJSON, []byte(`{}`)) {
		t.Fatal("첫 페이로드 접수 실패")
	}
	if h.fwd.Enqueue(otlpdecode.PayloadLogs, otlpdecode.EncodingJSON, []byte(`{}`)) {
		t.Fatal("포화 큐에 추가됨")
	}
	s := h.fwd.Stats()
	if s.Pending != 1 || s.DroppedQueueFull != 1 || s.Sent != 0 || !s.LastSentAt.IsZero() {
		t.Fatalf("대기 상태 = %+v", s)
	}
	h.fwd.Start()
	if err := h.shutdown(t, time.Second); err != nil {
		t.Fatal(err)
	}
	s = h.fwd.Stats()
	if s.Pending != 0 || s.Sent != 1 || !s.LastSentAt.Equal(now) || s.DroppedQueueFull != 1 {
		t.Fatalf("완료 상태 = %+v", s)
	}
}

func TestPendingIncludesInFlightRequest(t *testing.T) {
	entered, release := make(chan struct{}, 2), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		entered <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	f, err := New(Options{Manifest: testManifest(srv.URL, blockAll()), Tokens: StaticToken("t"), Logger: log.New(io.Discard, "", 0)})
	if err != nil {
		t.Fatal(err)
	}
	f.Start()
	defer func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_ = f.Shutdown(ctx)
	}()
	f.Enqueue(otlpdecode.PayloadLogs, otlpdecode.EncodingJSON, []byte(`{}`))
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("첫 요청이 시작되지 않음")
	}
	f.Enqueue(otlpdecode.PayloadLogs, otlpdecode.EncodingJSON, []byte(`{}`))
	if s := f.Stats(); s.Pending != 2 || s.Sent != 0 {
		t.Fatalf("전송 중 1건·큐 1건을 모두 세지 않음: %+v", s)
	}
}

func TestFinalFailureClearsPendingWithoutSuccessTime(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"discard", 400, `{}`},
		{"exhausted retries", 503, `{}`},
		{"scrub failure", 200, `invalid`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, harnessOpts{status: func(int) int { return tc.status }, maxAttempts: 2, start: true})
			h.fwd.Enqueue(otlpdecode.PayloadLogs, otlpdecode.EncodingJSON, []byte(tc.body))
			if err := h.shutdown(t, time.Second); err != nil {
				t.Fatal(err)
			}
			s := h.fwd.Stats()
			if s.Pending != 0 || s.Sent != 0 || !s.LastSentAt.IsZero() || s.Discarded+s.Failed+s.DroppedScrub != 1 {
				t.Fatalf("실패 후 상태 = %+v", s)
			}
		})
	}
}

func TestShutdownDuringRetryClearsPending(t *testing.T) {
	h := newHarness(t, harnessOpts{status: func(int) int { return 503 }, maxAttempts: 3})
	h.fwd.baseBackoff, h.fwd.maxBackoff = time.Hour, time.Hour
	h.fwd.Start()
	h.fwd.Enqueue(otlpdecode.PayloadLogs, otlpdecode.EncodingJSON, []byte(`{}`))
	deadline := time.Now().Add(time.Second)
	for h.fwd.Stats().Retries == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if h.fwd.Stats().Retries != 1 {
		t.Fatal("재시도 대기에 진입하지 않음")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := h.fwd.Shutdown(ctx); err == nil {
		t.Fatal("종료 제한을 보고하지 않음")
	}
	if s := h.fwd.Stats(); s.Pending != 0 || s.DroppedShutdown != 1 {
		t.Fatalf("재시도 중 취소된 배치가 대기로 남음: %+v", s)
	}
}
