package dashboard

// 데몬 쓰기와 GUI read-only 조회의 동시 실행 (PROJ-97, ADR 0004·0009 인수조건).
//
// concurrent_test.go 는 **Reader** 를 상대로 같은 성질을 본다. 여기는 GUI 가 실제로 쓰는
// 표면인 **Service** 다. 둘은 같지 않다 — Service 는 조회마다 reconnect 를 부르고
// (service.go), 트레이 모니터라는 **가변 상태** 를 하나 들고 있으며, 그 상태는 여러
// 화면이 동시에 만진다. Reader 만 검증하면 그 층은 -race 아래를 지나간 적이 없다.
//
// 두 시나리오다.
//
//  1. DB 가 이미 있고 데몬이 계속 쓰는 동안 모든 화면을 동시에 조회한다.
//  2. **DB 가 아직 없는 상태에서 서비스를 띄우고**, 조회가 도는 중에 데몬이 DB 를 만든다.
//     GUI 를 먼저 켜고 나중에 `local enable` 을 하는 정상 시나리오이고, reconnect 가
//     조회 고루틴들과 겹치는 유일한 자리다.

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/event"
	"github.com/your-org/pulsemetry/internal/session"
	"github.com/your-org/pulsemetry/internal/store"
	"github.com/your-org/pulsemetry/internal/vendorlimit"
)

// daemonWriter 는 데몬의 쓰기 루프를 흉내 낸다. rounds 번 쓰고 done 을 닫는다.
func daemonWriter(t *testing.T, db *store.DB, rounds int, done chan<- struct{}) func() {
	return func() {
		defer close(done)
		ctx := context.Background()
		for i := 1; i <= rounds; i++ {
			at := testNow.Add(-time.Duration(i) * time.Minute)
			key := fmt.Sprintf("cw-%02d", i%8)
			turn := fmt.Sprintf("%s-t%03d", key, i)
			batch := store.Batch{
				Sessions: []session.Session{newSession(key, at)},
				Events: []store.EventRecord{
					promptRecord(key, turn, at, i, "인증 토큰 검증 프록시"),
					llmRecord(key, turn, at, 1000+i, llmSpec{
						Model: "claude-sonnet-4-5", Cost: 0.05, Input: 20, Output: 8,
					}),
					toolRecord(key, turn, fmt.Sprintf("cw-call-%04d", i), at, 2000+i, toolSpec{
						ToolName: "Edit", MCPServer: "github", Success: event.Some(true),
						Target: workspaceA + "/apply.go",
						File:   fileChange(workspaceA+"/apply.go", 1, 0),
					}),
				},
			}
			if _, err := db.Write(ctx, batch); err != nil {
				t.Errorf("데몬 쓰기 %d: %v", i, err)
				return
			}
		}
	}
}

