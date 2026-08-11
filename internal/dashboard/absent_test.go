package dashboard

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/store"
)

// 계획서 「GUI 연동 형태」: ServiceStartup 이 error 를 내면 앱 기동이 통째로 중단되므로
// 미설치 상태(DB 없음)는 error 가 아니라 빈 결과로 다뤄야 한다.
func TestOpenWithoutDatabaseIsNotError(t *testing.T) {
	path := store.PathIn(t.TempDir())

	r, err := Open(path)
	if err != nil {
		t.Fatalf("DB 가 없다고 Open 이 실패했다: %v", err)
	}
	t.Cleanup(func() { r.Close() })
	r.now = func() time.Time { return testNow }

	if r.Available() {
		t.Fatal("Available = true — 파일이 없는데 열렸다고 한다")
	}
	if r.Path() != path {
		t.Errorf("Path = %q, want %q", r.Path(), path)
	}

	ctx := context.Background()

	t.Run("Today", func(t *testing.T) {
		got, err := r.Today(ctx, seoul)
		if err != nil {
			t.Fatalf("Today: %v", err)
		}
		if got.Today.CostUSD != 0 || got.Yesterday.CostUSD != 0 {
			t.Errorf("합계가 0 이 아니다: %+v", got)
		}
		// 카드 모양은 유지해야 화면이 분기 없이 그린다.
		if len(got.Cards) != 4 {
			t.Errorf("카드 = %d, want 4", len(got.Cards))
		}
		if got.Date == "" || got.TZ != seoul {
			t.Errorf("날짜·시간대가 비었다: %+v", got)
		}
		if got.ActiveAgents == nil {
			t.Error("ActiveAgents 가 nil — JSON 에서 null 이 된다")
		}
	})

	t.Run("Sessions", func(t *testing.T) {
		rows, err := r.Sessions(ctx, SessionQuery{})
		if err != nil {
			t.Fatalf("Sessions: %v", err)
		}
		if len(rows) != 0 || rows == nil {
			t.Errorf("Sessions = %v, want 빈 슬라이스", rows)
		}
	})

	t.Run("Session", func(t *testing.T) {
		got, err := r.Session(ctx, "없는-세션")
		if err != nil {
			t.Fatalf("Session: %v", err)
		}
		if got.Found {
			t.Error("Found = true")
		}
	})

	t.Run("Breakdown", func(t *testing.T) {
		rows, err := r.Breakdown(ctx, BreakdownQuery{Dim: DimVendor, TZ: seoul})
		if err != nil {
			t.Fatalf("Breakdown: %v", err)
		}
		if len(rows) != 0 {
			t.Errorf("Breakdown = %v", rows)
		}
		// 시간 축은 골격을 유지한다 — 빈 그래프와 없는 그래프는 다르다.
		hours, err := r.Breakdown(ctx, BreakdownQuery{TZ: seoul, Bucket: BucketHourOfDay})
		if err != nil {
			t.Fatalf("Breakdown(hour): %v", err)
		}
		if len(hours) != 24 {
			t.Errorf("시간 축 = %d행, want 24", len(hours))
		}
	})

	t.Run("Search", func(t *testing.T) {
		hits, err := r.Search(ctx, SearchQuery{Text: "토큰"})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(hits) != 0 || hits == nil {
			t.Errorf("Search = %v", hits)
		}
	})

	t.Run("Vendors", func(t *testing.T) {
		rows, err := r.Vendors(ctx)
		if err != nil {
			t.Fatalf("Vendors: %v", err)
		}
		if len(rows) != 0 || rows == nil {
			t.Errorf("Vendors = %v", rows)
		}
	})

	t.Run("MCPUsage", func(t *testing.T) {
		rows, err := r.MCPUsage(ctx, 14)
		if err != nil {
			t.Fatalf("MCPUsage: %v", err)
		}
		if len(rows) != 0 || rows == nil {
			t.Errorf("MCPUsage = %v", rows)
		}
	})

	t.Run("Status", func(t *testing.T) {
		st, err := r.Status(ctx)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if st.Available {
			t.Error("Available = true")
		}
		if st.DatabasePath != path {
			t.Errorf("DatabasePath = %q", st.DatabasePath)
		}
		if st.LatestSchemaVersion != store.LatestSchemaVersion() {
			t.Errorf("LatestSchemaVersion = %d", st.LatestSchemaVersion)
		}
		// 모든 슬라이스가 JSON 에서 [] 여야 한다.
		b, err := json.Marshal(st)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		if got := string(b); containsAny(got, `"active_vendors":null`, `"listen_addrs":null`) {
			t.Errorf("JSON 에 null 슬라이스가 있다: %s", got)
		}
	})

	t.Run("잘못된 시간대는 DB 가 없어도 에러", func(t *testing.T) {
		if _, err := r.Today(ctx, "Mars/Phobos"); err == nil {
			t.Error("에러가 없다 — 시간대 오타는 미설치와 무관하게 잘못된 입력이다")
		}
	})
}

// 데몬이 나중에 DB 를 만들면 다시 붙을 수 있어야 한다. 없으면 사용자는 앱을 껐다 켜야 한다.
func TestReopenAfterDatabaseAppears(t *testing.T) {
	dir := t.TempDir()
	path := store.PathIn(dir)

	r, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { r.Close() })
	if r.Available() {
		t.Fatal("Available = true")
	}
	// 아직 없을 때 Reopen 은 조용히 아무것도 안 한다.
	if err := r.Reopen(); err != nil {
		t.Fatalf("Reopen(파일 없음): %v", err)
	}
	if r.Available() {
		t.Fatal("파일이 없는데 Available = true")
	}

	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer db.Close()

	if err := r.Reopen(); err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	if !r.Available() {
		t.Fatal("DB 가 생겼는데 Available = false")
	}
	if _, err := r.Status(context.Background()); err != nil {
		t.Fatalf("Status: %v", err)
	}
}

func TestOpenRejectsEmptyPath(t *testing.T) {
	for _, p := range []string{"", "   "} {
		if _, err := Open(p); err == nil {
			t.Errorf("Open(%q) 가 에러를 내지 않았다 — 경로 없음은 미설치가 아니라 호출자 버그다", p)
		}
	}
}

// 디렉터리를 DB 파일로 지목한 경우처럼 진짜 실패는 에러여야 한다. 부재와 고장은 다르다.
func TestOpenReportsRealFailures(t *testing.T) {
	dir := t.TempDir()
	bogus := filepath.Join(dir, "pulsemetry.db")
	if err := os.Mkdir(bogus, 0o700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if _, err := Open(bogus); err == nil {
		t.Error("디렉터리를 DB 로 열었는데 에러가 없다")
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
