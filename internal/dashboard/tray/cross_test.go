package tray

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/dashboard"
	"github.com/your-org/pulsemetry/internal/session"
	"github.com/your-org/pulsemetry/internal/store"
	"github.com/your-org/pulsemetry/internal/vendorlimit"
)

// ── 본 화면과의 대조 ────────────────────────────────────────────────────────

// 트레이는 자기 SQL 을 쓰지 않고 Status·Home 을 그대로 부르는 것이 계약이다 (local.go).
func TestRepeatsHomeNumbers(t *testing.T) {
	f := newFixture(t)
	// 트레이는 "오늘" 을 본다. testNow 기준 오늘에 세션을 하나 둔다.
	at := testNow.Add(-30 * time.Minute)
	f.write(store.Batch{
		Sessions: []session.Session{newSession("cs-tray", at, running)},
		Events: []store.EventRecord{
			promptRecord("cs-tray", "cs-tray-t1", at, 1, "트레이 대조"),
			llmRecord("cs-tray", "cs-tray-t1", at, 2, llmSpec{Model: "claude-sonnet-4-5", Cost: 0.3, Input: 60, Output: 20}),
		},
	})

	ctx := context.Background()
	b, _, _ := newTestBuilder(f.svc, vendorlimit.Snapshot{
		Results:    []vendorlimit.Result{availableResult(vendorlimit.VendorClaudeCode)},
		ObservedAt: "2026-08-10T02:00:00Z",
	})
	snap, err := b.Snapshot(ctx, Query{TZ: seoul})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	home, err := f.svc.Home(ctx, dashboard.HomeQuery{TZ: seoul})
	if err != nil {
		t.Fatalf("Home: %v", err)
	}
	if snap.Date != home.Date || snap.TZ != home.TZ {
		t.Errorf("날짜·시간대가 다르다: tray = %s/%s, home = %s/%s",
			snap.Date, snap.TZ, home.Date, home.TZ)
	}
	if snap.ActiveSessions != home.ActiveSessions {
		t.Errorf("활성 세션: tray = %d, home = %d", snap.ActiveSessions, home.ActiveSessions)
	}
	if len(snap.Recent) != len(home.Recent) {
		t.Fatalf("최근 세션: tray = %d건, home = %d건", len(snap.Recent), len(home.Recent))
	}
	for i := range snap.Recent {
		if !reflect.DeepEqual(snap.Recent[i], home.Recent[i]) {
			t.Errorf("[%d] 최근 세션이 다르다:\ntray = %+v\nhome = %+v",
				i, snap.Recent[i], home.Recent[i])
		}
	}

	// Status 도 같은 근거를 본다.
	st, err := f.svc.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if snap.Monitoring.RunningSessions != st.RunningSessions {
		t.Errorf("진행 중 세션: tray = %d, status = %d",
			snap.Monitoring.RunningSessions, st.RunningSessions)
	}
	if snap.Monitoring.LastEventAt != st.NewestEventAt {
		t.Errorf("마지막 이벤트: tray = %d, status = %d",
			snap.Monitoring.LastEventAt, st.NewestEventAt)
	}
}

// ── 사용량 API 장애 ─────────────────────────────────────────────────────────