// serviceReads 는 GUI 화면 하나하나에 대응하는 조회다.
//
// 세션 id 를 인자로 받지 않는 것이 의도다 — 쓰기가 도는 중이라 id 는 계속 늘고, 화면도
// 그때그때 목록에서 얻은 id 를 쓴다. 목록 → 상세의 순서를 그대로 흉내 내야 "id 를 얻는
// 사이에 그 세션이 사라진다" 는 실제 경합이 테스트에 들어온다.
func serviceReads(svc *Service) []struct {
	name string
	call func(context.Context) error
} {
	firstID := func(ctx context.Context) (int64, bool, error) {
		rows, err := svc.Sessions(ctx, SessionQuery{Limit: 1})
		if err != nil || len(rows) == 0 {
			return 0, false, err
		}
		return rows[0].ID, true, nil
	}

	return []struct {
		name string
		call func(context.Context) error
	}{
		{"Home", func(ctx context.Context) error { _, err := svc.Home(ctx, HomeQuery{TZ: seoul}); return err }},
		{"HomeBreakdown", func(ctx context.Context) error {
			_, err := svc.HomeBreakdown(ctx, HomeBreakdownQuery{TZ: seoul})
			return err
		}},
		{"Today", func(ctx context.Context) error { _, err := svc.Today(ctx, seoul); return err }},
		{"Activity", func(ctx context.Context) error {
			_, err := svc.Activity(ctx, ActivityQuery{Limit: 20, Text: "인증"})
			return err
		}},
		{"Sessions", func(ctx context.Context) error {
			_, err := svc.Sessions(ctx, SessionQuery{Limit: 20})
			return err
		}},
		{"SessionDetail", func(ctx context.Context) error {
			id, ok, err := firstID(ctx)
			if err != nil || !ok {
				return err
			}
			_, err = svc.Session(ctx, id)
			return err
		}},
		{"SessionMetrics", func(ctx context.Context) error {
			id, ok, err := firstID(ctx)
			if err != nil || !ok {
				return err
			}
			_, err = svc.SessionMetrics(ctx, SessionMetricsQuery{SessionID: id})
			return err
		}},
		{"FileChanges", func(ctx context.Context) error {
			id, ok, err := firstID(ctx)
			if err != nil || !ok {
				return err
			}
			_, err = svc.FileChanges(ctx, id)
			return err
		}},
		{"WorkspaceFolder", func(ctx context.Context) error {
			id, ok, err := firstID(ctx)
			if err != nil || !ok {
				return err
			}
			_, err = svc.WorkspaceFolder(ctx, id)
			return err
		}},
		{"Classifier", func(ctx context.Context) error {
			id, ok, err := firstID(ctx)
			if err != nil || !ok {
				return err
			}
			_, err = NewClassifier(svc.Reader()).Session(ctx, id)
			return err
		}},
		{"Breakdown", func(ctx context.Context) error {
			_, err := svc.Breakdown(ctx, BreakdownQuery{Dim: DimProject, TZ: seoul})
			return err
		}},
		{"Search", func(ctx context.Context) error {
			_, err := svc.Search(ctx, SearchQuery{Text: "인증 토큰"})
			return err
		}},
		{"Vendors", func(ctx context.Context) error { _, err := svc.Vendors(ctx); return err }},
		{"MCPUsage", func(ctx context.Context) error { _, err := svc.MCPUsage(ctx, 14); return err }},
		{"Status", func(ctx context.Context) error { _, err := svc.Status(ctx); return err }},
		{"Tray", func(ctx context.Context) error { _, err := svc.Tray(ctx, TrayQuery{TZ: seoul}); return err }},
		{"RefreshTray", func(ctx context.Context) error {
			_, err := svc.RefreshTray(ctx, TrayQuery{TZ: seoul})
			return err
		}},
	}
}

// stubTrayCollector 는 서비스의 트레이 모니터가 네트워크에 닿지 않게 한다.
//
// 벤더 한도 조회는 남의 API 다. -race 테스트가 그것을 두드리면 CI 는 네트워크에 묶이고,
// 개발자 기계에서는 실제 계정 한도를 조회하게 된다.
func stubTrayCollector(svc *Service) {
	snap := vendorlimit.Snapshot{
		Results: []vendorlimit.Result{
			availableResult(vendorlimit.VendorClaudeCode,
				window(vendorlimit.PeriodFiveHour, "primary", 0.4, 3600)),
		},
		ObservedAt: "2026-08-10T02:00:00Z",
	}
	svc.tray.collect = func(context.Context) vendorlimit.Snapshot { return snap }
	// 주기를 0 으로 두어 호출마다 실제로 다시 만들게 한다 — 캐시가 걸리면 갱신 경로가
	// 한 번만 돌고 그 자리의 경합은 검사되지 않는다.
	svc.tray.interval = 0
}

