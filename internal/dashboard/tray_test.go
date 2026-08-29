package dashboard

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
//   - 가장 빠듯한 한도의 선택과 동률 규칙이 결정론이다.
//   - 새로고침이 실패하면 마지막 정상 스냅샷과 stale 상태를 유지한다.

// ── 테스트 보조 ─────────────────────────────────────────────────────────────

// availableResult 는 성공한 벤더 하나의 결과다.
func availableResult(v vendorlimit.Vendor, windows ...vendorlimit.Window) vendorlimit.Result {
	if windows == nil {
		windows = []vendorlimit.Window{}
	}
	return vendorlimit.Result{
		Vendor:     v,
		State:      vendorlimit.StateAvailable,
		Windows:    windows,
		ObservedAt: "2026-08-10T02:00:00Z",
	}
}

// unavailableResult 는 실패한 벤더 하나의 결과다. 창이 있어도 후보가 되면 안 된다.
func unavailableResult(v vendorlimit.Vendor, reason vendorlimit.Reason, windows ...vendorlimit.Window) vendorlimit.Result {
	if windows == nil {
		windows = []vendorlimit.Window{}
	}
	return vendorlimit.Result{
		Vendor:     v,
		State:      vendorlimit.StateUnavailable,
		Reason:     reason,
		Windows:    windows,
		ObservedAt: "2026-08-10T02:00:00Z",
	}
}

func window(period vendorlimit.PeriodKind, label string, ratio float64, resetsIn int64) vendorlimit.Window {
	return vendorlimit.Window{Period: period, Label: label, UsedRatio: ratio, ResetsInSeconds: resetsIn}
}

// stubCollector 는 벤더 한도 조회를 대신한다. 호출 횟수를 세어 갱신 주기를 검증한다.
type stubCollector struct {
	mu    sync.Mutex
	calls int
	snap  vendorlimit.Snapshot
}

func (c *stubCollector) collect(context.Context) vendorlimit.Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	return c.snap
}

func (c *stubCollector) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// newTestMonitor 는 벽시계와 네트워크에 닿지 않는 모니터다.
func newTestMonitor(r *Reader, snap vendorlimit.Snapshot) (*TrayMonitor, *stubCollector, *time.Time) {
	col := &stubCollector{snap: snap}
	clock := testNow
	m := NewTrayMonitor(r)
	m.collect = col.collect
	m.now = func() time.Time { return clock }
	return m, col, &clock
}

// ── 가장 빠듯한 한도 ────────────────────────────────────────────────────────

