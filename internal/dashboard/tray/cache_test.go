package tray

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/session"
	"github.com/your-org/pulsemetry/internal/store"
	"github.com/your-org/pulsemetry/internal/vendorlimit"
)

// 이 파일이 **트레이 스냅샷의 계약** 이다 (PROJ-96).
//
//   - 한 응답에 모니터링 상태·마지막 갱신 시각·활성/최근 세션·벤더 한도·가장 빠듯한 한도가 있다.
//   - 부분 장애가 다른 벤더와 최근 세션 표시를 막지 않는다.
//   - 새로고침이 실패하면 마지막 정상 스냅샷과 stale 상태를 유지한다.

// ── 한 응답 ─────────────────────────────────────────────────────────────────

func TestSnapshotAnswersEverythingAtOnce(t *testing.T) {
	f := newFixture(t)
	at := testNow.Add(-2 * time.Hour)
	f.write(store.Batch{
		Sessions: []session.Session{
			newSession("s-running", at, running),
			newSession("s-done", at.Add(-time.Hour), codex),
		},
		Events: []store.EventRecord{
			llmRecord("s-running", "t-1", at, 1, llmSpec{Model: "claude-sonnet-4", Cost: 0.2, Input: 100, Output: 50}),
		},
	})

	snap := vendorlimit.Snapshot{
		ObservedAt: "2026-08-10T02:00:00Z",
		Results: []vendorlimit.Result{
			availableResult(vendorlimit.VendorClaudeCode,
				window(vendorlimit.PeriodFiveHour, "five_hour", 0.62, 3600)),
			availableResult(vendorlimit.VendorCodex,
				window(vendorlimit.PeriodWeekly, "primary", 0.31, 400000)),
		},
	}
	m, col, _ := newTestCache(f.svc, snap)

	got, err := m.Current(context.Background(), Query{TZ: seoul})
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if col.count() != 1 {
		t.Fatalf("벤더 한도 조회 = %d회, want 1", col.count())
	}

	// 모니터링 상태 — 데몬 runtime.json 이 없으므로 DB 는 있는데 수집은 멈춘 상태다.
	if got.Monitoring.State != StatePaused {
		t.Errorf("State = %q, want paused", got.Monitoring.State)
	}
	if !got.Monitoring.DatabaseAvailable {
		t.Error("DatabaseAvailable = false")
	}
	if got.Monitoring.LastEventAt == 0 {
		t.Error("LastEventAt = 0 — 이벤트를 넣었는데 마지막 수집 시각이 없다")
	}
	if got.Monitoring.RunningSessions != 1 {
		t.Errorf("RunningSessions = %d, want 1", got.Monitoring.RunningSessions)
	}

	// 마지막 갱신 시각.
	if got.RefreshedAt != testNow.Unix() || got.CheckedAt != testNow.Unix() {
		t.Errorf("갱신 시각 = %d/%d, want %d", got.RefreshedAt, got.CheckedAt, testNow.Unix())
	}
	if got.Stale || got.StaleReason != "" {
		t.Errorf("첫 성공 조회가 stale 이다: %+v", got)
	}

	// 활성·최근 세션.
	if got.ActiveSessions != 1 || !containsString(got.ActiveAgents, vendorClaude) {
		t.Errorf("활성 = %d/%v", got.ActiveSessions, got.ActiveAgents)
	}
	if len(got.Recent) != 2 {
		t.Errorf("최근 세션 = %d개, want 2 (%+v)", len(got.Recent), got.Recent)
	}

	// 벤더 한도와 가장 빠듯한 한도.
	if len(got.Limits) != 2 {
		t.Fatalf("한도 = %d개, want 2", len(got.Limits))
	}
	if got.LimitsObservedAt != snap.ObservedAt {
		t.Errorf("LimitsObservedAt = %q, want %q", got.LimitsObservedAt, snap.ObservedAt)
	}
	if !got.Tightest.Found || got.Tightest.Vendor != "claude_code" {
		t.Errorf("Tightest = %+v", got.Tightest)
	}
	if got.TZ != seoul || got.Date == "" {
		t.Errorf("시간대·날짜가 비었다: %+v", got)
	}
}