// TestConcurrentServiceQueriesWhileDaemonWrites 는 데몬이 쓰는 동안 GUI 의 모든 화면을
// 동시에 조회한다. -race 로 돌 때 경쟁이 검출되지 않아야 한다 (ADR 0009 인수조건).
func TestConcurrentServiceQueriesWhileDaemonWrites(t *testing.T) {
	f := newFixture(t)
	// 조회가 빈 DB 를 도는 것을 막는 초기 데이터.
	f.write(store.Batch{
		Sessions: []session.Session{newSession("cw-seed", testNow.Add(-time.Hour), workspace(f.dir))},
		Events: []store.EventRecord{
			promptRecord("cw-seed", "cw-seed-t1", testNow.Add(-time.Hour), 1, "인증 토큰 검증"),
		},
	})

	svc := NewService(f.path)
	if err := svc.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { svc.Stop() }) //nolint:errcheck // 테스트 정리
	svc.Reader().now = func() time.Time { return testNow }
	stubTrayCollector(svc)

	const rounds = 30
	writeDone := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		daemonWriter(t, f.db, rounds, writeDone)()
	}()

	var reads atomic.Int64
	for _, read := range serviceReads(svc) {
		wg.Add(1)
		go func(name string, call func(context.Context) error) {
			defer wg.Done()
			ctx := context.Background()
			for {
				if err := call(ctx); err != nil {
					t.Errorf("쓰기 중 %s 조회 실패: %v", name, err)
					return
				}
				reads.Add(1)
				select {
				case <-writeDone:
					// 쓰기가 끝난 뒤 한 번 더 돌아 최종 상태를 읽는다.
					if err := call(ctx); err != nil {
						t.Errorf("쓰기 후 %s 조회 실패: %v", name, err)
					}
					return
				default:
				}
			}
		}(read.name, read.call)
	}
	wg.Wait()

	if reads.Load() < int64(len(serviceReads(svc))) {
		t.Fatalf("조회 횟수 = %d — 화면당 한 번도 못 돌았다", reads.Load())
	}
	// 전부 성공했는데 빈 결과였다면 이 테스트는 아무것도 검증하지 못한 것이다.
	rows, err := svc.Sessions(context.Background(), SessionQuery{Limit: 100})
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(rows) < 2 {
		t.Fatalf("세션 = %d건, want 2건 이상", len(rows))
	}
}

// TestConcurrentServiceReconnectsMidQuery 는 **조회가 도는 중에 DB 가 생기는** 경우다.
//
// GUI 를 먼저 켜고 나중에 `telemetryctl local enable` 로 데몬이 DB 를 만드는 순서가
// 정상 시나리오다 (dashboard.go 의 Reopen 주석). 그때 여러 화면이 동시에 reconnect 를
// 부르므로, 핸들이 갈리거나 새는 자리가 있다면 여기서만 드러난다.
func TestConcurrentServiceReconnectsMidQuery(t *testing.T) {
	dir := t.TempDir()
	path := store.PathIn(dir)

	// DB 가 없는 상태로 기동한다. 이것이 에러가 아니어야 한다 (ADR 0004).
	svc := NewService(path)
	if err := svc.Start(); err != nil {
		t.Fatalf("Start(DB 없음): %v", err)
	}
	t.Cleanup(func() { svc.Stop() }) //nolint:errcheck // 테스트 정리
	svc.Reader().now = func() time.Time { return testNow }
	stubTrayCollector(svc)
	if svc.Available() {
		t.Fatal("Available = true — 아직 DB 가 없다")
	}

	reads := serviceReads(svc)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var attempts atomic.Int64

	// 조회 고루틴들을 먼저 띄운다. 처음에는 전부 빈 결과를 받는다.
	for _, read := range reads {
		wg.Add(1)
		go func(name string, call func(context.Context) error) {
			defer wg.Done()
			ctx := context.Background()
			for {
				if err := call(ctx); err != nil {
					t.Errorf("%s 조회 실패: %v", name, err)
					return
				}
				attempts.Add(1)
				select {
				case <-stop:
					return
				default:
				}
			}
		}(read.name, read.call)
	}

	// 조회가 이미 도는 동안 데몬이 DB 를 만들고 쓴다.
	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() }) //nolint:errcheck // 테스트 정리

	writeDone := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		daemonWriter(t, db, 20, writeDone)()
	}()
	<-writeDone

	// 데몬이 다 쓴 뒤에도 조회가 몇 바퀴 더 돌게 두어 재연결이 실제로 일어나게 한다.
	deadline := time.Now().Add(3 * time.Second)
	for !svc.Available() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	close(stop)
	wg.Wait()

	if !svc.Available() {
		t.Fatal("DB 가 생겼는데 Available = false — 재연결이 일어나지 않았다")
	}
	if attempts.Load() == 0 {
		t.Fatal("조회가 한 번도 돌지 않았다")
	}
	rows, err := svc.Sessions(context.Background(), SessionQuery{Limit: 100})
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("재연결 후에도 세션이 보이지 않는다")
	}
	// 재연결 뒤의 화면도 정상 모양이어야 한다.
	home, err := svc.Home(context.Background(), HomeQuery{TZ: seoul})
	if err != nil {
		t.Fatalf("Home: %v", err)
	}
	if len(home.Cards) != 4 {
		t.Errorf("카드 = %d장, want 4", len(home.Cards))
	}
}

