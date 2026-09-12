package localapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/your-org/pulsemetry/internal/dashboard/tray"
	"github.com/your-org/pulsemetry/internal/vendorlimit"
)

type fakeSource struct {
	got  tray.Query
	snap tray.Snapshot
	err  error
}

func (f *fakeSource) Snapshot(_ context.Context, q tray.Query) (tray.Snapshot, error) {
	f.got = q
	return f.snap, f.err
}

type fakeRefresher struct {
	calls  int
	manual int
}

func (f *fakeRefresher) RefreshManual(context.Context) error {
	f.calls++
	f.manual++
	return nil
}

func TestServerTrayCarriesQueryAndSnapshot(t *testing.T) {
	src := &fakeSource{snap: tray.Snapshot{
		TZ:         "Asia/Seoul",
		Date:       "2026-08-30",
		Monitoring: tray.Monitoring{State: tray.StateMonitoring},
		Limits: []vendorlimit.Result{{
			Vendor: vendorlimit.VendorClaudeCode, State: vendorlimit.StateAvailable,
			Windows: []vendorlimit.Window{{Period: vendorlimit.PeriodFiveHour, UsedRatio: .17}},
		}},
	}}
	ref := &fakeRefresher{}
	srv := httptest.NewServer(NewServer(ref, src))
	defer srv.Close()

	resp, err := http.Get(srv.URL + TrayPath + "?tz=Asia/Seoul&recent_limit=7")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	if ref.calls != 0 {
		t.Fatal("스냅샷 조회가 벤더를 갱신했다")
	}

	// 조회 조건이 쿼리 파라미터로 건너가야 한다. 빠지면 데몬이 늘 UTC 기본값으로 답한다.
	if src.got.TZ != "Asia/Seoul" || src.got.RecentLimit != 7 {
		t.Errorf("데몬이 받은 조건 = %+v", src.got)
	}

	var got tray.Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Date != "2026-08-30" || got.Monitoring.State != tray.StateMonitoring {
		t.Errorf("스냅샷이 온전히 오지 않았다: %+v", got)
	}
	if len(got.Limits) != 1 || got.Limits[0].Windows[0].UsedRatio != .17 {
		t.Errorf("한도가 왕복에서 깨졌다: %+v", got.Limits)
	}
}

// 조회 조건 오류는 4xx 여야 한다. 5xx 로 올리면 GUI 가 "데몬이 고장났다" 로 읽고
// 마지막 정상 스냅샷을 Stale 로 덮는다.
func TestServerTrayReportsBadQueryAsClientError(t *testing.T) {
	src := &fakeSource{err: context.DeadlineExceeded}
	srv := httptest.NewServer(NewServer(&fakeRefresher{}, src))
	defer srv.Close()

	resp, err := http.Get(srv.URL + TrayPath + "?tz=Mars/Phobos")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestServerRefreshReturnsUpdatedSnapshot(t *testing.T) {
	ref := &fakeRefresher{}
	src := &fakeSource{snap: tray.Snapshot{TZ: "Asia/Seoul", Date: "2026-08-31"}}
	srv := httptest.NewServer(NewServer(ref, src))
	defer srv.Close()

	resp, err := http.Post(srv.URL+TrayRefreshPath+"?tz=Asia/Seoul&recent_limit=7", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK || ref.calls != 1 {
		t.Errorf("status = %d, calls = %d", resp.StatusCode, ref.calls)
	}
	if src.got.TZ != "Asia/Seoul" || src.got.RecentLimit != 7 {
		t.Errorf("데몬이 받은 조건 = %+v", src.got)
	}
	var got tray.Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Date != "2026-08-31" {
		t.Errorf("갱신 응답 = %+v", got)
	}
}

// GET 경로에 POST 를 던지면 mux 가 405 로 끊는다. receiver 가 메서드를 안 보기 때문에
// 이 판정은 여기 있어야 한다.
func TestServerRejectsWrongMethod(t *testing.T) {
	srv := httptest.NewServer(NewServer(&fakeRefresher{}, &fakeSource{}))
	defer srv.Close()

	resp, err := http.Post(srv.URL+TrayPath, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}

// POST는 매개변수와 관계없이 수동 갱신만 요청한다.
func TestServerRefreshIsAlwaysManual(t *testing.T) {
	for _, tc := range []struct {
		name       string
		query      string
		wantManual int
	}{
		{"매개변수 없이 수동", "?tz=UTC&recent_limit=5", 1},
		{"버튼은 수동", "?tz=UTC&recent_limit=5&manual=1", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ref := &fakeRefresher{}
			srv := httptest.NewServer(NewServer(ref, &fakeSource{}))
			defer srv.Close()

			resp, err := http.Post(srv.URL+TrayRefreshPath+tc.query, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close() //nolint:errcheck
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d", resp.StatusCode)
			}
			if ref.calls != 1 || ref.manual != tc.wantManual {
				t.Errorf("calls = %d, manual = %d, want manual %d", ref.calls, ref.manual, tc.wantManual)
			}
		})
	}
}