// 인수조건 — 한 벤더의 실패가 다른 벤더와 최근 세션 표시를 막지 않는다.
func TestVendorFailureDoesNotBlankTheRest(t *testing.T) {
	f := newFixture(t)
	at := testNow.Add(-time.Hour)
	f.write(store.Batch{
		Sessions: []session.Session{newSession("s-1", at), newSession("s-2", at.Add(-time.Minute))},
	})

	m, _, _ := newTestCache(f.svc, vendorlimit.Snapshot{
		ObservedAt: "2026-08-10T02:00:00Z",
		Results: []vendorlimit.Result{
			unavailableResult(vendorlimit.VendorClaudeCode, vendorlimit.ReasonNetwork),
			availableResult(vendorlimit.VendorCodex,
				window(vendorlimit.PeriodFiveHour, "primary", 0.44, 1800)),
		},
	})

	got, err := m.Current(context.Background(), Query{TZ: seoul})
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if got.Stale {
		t.Error("한 벤더의 실패가 스냅샷 전체를 stale 로 만들었다")
	}
	// 실패한 벤더도 자리를 지킨다 — 빠지면 화면이 "로딩 중" 과 구분하지 못한다.
	if len(got.Limits) != 2 {
		t.Fatalf("한도 = %d개, want 2 (실패한 벤더도 남아야 한다)", len(got.Limits))
	}
	if got.Limits[0].State != vendorlimit.StateUnavailable || got.Limits[0].Reason != vendorlimit.ReasonNetwork {
		t.Errorf("실패한 벤더의 사유가 사라졌다: %+v", got.Limits[0])
	}
	if got.Limits[1].State != vendorlimit.StateAvailable {
		t.Errorf("성공한 벤더가 함께 지워졌다: %+v", got.Limits[1])
	}
	// 가장 빠듯한 한도는 available 벤더에서만 나온다.
	if !got.Tightest.Found || got.Tightest.Vendor != "codex" {
		t.Errorf("Tightest = %+v, want codex", got.Tightest)
	}
	// 최근 세션은 벤더 조회와 무관하다.
	if len(got.Recent) != 2 {
		t.Errorf("최근 세션 = %d개, want 2", len(got.Recent))
	}
}

// ── 저장된 한도 읽기 ────────────────────────────────────────────────────────

// 데몬이 SQLite 에 넣어 둔 스냅샷을 트레이가 그대로 읽어야 한다. collect 를 바꿔치기하지
// 않고 New 의 기본 경로를 그대로 태우는 유일한 테스트다.
func TestReadsPersistedVendorLimits(t *testing.T) {
	f := newFixture(t)
	result := vendorlimit.Result{Vendor: vendorlimit.VendorCodex, State: vendorlimit.StateAvailable,
		Plan: "pro", Windows: []vendorlimit.Window{{Period: vendorlimit.PeriodFiveHour, Label: "primary", UsedRatio: .42}},
		ObservedAt: "2026-08-30T00:00:00Z"}
	if err := f.db.UpsertVendorLimit(context.Background(), result, testNow); err != nil {
		t.Fatal(err)
	}
	m := New(builderSource{NewBuilder(f.svc)})
	m.now = func() time.Time { return testNow }
	got, err := m.RefreshManual(context.Background(), Query{TZ: utc})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Limits) != 2 {
		t.Fatalf("limits=%d", len(got.Limits))
	}
	for _, limit := range got.Limits {
		if limit.Vendor == vendorlimit.VendorCodex {
			if limit.Plan != "pro" || len(limit.Windows) != 1 {
				t.Fatalf("codex=%+v", limit)
			}
			return
		}
	}
	t.Fatal("codex 한도가 없음")
}