// TestConcurrentTrayMonitorIsOnePerService 는 여러 화면이 동시에 트레이를 불러도
// 모니터가 하나로 유지되는지 본다. 호출마다 새로 만들면 "마지막 정상 스냅샷" 이 매번
// 사라져 실패가 곧 빈 화면이 된다 (service.go 의 tray 필드 주석).
func TestConcurrentTrayMonitorIsOnePerService(t *testing.T) {
	f := newFixture(t)
	f.write(store.Batch{
		Sessions: []session.Session{newSession("cw-tray", testNow.Add(-time.Hour), running)},
	})

	svc := NewService(f.path)
	if err := svc.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { svc.Stop() }) //nolint:errcheck // 테스트 정리
	svc.Reader().now = func() time.Time { return testNow }

	col := &stubCollector{snap: vendorlimit.Snapshot{
		Results:    []vendorlimit.Result{availableResult(vendorlimit.VendorClaudeCode)},
		ObservedAt: "2026-08-10T02:00:00Z",
	}}
	svc.tray.collect = col.collect
	svc.tray.now = func() time.Time { return testNow }

	// 주기(60초) 안에서 동시에 여러 번 부른다. 모니터가 하나라면 벤더 조회는 한 번뿐이다.
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := svc.Tray(context.Background(), TrayQuery{TZ: seoul}); err != nil {
				t.Errorf("Tray: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := col.count(); got != 1 {
		t.Errorf("벤더 조회 = %d회, want 1 — 주기를 지키는 캐시가 동시 호출에서 무너졌다", got)
	}
}

// TestConcurrentServiceIgnoresHalfCreatedDatabase 는 위 경합에서 실제로 드러난 버그의
// 화면 쪽 회귀 테스트다.
//
// 데몬이 DB 를 만드는 중(파일은 있고 테이블은 아직 없음)에 서비스가 재연결하면, 예전에는
// 그 핸들을 붙잡고 **모든 화면이 `no such table` 로 실패** 했다. Reopen 은 이미 붙었다고
// 보고 다시 시도하지 않아 앱을 껐다 켜기 전에는 회복되지 않았다.
//
// 지금은 준비되지 않은 DB 를 파일이 없는 것과 같이 다룬다 (store.OpenReadOnlyIfPresent).
// 그래서 이 상태의 화면은 **빈 결과** 이고, 마이그레이션이 끝나면 다음 조회가 붙는다.
func TestConcurrentServiceIgnoresHalfCreatedDatabase(t *testing.T) {
	dir := t.TempDir()
	path := store.PathIn(dir)

	// 데몬이 파일만 만들고 아직 마이그레이션을 끝내지 못한 상태를 흉내 낸다.
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	svc := NewService(path)
	if err := svc.Start(); err != nil {
		t.Fatalf("Start: %v — 준비 중인 DB 는 기동 실패가 아니다", err)
	}
	t.Cleanup(func() { svc.Stop() }) //nolint:errcheck // 테스트 정리
	svc.Reader().now = func() time.Time { return testNow }
	stubTrayCollector(svc)

	if svc.Available() {
		t.Fatal("Available = true — 테이블이 없는 파일에 붙었다")
	}

	ctx := context.Background()
	for _, read := range serviceReads(svc) {
		if err := read.call(ctx); err != nil {
			t.Errorf("%s: %v — 준비 중인 DB 는 빈 결과여야 한다", read.name, err)
		}
	}

	// 이제 데몬이 마이그레이션을 끝낸다. 앱 재시작 없이 다음 조회가 붙어야 한다.
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() }) //nolint:errcheck // 테스트 정리
	at := testNow.Add(-time.Hour)
	if _, err := db.Write(ctx, store.Batch{
		Sessions: []session.Session{newSession("half", at)},
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	rows, err := svc.Sessions(ctx, SessionQuery{Limit: 10})
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if !svc.Available() {
		t.Fatal("마이그레이션이 끝났는데 붙지 못했다 — 앱을 껐다 켜야 회복된다")
	}
	if len(rows) != 1 {
		t.Errorf("세션 = %d건, want 1", len(rows))
	}
}