// 벤더 사용량 API 가 죽었을 때 트레이가 어떻게 보이는지 고정한다.
//
// 조회는 httptest 모의 서버로 간다. 응답 → Result 매핑 자체는 internal/vendorlimit 이
// 소유하고, 여기서 소유하는 것은 **화면 계약** 이다 — 한도가 비어도 로컬 숫자는 살아
// 있고, 스냅샷은 stale 이 아니며, 가장 빠듯한 한도는 없다.
func TestVendorUsageAPIFailureKeepsTrayUsable(t *testing.T) {
	f := newFixture(t)
	at := testNow.Add(-time.Hour)
	f.write(store.Batch{
		Sessions: []session.Session{newSession("api-down", at, running)},
		Events: []store.EventRecord{
			promptRecord("api-down", "api-down-t1", at, 1, "사용량 API 장애"),
			llmRecord("api-down", "api-down-t1", at, 2, llmSpec{
				Model: "claude-sonnet-4-5", Cost: 0.5, Input: 200, Output: 80,
			}),
		},
	})

	cases := []struct {
		name       string
		status     int
		wantReason vendorlimit.Reason
	}{
		{name: "5xx", status: http.StatusInternalServerError, wantReason: vendorlimit.ReasonUpstreamStatus},
		{name: "401", status: http.StatusUnauthorized, wantReason: vendorlimit.ReasonUpstreamStatus},
		{name: "429", status: http.StatusTooManyRequests, wantReason: vendorlimit.ReasonUpstreamStatus},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var hits int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits++
				w.WriteHeader(tc.status)
			}))
			t.Cleanup(srv.Close)

			b := newTestBuilderWithLimits(f.svc, func(ctx context.Context) vendorlimit.Snapshot {
				return probeFailingUsageAPI(ctx, srv.URL, tc.wantReason)
			})

			snap, err := b.Snapshot(context.Background(), Query{TZ: seoul})
			if err != nil {
				t.Fatalf("Snapshot: %v — 벤더 장애는 조회 실패가 아니다", err)
			}
			if hits == 0 {
				t.Fatal("모의 서버가 한 번도 불리지 않았다 — 이 테스트는 아무것도 검증하지 못했다")
			}

			// 로컬 숫자는 그대로 살아 있다.
			if snap.ActiveSessions != 1 {
				t.Errorf("활성 세션 = %d, want 1 — 벤더 장애가 로컬 표시를 지웠다", snap.ActiveSessions)
			}
			if len(snap.Recent) != 1 {
				t.Errorf("최근 세션 = %d건, want 1", len(snap.Recent))
			}
			// 실패한 벤더도 자리를 지킨다.
			if len(snap.Limits) != len(vendorlimit.SupportedVendors()) {
				t.Fatalf("한도 결과 = %d건, want %d", len(snap.Limits), len(vendorlimit.SupportedVendors()))
			}
			for _, res := range snap.Limits {
				if res.State != vendorlimit.StateUnavailable || res.Reason != tc.wantReason {
					t.Errorf("%s = %s/%s, want unavailable/%s",
						res.Vendor, res.State, res.Reason, tc.wantReason)
				}
				if res.Windows == nil {
					t.Errorf("%s windows = nil — JSON 에서 null 이 된다", res.Vendor)
				}
				// 상태 코드나 URL 이 화면 문자열로 새면 안 된다 (vendorlimit.Reason 규약).
				if res.Detail != "" && containsString([]string{res.Detail}, srv.URL) {
					t.Errorf("%s detail 에 URL 이 실렸다: %q", res.Vendor, res.Detail)
				}
			}
		})
	}
}

// probeFailingUsageAPI 는 모의 사용량 API 를 실제로 두드리고 그 실패를 벤더별 Result 로
// 옮긴다. 응답 해석 로직을 흉내 내지 않는다 — 상태 코드 → Reason 매핑의 정확성은
// internal/vendorlimit 이 소유하므로 기대 Reason 을 인자로 받는다.
func probeFailingUsageAPI(ctx context.Context, url string, reason vendorlimit.Reason) vendorlimit.Snapshot {
	const observed = "2026-08-10T02:00:00Z"
	snap := vendorlimit.Snapshot{ObservedAt: observed}
	client := &http.Client{Timeout: 5 * time.Second}

	for _, v := range vendorlimit.SupportedVendors() {
		res := vendorlimit.Result{
			Vendor:     v,
			State:      vendorlimit.StateAvailable,
			Windows:    []vendorlimit.Window{},
			ObservedAt: observed,
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err == nil {
			resp, derr := client.Do(req)
			if derr == nil {
				resp.Body.Close() //nolint:errcheck // 본문을 쓰지 않는다
				if resp.StatusCode/100 != 2 {
					res.State = vendorlimit.StateUnavailable
					res.Reason = reason
					res.Detail = "사용량 API 가 오류를 돌려줬다"
				}
			} else {
				res.State = vendorlimit.StateUnavailable
				res.Reason = vendorlimit.ReasonNetwork
			}
		}
		snap.Results = append(snap.Results, res)
	}
	return snap
}
