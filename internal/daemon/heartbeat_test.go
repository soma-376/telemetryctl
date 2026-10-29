package daemon

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"regexp"
	"runtime"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/your-org/pulsemetry/internal/heartbeat"
	"github.com/your-org/pulsemetry/internal/installer"
)

type heartbeatReporterFunc func(context.Context, heartbeat.Report) error

func (f heartbeatReporterFunc) Send(ctx context.Context, report heartbeat.Report) (heartbeat.Response, error) {
	return heartbeat.Response{}, f(ctx, report)
}

func TestHeartbeatStartsImmediatelyAndReportsEveryMinuteAfterFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		received := make(chan heartbeat.Report, 8)
		opts, err := (Options{StatePath: "unused", Logger: log.New(io.Discard, "", 0)}).normalized()
		if err != nil {
			t.Fatal(err)
		}
		opts.HeartbeatReporter = heartbeatReporterFunc(func(_ context.Context, report heartbeat.Report) error {
			received <- report
			return errors.New("server unavailable")
		})
		d := &daemon{opts: opts, log: opts.Logger, state: &installer.State{ServerURL: "http://example.test", ConfigRevision: 3}}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		d.startHeartbeat(ctx)
		synctest.Wait()
		if len(received) != 1 {
			t.Fatalf("즉시 보고 = %d회", len(received))
		}
		reports := []heartbeat.Report{<-received}
		for n := 2; n <= 3; n++ {
			time.Sleep(59 * time.Second)
			synctest.Wait()
			if len(received) != 0 {
				t.Fatal("1분이 되기 전에 보고함")
			}
			time.Sleep(time.Second)
			synctest.Wait()
			if len(received) != 1 {
				t.Fatal("1분 주기 보고가 오지 않음")
			}
			reports = append(reports, <-received)
			if reports[n-1].SentAt.Sub(reports[n-2].SentAt) != time.Minute {
				t.Fatalf("실패 후 1분 주기 보고가 유지되지 않음: %#v", reports)
			}
		}
		if !regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`).MatchString(reports[0].Daemon.RunID) || reports[0].Daemon.RunID != reports[2].Daemon.RunID {
			t.Fatal("프로세스 식별자가 잘못됐거나 보고 사이에 바뀜")
		}
		cancel()
		synctest.Wait()
		<-d.heartbeatDone
		time.Sleep(time.Minute)
		if len(received) != 0 {
			t.Fatal("종료 후에도 보고함")
		}
		ctx, cancel = context.WithCancel(context.Background())
		defer cancel()
		d.startHeartbeat(ctx)
		synctest.Wait()
		if reports[0].Daemon.RunID == (<-received).Daemon.RunID {
			t.Fatal("새 실행에 이전 run_id를 재사용함")
		}
		cancel()
		synctest.Wait()
	})
}

func TestHeartbeatRequestHasTenSecondDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		requestErrors := make(chan error, 2)
		logger := log.New(io.Discard, "", 0)
		d := &daemon{state: &installer.State{ServerURL: "http://example.test"}, log: logger,
			opts: Options{Now: time.Now, HeartbeatInterval: time.Minute, HeartbeatReporter: heartbeatReporterFunc(func(ctx context.Context, _ heartbeat.Report) error {
				calls.Add(1)
				<-ctx.Done()
				requestErrors <- ctx.Err()
				return ctx.Err()
			})}}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		d.startHeartbeat(ctx)
		synctest.Wait()
		time.Sleep(10 * time.Second)
		synctest.Wait()
		requestErr := <-requestErrors
		if calls.Load() != 1 || !errors.Is(requestErr, context.DeadlineExceeded) {
			t.Fatalf("요청 제한 = %v, 호출 = %d", requestErr, calls.Load())
		}
		time.Sleep(50 * time.Second)
		synctest.Wait()
		if calls.Load() != 2 {
			t.Fatal("시간 초과 뒤 다음 주기에 재시도하지 않음")
		}
		cancel()
		synctest.Wait()
	})
}

func isolateHeartbeatTest(t *testing.T, o *Options) {
	t.Helper()
	o.VendorLimitCollector = screenLimitCollector{}
	o.CodexThreadReader = &titleReaderStub{}
	o.CodexJSONLRoot = t.TempDir()
}

func receiveHeartbeat(t *testing.T, reports <-chan heartbeat.Report) heartbeat.Report {
	t.Helper()
	select {
	case report := <-reports:
		return report
	case <-time.After(5 * time.Second):
		t.Fatal("설치 상태 보고가 오지 않음")
		return heartbeat.Report{}
	}
}

func TestHeartbeatReportsActualCollectionAndDelivery(t *testing.T) {
	reports := make(chan heartbeat.Report, 64)
	h := start(t, harnessOptions{
		state: func(st *installer.State) { st.Local.Enabled = true },
		daemon: func(o *Options) {
			isolateHeartbeatTest(t, o)
			o.HeartbeatInterval = 20 * time.Millisecond
			o.HeartbeatReporter = heartbeatReporterFunc(func(ctx context.Context, report heartbeat.Report) error {
				select {
				case reports <- report:
				case <-ctx.Done():
				}
				return nil
			})
		},
	})
	first := receiveHeartbeat(t, reports)
	c := first.Collection
	if c.Mode != "local" || !c.Forwarding || c.ReceivingSince == nil || c.Delivered != 0 || c.Pending != 0 || c.Lost != 0 || c.LastDeliveredAt != nil {
		t.Fatalf("첫 상태 = %+v", c)
	}
	if first.AppliedConfigRevision != 7 || first.Daemon.Version != installer.Version || first.Daemon.Platform != runtime.GOOS || first.Daemon.Architecture != runtime.GOARCH {
		t.Fatalf("설치 정보 = %+v", first)
	}
	h.postFixture("logs_session_walkthrough.json")
	for n := 0; n < 100; n++ {
		r := receiveHeartbeat(t, reports)
		if r.Collection.Delivered == 0 {
			continue
		}
		c = r.Collection
		if c.Delivered != 1 || c.Pending != 0 || c.Lost != 0 || c.LastDeliveredAt == nil || c.LastDeliveredAt.After(r.SentAt) || !c.ReceivingSince.Equal(*first.Collection.ReceivingSince) {
			t.Fatalf("전달 후 상태 = %+v", c)
		}
		return
	}
	t.Fatal("전달 성공이 보고에 반영되지 않음")
}

func TestBlockedHeartbeatDoesNotBlockCollectionForwardingOrShutdown(t *testing.T) {
	var calls atomic.Int32
	entered, ended := make(chan struct{}), make(chan struct{})
	h := start(t, harnessOptions{
		state: func(st *installer.State) { st.Local.Enabled = true },
		daemon: func(o *Options) {
			isolateHeartbeatTest(t, o)
			o.HeartbeatInterval = 5 * time.Millisecond
			o.HeartbeatReporter = heartbeatReporterFunc(func(ctx context.Context, _ heartbeat.Report) error {
				if calls.Add(1) == 1 {
					close(entered)
				}
				<-ctx.Done()
				close(ended)
				return ctx.Err()
			})
		},
	})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("보고가 시작되지 않음")
	}
	h.postFixture("logs_session_walkthrough.json")
	waitFor(t, "보고가 멈춘 동안 상위 전달", func() bool { return len(h.upstream.received()) == 1 })
	if calls.Load() != 1 {
		t.Fatal("진행 중인 보고와 겹치는 요청을 시작함")
	}
	if elapsed := h.stop(); elapsed > 2*time.Second {
		t.Fatalf("보고가 종료를 지연시킴: %s", elapsed)
	}
	select {
	case <-ended:
	default:
		t.Fatal("종료가 보고 요청을 취소하지 않음")
	}
	if n := countRows(t, h.openDB(), "SELECT COUNT(*) FROM events"); n == 0 {
		t.Fatal("보고가 멈춘 동안 수집한 사용량이 저장되지 않음")
	}
}

func TestHeartbeatDoesNotClaimDisabledOrDirectCollection(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		local, noReceiver, noForward bool
	}{
		{name: "direct"},
		{name: "no receiver", local: true, noReceiver: true},
		{name: "no forward", local: true, noForward: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reports := make(chan heartbeat.Report, 1)
			h := start(t, harnessOptions{
				state: func(st *installer.State) { st.Local.Enabled = tc.local },
				daemon: func(o *Options) {
					isolateHeartbeatTest(t, o)
					o.DisableReceiver, o.DisableForward = tc.noReceiver, tc.noForward
					o.HeartbeatReporter = heartbeatReporterFunc(func(_ context.Context, r heartbeat.Report) error { reports <- r; return nil })
				},
			})
			defer h.stop()
			c := receiveHeartbeat(t, reports).Collection
			if (c.Mode == "local") != tc.local || c.Forwarding != !tc.noForward || (c.ReceivingSince == nil) != tc.noReceiver {
				t.Fatalf("실행 상태와 보고가 다름: %+v", c)
			}
		})
	}
}

func TestHeartbeatReportsLossWithoutCountingDisabledSignals(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()
	reports := make(chan heartbeat.Report, 64)
	h := start(t, harnessOptions{
		state: func(st *installer.State) {
			st.Local.Enabled = true
			st.Manifest.OTLP.Endpoint = srv.URL
			st.Manifest.Signals.Metrics = false
		},
		daemon: func(o *Options) {
			isolateHeartbeatTest(t, o)
			o.HeartbeatInterval = 20 * time.Millisecond
			o.HeartbeatReporter = heartbeatReporterFunc(func(ctx context.Context, r heartbeat.Report) error {
				select {
				case reports <- r:
				case <-ctx.Done():
				}
				return nil
			})
		},
	})
	receiveHeartbeat(t, reports)
	h.post("/v1/logs", []byte(`{}`))    // 전달 후 400으로 폐기한다.
	h.post("/v1/metrics", []byte(`{}`)) // 회사가 끈 시그널이다.
	h.post("/v1/logs", []byte(`{"resourceLogs":"invalid"}`))
	h.post("/v1/metrics", []byte(`{"resourceMetrics":"invalid"}`))
	waitFor(t, "수신기 디코드 완료", func() bool {
		s := h.health().Stats
		return s.Decoded == 2 && s.DecodeErrors == 2
	})
	for n := 0; n < 100; n++ {
		c := receiveHeartbeat(t, reports).Collection
		if c.Lost < 2 {
			continue
		}
		if c.Lost != 2 || c.Delivered != 0 || c.Pending != 0 || c.LastDeliveredAt != nil || calls.Load() != 1 {
			t.Fatalf("전달 폐기 1건·logs 디코드 손실 1건만 보고해야 함: %+v, 전송 %d회", c, calls.Load())
		}
		return
	}
	t.Fatal("실제 손실이 보고되지 않음")
}
