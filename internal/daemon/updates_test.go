package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/installer"
	"github.com/your-org/pulsemetry/internal/receiver"
	"github.com/your-org/pulsemetry/internal/runtimeinfo"
	"github.com/your-org/pulsemetry/internal/updatecheck"
)

type updateCheckerFunc func(context.Context) (updatecheck.Result, error)

func (f updateCheckerFunc) Check(ctx context.Context) (updatecheck.Result, error) { return f(ctx) }

// 업데이트 테스트는 실제 벤더 자격증명이나 App Server에 연결하지 않는다.
func isolateUpdateTest(o *Options) {
	o.VendorLimitCollector = screenLimitCollector{}
	o.CodexThreadReader = &titleReaderStub{}
	o.DisableForward = true
}

func readUpdateSnapshot(t *testing.T, h *harness) updatecheck.Snapshot {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, h.info.Endpoint+"/v1/updates", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testIngestToken)
	req.Header.Set(receiver.LocalHeader, receiver.LocalHeaderValue)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("상태 조회 HTTP %d", resp.StatusCode)
	}
	var snap updatecheck.Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		t.Fatal(err)
	}
	return snap
}

func TestUpdateChecksUseEnrollmentServerAndExposeCache(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		q := r.URL.Query()
		if r.URL.Path != "/base/api/v1/check-updates" || q.Get("current_version") != installer.Version || q.Get("platform") != runtime.GOOS || q.Get("architecture") != runtime.GOARCH {
			t.Errorf("업데이트 요청 경로나 실행 버전이 다름: %s", r.URL.String())
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("회사 업데이트 조회에 인증이 붙음")
		}
		_, _ = fmt.Fprint(w, `{"latest_version":"2.0.0","update_available":true}`)
	}))
	defer srv.Close()
	h := start(t, harnessOptions{
		state: func(st *installer.State) {
			st.ServerURL = srv.URL + "/base/"
			st.InstallerVersion = "old-installed-version"
		},
		daemon: func(o *Options) {
			isolateUpdateTest(o)
			o.UpdateChecker = nil
		},
	})
	waitFor(t, "기동 직후 업데이트 결과", func() bool { return readUpdateSnapshot(t, h).Status == updatecheck.StatusReady })
	for range 3 {
		got := readUpdateSnapshot(t, h)
		if got.CurrentVersion != installer.Version || got.LatestVersion != "2.0.0" || got.UpdateAvailable == nil || !*got.UpdateAvailable || got.LastSuccessAt == "" {
			t.Fatalf("로컬 상태=%+v", got)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("캐시 조회가 외부 서버를 호출함: %d회", got)
	}
}

func TestMissingServerDisablesUpdateChecks(t *testing.T) {
	var calls atomic.Int32
	h := start(t, harnessOptions{
		state: func(st *installer.State) { st.ServerURL = "" },
		daemon: func(o *Options) {
			isolateUpdateTest(o)
			o.UpdateInterval = time.Millisecond
			o.UpdateChecker = updateCheckerFunc(func(context.Context) (updatecheck.Result, error) {
				calls.Add(1)
				return updatecheck.Result{}, nil
			})
		},
	})
	got := readUpdateSnapshot(t, h)
	if got.Status != updatecheck.StatusDisabled || got.UpdateAvailable != nil || got.LastAttemptAt != "" {
		t.Fatalf("서버 없는 상태=%+v", got)
	}
	h.stop()
	if calls.Load() != 0 {
		t.Fatal("서버가 없는데 업데이트 조회를 실행함")
	}
}

func TestUpdateWorkerChecksPeriodicallyWithoutOverlap(t *testing.T) {
	var calls atomic.Int32
	first, release, second := make(chan struct{}), make(chan struct{}), make(chan struct{})
	checker := updateCheckerFunc(func(ctx context.Context) (updatecheck.Result, error) {
		switch calls.Add(1) {
		case 1:
			close(first)
			select {
			case <-release:
			case <-ctx.Done():
				return updatecheck.Result{}, ctx.Err()
			}
		case 2:
			close(second)
		}
		return updatecheck.Result{LatestVersion: "2.0.0"}, nil
	})
	d := &daemon{opts: Options{UpdateInterval: 10 * time.Millisecond}, log: log.New(io.Discard, "", 0),
		updates: updatecheck.NewService("1.0.0", true, checker, nil)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.startUpdates(ctx)
	select {
	case <-first:
	case <-time.After(time.Second):
		t.Fatal("기동 직후 조회가 없음")
	}
	time.Sleep(40 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatal("첫 조회 중 주기 조회가 겹쳤다")
	}
	close(release)
	select {
	case <-second:
	case <-time.After(time.Second):
		t.Fatal("다음 주기 조회가 없음")
	}
	cancel()
	d.waitUpdates(time.Now().Add(time.Second))
}

func TestUpdateCancellationKeepsShutdownFlush(t *testing.T) {
	entered, canceled := make(chan struct{}), make(chan struct{})
	h := start(t, harnessOptions{daemon: func(o *Options) {
		isolateUpdateTest(o)
		o.FlushInterval, o.Interval = time.Hour, time.Hour
		o.BatchEvents = 100_000
		o.ShutdownTimeout = 2 * time.Second
		o.UpdateChecker = updateCheckerFunc(func(ctx context.Context) (updatecheck.Result, error) {
			close(entered)
			<-ctx.Done()
			close(canceled)
			return updatecheck.Result{}, ctx.Err()
		})
	}})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("업데이트 조회가 시작되지 않음")
	}
	h.postFixture("logs_session_walkthrough.json")
	h.waitDecoded(1)
	if took := h.stop(); took > 2*time.Second {
		t.Fatalf("업데이트 조회 때문에 종료 예산 초과: %s", took)
	}
	select {
	case <-canceled:
	default:
		t.Fatal("종료가 업데이트 조회를 취소하지 않음")
	}
	if _, err := os.Stat(runtimeinfo.PathIn(h.dataDir)); !os.IsNotExist(err) {
		t.Fatal("종료 후 runtime.json이 남음")
	}
	if n := countRows(t, h.openDB(), `SELECT COUNT(*) FROM events`); n == 0 {
		t.Fatal("진행 중 업데이트 조회 때문에 종료 flush가 누락됨")
	}
}

func TestUpdateWorkerJoinHonorsShutdownBudget(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	d := &daemon{opts: Options{UpdateInterval: time.Hour}, log: log.New(io.Discard, "", 0),
		updates: updatecheck.NewService("1.0.0", true, updateCheckerFunc(func(context.Context) (updatecheck.Result, error) {
			close(entered)
			<-release
			return updatecheck.Result{LatestVersion: "1.0.0"}, nil
		}), nil)}
	ctx, cancel := context.WithCancel(context.Background())
	d.startUpdates(ctx)
	<-entered
	cancel()
	started := time.Now()
	d.waitUpdates(started.Add(20 * time.Millisecond))
	if time.Since(started) > 200*time.Millisecond {
		t.Error("응답 없는 워커 때문에 종료 예산을 넘겨 기다림")
	}
	close(release)
	<-d.updatesDone
}