func TestTightestLimitIsDeterministic(t *testing.T) {
	cases := []struct {
		name       string
		results    []vendorlimit.Result
		wantFound  bool
		wantVendor string
		wantLabel  string
	}{
		{
			name:      "후보가 없으면 없음",
			results:   []vendorlimit.Result{},
			wantFound: false,
		},
		{
			name: "unavailable 벤더만 있으면 없음",
			results: []vendorlimit.Result{
				unavailableResult(vendorlimit.VendorClaudeCode, vendorlimit.ReasonCredentialMissing,
					window(vendorlimit.PeriodFiveHour, "five_hour", 0.99, 60)),
			},
			wantFound: false,
		},
		{
			name: "사용률이 높은 쪽이 이긴다",
			results: []vendorlimit.Result{
				availableResult(vendorlimit.VendorClaudeCode,
					window(vendorlimit.PeriodFiveHour, "five_hour", 0.42, 3600),
					window(vendorlimit.PeriodWeekly, "seven_day", 0.87, 500000)),
				availableResult(vendorlimit.VendorCodex,
					window(vendorlimit.PeriodFiveHour, "primary", 0.51, 600)),
			},
			wantFound: true, wantVendor: "claude_code", wantLabel: "seven_day",
		},
		{
			name: "사용률이 같으면 초기화가 빠른 쪽",
			results: []vendorlimit.Result{
				availableResult(vendorlimit.VendorClaudeCode,
					window(vendorlimit.PeriodWeekly, "seven_day", 0.9, 500000)),
				availableResult(vendorlimit.VendorCodex,
					window(vendorlimit.PeriodFiveHour, "primary", 0.9, 300)),
			},
			wantFound: true, wantVendor: "codex", wantLabel: "primary",
		},
		{
			name: "초기화 시각을 모르는 창은 아는 창에 진다",
			results: []vendorlimit.Result{
				availableResult(vendorlimit.VendorClaudeCode,
					window(vendorlimit.PeriodFiveHour, "unknown_reset", 0.9, 0)),
				availableResult(vendorlimit.VendorCodex,
					window(vendorlimit.PeriodWeekly, "known_reset", 0.9, 999999)),
			},
			wantFound: true, wantVendor: "codex", wantLabel: "known_reset",
		},
		{
			name: "사용률·초기화가 같으면 벤더 이름 오름차순",
			results: []vendorlimit.Result{
				availableResult(vendorlimit.VendorCodex,
					window(vendorlimit.PeriodFiveHour, "primary", 0.75, 1200)),
				availableResult(vendorlimit.VendorClaudeCode,
					window(vendorlimit.PeriodFiveHour, "primary", 0.75, 1200)),
			},
			wantFound: true, wantVendor: "claude_code", wantLabel: "primary",
		},
		{
			name: "같은 벤더의 동률은 짧은 창이 이긴다",
			results: []vendorlimit.Result{
				availableResult(vendorlimit.VendorClaudeCode,
					window(vendorlimit.PeriodMonthly, "monthly", 0.75, 1200),
					window(vendorlimit.PeriodWeekly, "seven_day", 0.75, 1200),
					window(vendorlimit.PeriodFiveHour, "five_hour", 0.75, 1200)),
			},
			wantFound: true, wantVendor: "claude_code", wantLabel: "five_hour",
		},
		{
			name: "창 종류까지 같으면 Label 오름차순",
			results: []vendorlimit.Result{
				availableResult(vendorlimit.VendorClaudeCode,
					window(vendorlimit.PeriodWeekly, "seven_day_opus", 0.75, 1200),
					window(vendorlimit.PeriodWeekly, "seven_day", 0.75, 1200)),
			},
			wantFound: true, wantVendor: "claude_code", wantLabel: "seven_day",
		},
		{
			name: "unavailable 벤더의 더 높은 창이 선택을 흔들지 않는다",
			results: []vendorlimit.Result{
				unavailableResult(vendorlimit.VendorClaudeCode, vendorlimit.ReasonTokenExpired,
					window(vendorlimit.PeriodFiveHour, "five_hour", 1.0, 10)),
				availableResult(vendorlimit.VendorCodex,
					window(vendorlimit.PeriodFiveHour, "primary", 0.11, 7200)),
			},
			wantFound: true, wantVendor: "codex", wantLabel: "primary",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tightestLimit(tc.results)
			if got.Found != tc.wantFound {
				t.Fatalf("Found = %v, want %v (%+v)", got.Found, tc.wantFound, got)
			}
			if !tc.wantFound {
				return
			}
			if got.Vendor != tc.wantVendor || got.Label != tc.wantLabel {
				t.Fatalf("선택 = %s/%s, want %s/%s", got.Vendor, got.Label, tc.wantVendor, tc.wantLabel)
			}
		})
	}
}

// 같은 입력을 여러 번 넣어도 같은 답이어야 한다. 순회 순서에 기대는 구현이면 여기서 흔들린다.
func TestTightestLimitRepeatsTheSameAnswer(t *testing.T) {
	results := []vendorlimit.Result{
		availableResult(vendorlimit.VendorClaudeCode,
			window(vendorlimit.PeriodFiveHour, "five_hour", 0.5, 900),
			window(vendorlimit.PeriodWeekly, "seven_day", 0.5, 900)),
		availableResult(vendorlimit.VendorCodex,
			window(vendorlimit.PeriodFiveHour, "primary", 0.5, 900),
			window(vendorlimit.PeriodMonthly, "monthly", 0.5, 900)),
	}
	first := tightestLimit(results)
	for i := range 50 {
		if got := tightestLimit(results); got != first {
			t.Fatalf("%d번째 호출이 다른 답을 냈다: %+v != %+v", i, got, first)
		}
	}
}

// ── 한 응답 ─────────────────────────────────────────────────────────────────