// ── 새로고침 실패 ───────────────────────────────────────────────────────────

// 갱신이 실패하면 마지막 정상 스냅샷을 그대로 두고 stale 만 세운다.
func TestKeepsLastGoodSnapshotOnRefreshFailure(t *testing.T) {
	f := newFixture(t)
	f.write(store.Batch{
		Sessions: []session.Session{newSession("s-1", testNow.Add(-time.Hour))},
	})

	m, col, clock := newTestCache(f.svc, vendorlimit.Snapshot{
		ObservedAt: "2026-08-10T02:00:00Z",
		Results: []vendorlimit.Result{
			availableResult(vendorlimit.VendorCodex, window(vendorlimit.PeriodFiveHour, "primary", 0.5, 600)),
		},
	})

	ctx := context.Background()
	good, err := m.Current(ctx, Query{TZ: seoul})
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if len(good.Recent) != 1 {
		t.Fatalf("최근 세션 = %d개, want 1", len(good.Recent))
	}

	f.breakQueries()
	*clock = testNow.Add(5 * time.Minute)

	stale, err := m.RefreshManual(ctx, Query{TZ: seoul})
	if err != nil {
		t.Fatalf("Refresh 가 error 를 냈다 — 갱신 실패는 상태이지 오류가 아니다: %v", err)
	}
	if !stale.Stale || stale.StaleReason != StaleLocalQuery {
		t.Fatalf("stale 표시가 없다: %+v", stale)
	}
	if stale.RefreshedAt != good.RefreshedAt {
		t.Errorf("RefreshedAt = %d, want %d (성공한 갱신 시각이 유지돼야 한다)", stale.RefreshedAt, good.RefreshedAt)
	}
	if stale.CheckedAt != clock.Unix() {
		t.Errorf("CheckedAt = %d, want %d (시도 시각은 움직여야 한다)", stale.CheckedAt, clock.Unix())
	}
	// 값 자체는 마지막 정상 스냅샷 그대로다.
	if len(stale.Recent) != len(good.Recent) || stale.ActiveSessions != good.ActiveSessions {
		t.Errorf("마지막 정상 스냅샷이 지워졌다: %+v", stale)
	}
	if len(stale.Limits) != len(good.Limits) || stale.Tightest != good.Tightest {
		t.Errorf("한도가 지워졌다: %+v", stale)
	}
	// 로컬 조회가 실패했으면 벤더 한도를 다시 읽을 이유가 없다.
	if col.count() != 1 {
		t.Errorf("벤더 한도 조회 = %d회, want 1", col.count())
	}
}