func TestTraySnapshotAnswersEverythingAtOnce(t *testing.T) {
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
	m, col, _ := newTestMonitor(f.reader, snap)

	got, err := m.Snapshot(context.Background(), TrayQuery{TZ: seoul})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if col.count() != 1 {
		t.Fatalf("벤더 한도 조회 = %d회, want 1", col.count())
	}

	// 모니터링 상태 — 데몬 runtime.json 이 없으므로 DB 는 있는데 수집은 멈춘 상태다.
	if got.Monitoring.State != TrayStatePaused {
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
func TestTrayVendorFailureDoesNotBlankTheRest(t *testing.T) {
	f := newFixture(t)
	at := testNow.Add(-time.Hour)
	f.write(store.Batch{
		Sessions: []session.Session{newSession("s-1", at), newSession("s-2", at.Add(-time.Minute))},
	})

	m, _, _ := newTestMonitor(f.reader, vendorlimit.Snapshot{
		ObservedAt: "2026-08-10T02:00:00Z",
		Results: []vendorlimit.Result{
			unavailableResult(vendorlimit.VendorClaudeCode, vendorlimit.ReasonNetwork),
			availableResult(vendorlimit.VendorCodex,
				window(vendorlimit.PeriodFiveHour, "primary", 0.44, 1800)),
		},
	})

	got, err := m.Snapshot(context.Background(), TrayQuery{TZ: seoul})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
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

// ── 새로고침 실패 ───────────────────────────────────────────────────────────

// 갱신이 실패하면 마지막 정상 스냅샷을 그대로 두고 stale 만 세운다.
func TestTrayKeepsLastGoodSnapshotOnRefreshFailure(t *testing.T) {
	f := newFixture(t)
	f.write(store.Batch{
		Sessions: []session.Session{newSession("s-1", testNow.Add(-time.Hour))},
	})

	m, col, clock := newTestMonitor(f.reader, vendorlimit.Snapshot{
		ObservedAt: "2026-08-10T02:00:00Z",
		Results: []vendorlimit.Result{
			availableResult(vendorlimit.VendorCodex, window(vendorlimit.PeriodFiveHour, "primary", 0.5, 600)),
		},
	})

	ctx := context.Background()
	good, err := m.Snapshot(ctx, TrayQuery{TZ: seoul})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(good.Recent) != 1 {
		t.Fatalf("최근 세션 = %d개, want 1", len(good.Recent))
	}

	// 조회 커넥션을 끊어 로컬 질의를 실제로 실패시킨다.
	if err := f.reader.ro.SQL().Close(); err != nil {
		t.Fatalf("조회 커넥션 닫기: %v", err)
	}
	*clock = testNow.Add(5 * time.Minute)

	stale, err := m.Refresh(ctx, TrayQuery{TZ: seoul})
	if err != nil {
		t.Fatalf("Refresh 가 error 를 냈다 — 갱신 실패는 상태이지 오류가 아니다: %v", err)
	}
	if !stale.Stale || stale.StaleReason != TrayStaleLocalQuery {
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
	// 로컬 조회가 실패했으면 벤더 API 를 다시 두드릴 이유가 없다.
	if col.count() != 1 {
		t.Errorf("벤더 한도 조회 = %d회, want 1", col.count())
	}
}

// 한 번도 성공한 적이 없는 상태에서 실패해도 화면이 분기 없이 그릴 모양은 나와야 한다.
func TestTrayFirstRefreshFailureKeepsResponseShape(t *testing.T) {
	f := newFixture(t)
	if err := f.reader.ro.SQL().Close(); err != nil {
		t.Fatalf("조회 커넥션 닫기: %v", err)
	}
	m, _, _ := newTestMonitor(f.reader, vendorlimit.Snapshot{})

	got, err := m.Snapshot(context.Background(), TrayQuery{TZ: seoul})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
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

func TestTraySnapshotHonoursRefreshInterval(t *testing.T) {
	f := newFixture(t)
	m, col, clock := newTestMonitor(f.reader, vendorlimit.Snapshot{})
	ctx := context.Background()
	q := TrayQuery{TZ: seoul}

	if _, err := m.Snapshot(ctx, q); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	// 주기 안에서는 다시 만들지 않는다.
	*clock = testNow.Add(DefaultTrayInterval - time.Second)
	if _, err := m.Snapshot(ctx, q); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if col.count() != 1 {
		t.Fatalf("주기 안인데 %d회 조회했다", col.count())
	}
	// 주기를 넘기면 다시 만든다.
	*clock = testNow.Add(DefaultTrayInterval)
	if _, err := m.Snapshot(ctx, q); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if col.count() != 2 {
		t.Fatalf("주기를 넘겼는데 %d회 조회했다", col.count())
	}
	// 조건이 바뀌면 주기와 무관하게 다시 만든다 — 남의 시간대 스냅샷을 캐시로 주면 안 된다.
	if _, err := m.Snapshot(ctx, TrayQuery{TZ: utc}); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if col.count() != 3 {
		t.Fatalf("시간대가 바뀌었는데 %d회 조회했다", col.count())
	}
	// 명시적 새로고침은 주기를 기다리지 않는다.
	if _, err := m.Refresh(ctx, TrayQuery{TZ: utc}); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if col.count() != 4 {
		t.Fatalf("Refresh 가 캐시를 돌려줬다 (%d회)", col.count())
	}
}

// 시간대 오타는 갱신 실패가 아니라 호출자 버그다. 마지막 정상 스냅샷으로 덮지 않는다.
func TestTrayRejectsUnknownTimezone(t *testing.T) {
	f := newFixture(t)
	m, col, _ := newTestMonitor(f.reader, vendorlimit.Snapshot{})
	for _, call := range []func() (TraySnapshot, error){
		func() (TraySnapshot, error) { return m.Snapshot(context.Background(), TrayQuery{TZ: "Mars/Phobos"}) },
		func() (TraySnapshot, error) { return m.Refresh(context.Background(), TrayQuery{TZ: "Mars/Phobos"}) },
	} {
		if _, err := call(); err == nil {
			t.Error("에러가 없다 — 시간대 오타를 조용히 UTC 로 떨어뜨리면 안 된다")
		}
	}
	if col.count() != 0 {
		t.Errorf("잘못된 입력으로 벤더 API 를 두드렸다 (%d회)", col.count())
	}
}

// ── 동시성·계약 ─────────────────────────────────────────────────────────────

// 트레이는 여러 창에서 동시에 열릴 수 있다. -race 로 돈다.
func TestTrayMonitorIsSafeForConcurrentUse(t *testing.T) {
	f := newFixture(t)
	f.write(store.Batch{Sessions: []session.Session{newSession("s-1", testNow.Add(-time.Hour))}})
	m, _, _ := newTestMonitor(f.reader, vendorlimit.Snapshot{
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
				got TraySnapshot
				err error
			)
			if i%2 == 0 {
				got, err = m.Snapshot(ctx, TrayQuery{TZ: seoul})
			} else {
				got, err = m.Refresh(ctx, TrayQuery{TZ: seoul})
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
func TestTrayResponseTagsAreSnakeCase(t *testing.T) {
	assertSnakeCaseTags(t, TraySnapshot{})
	assertSnakeCaseTags(t, TrayMonitoring{})
	assertSnakeCaseTags(t, TightestLimit{})
	assertSnakeCaseTags(t, TrayQuery{})
}

// 서비스는 모니터를 하나만 들고 있어야 한다. 호출마다 새로 만들면 "마지막 정상 스냅샷" 이
// 매번 사라져 실패가 곧 빈 화면이 된다.
func TestServiceTrayReusesOneMonitor(t *testing.T) {
	f := newFixture(t)
	f.write(store.Batch{Sessions: []session.Session{newSession("s-1", testNow.Add(-time.Hour))}})

	svc := NewService(f.path)
	if err := svc.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { svc.Stop() }) //nolint:errcheck // 테스트 정리
	svc.Reader().now = func() time.Time { return testNow }

	col := &stubCollector{snap: vendorlimit.Snapshot{
		ObservedAt: "2026-08-10T02:00:00Z",
		Results: []vendorlimit.Result{
			availableResult(vendorlimit.VendorCodex, window(vendorlimit.PeriodFiveHour, "primary", 0.5, 600)),
		},
	}}
	svc.tray.collect = col.collect
	svc.tray.now = func() time.Time { return testNow }

	ctx := context.Background()
	for range 3 {
		got, err := svc.Tray(ctx, TrayQuery{TZ: seoul})
		if err != nil {
			t.Fatalf("Tray: %v", err)
		}
		if len(got.Recent) != 1 || !got.Tightest.Found {
			t.Fatalf("스냅샷이 비었다: %+v", got)
		}
	}
	if col.count() != 1 {
		t.Fatalf("벤더 한도 조회 = %d회, want 1 (모니터가 호출마다 새로 만들어졌다)", col.count())
	}
	// 명시적 새로고침만 주기를 건너뛴다.
	if _, err := svc.RefreshTray(ctx, TrayQuery{TZ: seoul}); err != nil {
		t.Fatalf("RefreshTray: %v", err)
	}
	if col.count() != 2 {
		t.Fatalf("RefreshTray 가 캐시를 돌려줬다 (%d회)", col.count())
	}
}