// 한 번도 성공한 적이 없는 상태에서 실패해도 화면이 분기 없이 그릴 모양은 나와야 한다.
func TestFirstRefreshFailureKeepsResponseShape(t *testing.T) {
	f := newFixture(t)
	f.breakQueries()
	m, _, _ := newTestCache(f.svc, vendorlimit.Snapshot{})

	got, err := m.Current(context.Background(), Query{TZ: seoul})
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if !got.Stale || got.RefreshedAt != 0 {
		t.Fatalf("첫 실패의 모양이 틀렸다: %+v", got)
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, null := range []string{`"active_agents":null`, `"recent_sessions":null`, `"limits":null`} {
		if strings.Contains(string(b), null) {
			t.Errorf("JSON 에 null 슬라이스가 있다: %s", null)
		}
	}
}

// ── 갱신 주기 ───────────────────────────────────────────────────────────────

func TestSnapshotHonoursRefreshInterval(t *testing.T) {
	f := newFixture(t)
	m, col, clock := newTestCache(f.svc, vendorlimit.Snapshot{})
	ctx := context.Background()
	q := Query{TZ: seoul}

	if _, err := m.Current(ctx, q); err != nil {
		t.Fatalf("Current: %v", err)
	}
	// 주기 안에서는 다시 만들지 않는다.
	*clock = testNow.Add(DefaultInterval - time.Second)
	if _, err := m.Current(ctx, q); err != nil {
		t.Fatalf("Current: %v", err)
	}
	if col.count() != 1 {
		t.Fatalf("주기 안인데 %d회 조회했다", col.count())
	}
	// 주기를 넘기면 다시 만든다.
	*clock = testNow.Add(DefaultInterval)
	if _, err := m.Current(ctx, q); err != nil {
		t.Fatalf("Current: %v", err)
	}
	if col.count() != 2 {
		t.Fatalf("주기를 넘겼는데 %d회 조회했다", col.count())
	}
	// 조건이 바뀌면 주기와 무관하게 다시 만든다 — 남의 시간대 스냅샷을 캐시로 주면 안 된다.
	if _, err := m.Current(ctx, Query{TZ: utc}); err != nil {
		t.Fatalf("Current: %v", err)
	}
	if col.count() != 3 {
		t.Fatalf("시간대가 바뀌었는데 %d회 조회했다", col.count())
	}
	// 명시적 새로고침은 주기를 기다리지 않는다.
	if _, err := m.RefreshManual(ctx, Query{TZ: utc}); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if col.count() != 4 {
		t.Fatalf("Refresh 가 캐시를 돌려줬다 (%d회)", col.count())
	}
}

// 시간대 오타는 갱신 실패가 아니라 호출자 버그다. 마지막 정상 스냅샷으로 덮지 않는다.
func TestRejectsUnknownTimezone(t *testing.T) {
	f := newFixture(t)
	m, col, _ := newTestCache(f.svc, vendorlimit.Snapshot{})
	for _, call := range []func() (Snapshot, error){
		func() (Snapshot, error) { return m.Current(context.Background(), Query{TZ: "Mars/Phobos"}) },
		func() (Snapshot, error) { return m.RefreshManual(context.Background(), Query{TZ: "Mars/Phobos"}) },
	} {
		if _, err := call(); err == nil {
			t.Error("에러가 없다 — 시간대 오타를 조용히 UTC 로 떨어뜨리면 안 된다")
		}
	}
	if col.count() != 0 {
		t.Errorf("잘못된 입력으로 한도를 조회했다 (%d회)", col.count())
	}
}

// ── 동시성·계약 ─────────────────────────────────────────────────────────────

// 트레이는 여러 창에서 동시에 열릴 수 있다. -race 로 돈다.
func TestCacheIsSafeForConcurrentUse(t *testing.T) {
	f := newFixture(t)
	f.write(store.Batch{Sessions: []session.Session{newSession("s-1", testNow.Add(-time.Hour))}})
	m, _, _ := newTestCache(f.svc, vendorlimit.Snapshot{
		Results: []vendorlimit.Result{
			availableResult(vendorlimit.VendorCodex, window(vendorlimit.PeriodFiveHour, "primary", 0.5, 600)),
		},
	})

	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.Background()
			var (
				got Snapshot
				err error
			)
			if i%2 == 0 {
				got, err = m.Current(ctx, Query{TZ: seoul})
			} else {
				got, err = m.RefreshManual(ctx, Query{TZ: seoul})
			}
			if err != nil {
				t.Errorf("조회 %d: %v", i, err)
				return
			}
			if got.TZ != seoul {
				t.Errorf("조회 %d 의 시간대 = %q", i, got.TZ)
			}
		}()
	}
	wg.Wait()
}

// 응답 타입의 json 태그가 snake_case 여야 프런트엔드가 필드를 찾는다 (ADR 0004).
func TestResponseTagsAreSnakeCase(t *testing.T) {
	assertSnakeCaseTags(t, Snapshot{})
	assertSnakeCaseTags(t, Monitoring{})
	assertSnakeCaseTags(t, TightestLimit{})
	assertSnakeCaseTags(t, Query{})
}
