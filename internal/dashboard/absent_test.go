package dashboard

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/store"
)

// 이 파일이 **DB 부재 계약** 이다 (ADR 0004).
//
// Wails v3 서비스의 ServiceStartup 이 error 를 반환하면 앱 기동 자체가 중단된다. 그런데
// 아직 enroll 하지 않았거나 데몬을 한 번도 켜지 않은 사용자에게는 DB 파일이 없다. 그래서
// 미설치는 실패가 아니라 빈 결과여야 하고, **모든 조회 메서드가 예외 없이** 그래야 한다.
//
// 아래 표가 Reader 의 조회 메서드 전부를 덮는다. 메서드를 추가하면 여기에도 한 줄을
// 더해야 한다 — TestAbsentTableCoversEveryQueryMethod 가 빠뜨림을 잡는다.

// absentCase 는 DB 없이 불렀을 때의 기대다.
type absentCase struct {
	// method 는 Reader 의 메서드 이름이다. 리플렉션으로 표의 완전성을 검사한다.
	method string
	// call 은 실제 호출이다. 결과를 검사할 수 있게 값을 그대로 돌려준다.
	call func(context.Context, *Reader) (any, error)
	// check 는 빈 결과의 모양을 확인한다.
	check func(*testing.T, any)
}

func absentCases() []absentCase {
	return []absentCase{
		{
			method: "Today",
			call:   func(ctx context.Context, r *Reader) (any, error) { return r.Today(ctx, seoul) },
			check: func(t *testing.T, got any) {
				sum := got.(TodaySummary)
				if sum.Today.CostUSD != 0 || sum.Yesterday.CostUSD != 0 {
					t.Errorf("합계가 0 이 아니다: %+v", sum)
				}
				// 카드 모양은 유지해야 화면이 분기 없이 그린다.
				if len(sum.Cards) != 4 {
					t.Errorf("카드 = %d, want 4", len(sum.Cards))
				}
				if sum.Date == "" || sum.TZ != seoul {
					t.Errorf("날짜·시간대가 비었다: %+v", sum)
				}
				if sum.ActiveAgents == nil {
					t.Error("ActiveAgents 가 nil — JSON 에서 null 이 된다")
				}
			},
		},
		{
			method: "RecentActivity",
			call: func(ctx context.Context, r *Reader) (any, error) {
				return r.RecentActivity(ctx, RecentQuery{TZ: seoul})
			},
			check: func(t *testing.T, got any) {
				act := got.(RecentActivity)
				if act.Sessions == nil || act.ActiveAgents == nil {
					t.Error("슬라이스가 nil — JSON 에서 null 이 되어 프런트엔드가 터진다")
				}
				if act.Date == "" || act.TZ != seoul {
					t.Errorf("날짜·시간대가 비었다: %+v", act)
				}
			},
		},
		{
			method: "Home",
			call: func(ctx context.Context, r *Reader) (any, error) {
				return r.Home(ctx, HomeQuery{TZ: seoul})
			},
			check: func(t *testing.T, got any) {
				sum := got.(HomeSummary)
				if len(sum.Cards) != 4 {
					t.Errorf("카드 = %d, want 4", len(sum.Cards))
				}
				// 빈 날짜도 창 골격은 유지해야 화면이 분기 없이 그린다.
				if len(sum.TwoHour.Windows) != 12 || sum.TwoHour.ActiveWindows != 0 {
					t.Errorf("2시간 창 = %d개/활동 %d개, want 12/0",
						len(sum.TwoHour.Windows), sum.TwoHour.ActiveWindows)
				}
				if sum.Recent == nil || sum.ActiveAgents == nil {
					t.Error("슬라이스가 nil — JSON 에서 null 이 되어 프런트엔드가 터진다")
				}
				if sum.Date == "" || sum.TZ != seoul {
					t.Errorf("날짜·시간대가 비었다: %+v", sum)
				}
			},
		},
		{
			method: "HomeBreakdown",
			call: func(ctx context.Context, r *Reader) (any, error) {
				return r.HomeBreakdown(ctx, HomeBreakdownQuery{TZ: seoul})
			},
			check: func(t *testing.T, got any) {
				b := got.(HomeBreakdown)
				// 빈 날에도 창 골격은 유지해야 화면이 분기 없이 그린다.
				if len(b.Windows) != 12 {
					t.Errorf("창 = %d개, want 12", len(b.Windows))
				}
				for i, w := range b.Windows {
					if w.Vendors == nil {
						t.Errorf("창 %d 의 Vendors 가 nil — JSON 에서 null 이 된다", i)
					}
					if w.Active || w.Tokens != 0 || w.Cost.NanoUSD != 0 {
						t.Errorf("창 %d 가 비어 있지 않다: %+v", i, w)
					}
				}
				if b.Vendors == nil {
					t.Error("Vendors 가 nil — JSON 에서 null 이 되어 프런트엔드가 터진다")
				}
				// 사용량이 없는 날에 아무 창이나 최고 시간대로 고르면 안 된다.
				if b.Peak.Found || b.Peak.Index != -1 {
					t.Errorf("Peak = %+v, want 없음/-1", b.Peak)
				}
				if b.Date == "" || b.TZ != seoul {
					t.Errorf("날짜·시간대가 비었다: %+v", b)
				}
			},
		},
		{
			method: "Sessions",
			call:   func(ctx context.Context, r *Reader) (any, error) { return r.Sessions(ctx, SessionQuery{}) },
			check: func(t *testing.T, got any) {
				rows := got.([]SessionRow)
				if rows == nil || len(rows) != 0 {
					t.Errorf("Sessions = %v, want 빈 슬라이스", rows)
				}
			},
		},
		{
			method: "Session",
			call:   func(ctx context.Context, r *Reader) (any, error) { return r.Session(ctx, 42) },
			check: func(t *testing.T, got any) {
				d := got.(SessionDetail)
				if d.Found {
					t.Error("Found = true")
				}
				if d.Files == nil || d.Tools == nil || d.MCP == nil {
					t.Error("하위 슬라이스가 nil — JSON 에서 null 이 되어 프런트엔드가 터진다")
				}
			},
		},
		{
			method: "Breakdown",
			call: func(ctx context.Context, r *Reader) (any, error) {
				return r.Breakdown(ctx, BreakdownQuery{Dim: DimVendor, TZ: seoul})
			},
			check: func(t *testing.T, got any) {
				rows := got.([]Row)
				if rows == nil || len(rows) != 0 {
					t.Errorf("Breakdown = %v, want 빈 슬라이스", rows)
				}
			},
		},
		{
			method: "Search",
			call:   func(ctx context.Context, r *Reader) (any, error) { return r.Search(ctx, SearchQuery{Text: "토큰"}) },
			check: func(t *testing.T, got any) {
				hits := got.([]Hit)
				if hits == nil || len(hits) != 0 {
					t.Errorf("Search = %v, want 빈 슬라이스", hits)
				}
			},
		},
		{
			method: "Activity",
			call: func(ctx context.Context, r *Reader) (any, error) {
				return r.Activity(ctx, ActivityQuery{Text: "토큰"})
			},
			check: func(t *testing.T, got any) {
				page := got.(ActivityPage)
				if page.Rows == nil || len(page.Rows) != 0 {
					t.Errorf("Activity.Rows = %v, want 빈 슬라이스", page.Rows)
				}
				if page.HasMore {
					t.Error("HasMore = true — 미설치인데 다음 페이지가 있다고 한다")
				}
				if page.NextCursor.ID != 0 {
					t.Errorf("NextCursor = %+v, want 빈 커서", page.NextCursor)
				}
			},
		},
		{
			method: "Vendors",
			call:   func(ctx context.Context, r *Reader) (any, error) { return r.Vendors(ctx) },
			check: func(t *testing.T, got any) {
				rows := got.([]VendorStatus)
				if rows == nil || len(rows) != 0 {
					t.Errorf("Vendors = %v, want 빈 슬라이스", rows)
				}
			},
		},
		{
			method: "MCPUsage",
			call:   func(ctx context.Context, r *Reader) (any, error) { return r.MCPUsage(ctx, 14) },
			check: func(t *testing.T, got any) {
				rows := got.([]MCPRow)
				if rows == nil || len(rows) != 0 {
					t.Errorf("MCPUsage = %v, want 빈 슬라이스", rows)
				}
			},
		},
		{
			method: "Status",
			call:   func(ctx context.Context, r *Reader) (any, error) { return r.Status(ctx) },
			check: func(t *testing.T, st any) {
				s := st.(Status)
				if s.Available {
					t.Error("Available = true")
				}
				if s.LatestSchemaVersion != store.LatestSchemaVersion() {
					t.Errorf("LatestSchemaVersion = %d", s.LatestSchemaVersion)
				}
				// 모든 슬라이스가 JSON 에서 [] 여야 한다.
				b, err := json.Marshal(s)
				if err != nil {
					t.Fatalf("Marshal: %v", err)
				}
				if got := string(b); containsAny(got, `"active_vendors":null`, `"listen_addrs":null`) {
					t.Errorf("JSON 에 null 슬라이스가 있다: %s", got)
				}
			},
		},
		{
			method: "WorkspaceFolder",
			call: func(ctx context.Context, r *Reader) (any, error) {
				return r.WorkspaceFolder(ctx, 42)
			},
			check: func(t *testing.T, got any) {
				f := got.(WorkspaceFolder)
				// 미설치는 "열 수 없다" 이지 오류가 아니다. 사유는 기계 판독 가능해야 한다.
				if f.Openable || f.Reason != OpenReasonSessionNotFound {
					t.Errorf("WorkspaceFolder = %+v, want 열 수 없음/session_not_found", f)
				}
				// 열 수 없는 결과에 경로가 실리면 화면이 그것을 다시 어딘가로 넘길 수 있다.
				if f.Path != "" {
					t.Errorf("Path = %q, want 빈 문자열", f.Path)
				}
			},
		},
		{
			method: "SessionMetrics",
			call: func(ctx context.Context, r *Reader) (any, error) {
				return r.SessionMetrics(ctx, SessionMetricsQuery{SessionID: 42})
			},
			check: func(t *testing.T, got any) {
				m := got.(SessionMetrics)
				if m.Found {
					t.Error("Found = true")
				}
				if m.Turns == nil {
					t.Error("Turns 가 nil — JSON 에서 null 이 되어 프런트엔드가 터진다")
				}
				// 상한은 DB 가 없어도 응답에 있어야 한다. 화면이 분기 없이 그린다.
				if m.TurnLimit != defaultSessionTurns {
					t.Errorf("TurnLimit = %d, want %d", m.TurnLimit, defaultSessionTurns)
				}
			},
		},
		{
			method: "FileChanges",
			call:   func(ctx context.Context, r *Reader) (any, error) { return r.FileChanges(ctx, 42) },
			check: func(t *testing.T, got any) {
				fc := got.(SessionFileChanges)
				if fc.Found {
					t.Error("Found = true")
				}
				if fc.Files == nil {
					t.Error("Files 가 nil — JSON 에서 null 이 되어 프런트엔드가 터진다")
				}
				// 변경을 하나도 못 본 상태의 줄 수는 0 이 아니라 미관측이다. 미설치에서
				// 0 을 돌려주면 화면이 "오늘 0줄 바꿨다" 를 사실처럼 그린다.
				if fc.Totals.Additions.Observed() || fc.Totals.Deletions.Observed() {
					t.Errorf("DB 가 없는데 줄 수가 관측됨으로 나왔다: %+v", fc.Totals)
				}
			},
		},
	}
}

func TestOpenWithoutDatabaseIsNotError(t *testing.T) {
	path := store.PathIn(t.TempDir())

	r, err := Open(path)
	if err != nil {
		t.Fatalf("DB 가 없다고 Open 이 실패했다: %v", err)
	}
	t.Cleanup(func() { r.Close() }) //nolint:errcheck // 테스트 정리
	r.now = func() time.Time { return testNow }

	if r.Available() {
		t.Fatal("Available = true — 파일이 없는데 열렸다고 한다")
	}
	if r.Path() != path {
		t.Errorf("Path = %q, want %q", r.Path(), path)
	}

	ctx := context.Background()
	for _, tc := range absentCases() {
		t.Run(tc.method, func(t *testing.T) {
			got, err := tc.call(ctx, r)
			if err != nil {
				t.Fatalf("%s: %v", tc.method, err)
			}
			tc.check(t, got)
		})
	}

	t.Run("잘못된 시간대는 DB 가 없어도 에러", func(t *testing.T) {
		if _, err := r.Today(ctx, "Mars/Phobos"); err == nil {
			t.Error("에러가 없다 — 시간대 오타는 미설치와 무관하게 잘못된 입력이다")
		}
	})
	t.Run("잘못된 집계 축은 DB 가 없어도 에러", func(t *testing.T) {
		if _, err := r.Breakdown(ctx, BreakdownQuery{Dim: "vender"}); err == nil {
			t.Error("에러가 없다 — 오타를 '데이터 없음' 으로 보이게 하면 안 된다")
		}
	})
}

// 표가 조회 메서드를 하나라도 빠뜨리면 그 메서드는 DB 부재에서 검증되지 않는다.
// 리플렉션으로 Reader 의 공개 메서드를 훑어 표와 대조한다.
func TestAbsentTableCoversEveryQueryMethod(t *testing.T) {
	// 조회가 아닌 메서드다. 생명주기·좌표를 돌려줄 뿐이라 빈 결과 계약의 대상이 아니다.
	lifecycle := map[string]bool{
		"Reopen": true, "Close": true, "Available": true, "Path": true, "DataDir": true,
	}

	covered := map[string]bool{}
	for _, tc := range absentCases() {
		covered[tc.method] = true
	}

	rt := reflect.TypeOf(&Reader{})
	for i := range rt.NumMethod() {
		name := rt.Method(i).Name
		if lifecycle[name] || covered[name] {
			continue
		}
		t.Errorf("Reader.%s 가 DB 부재 표에 없다 — 미설치에서의 동작이 검증되지 않는다", name)
	}
	for name := range covered {
		if _, ok := rt.MethodByName(name); !ok {
			t.Errorf("표의 %q 는 Reader 의 메서드가 아니다", name)
		}
	}
}

// 데몬이 나중에 DB 를 만들면 다시 붙을 수 있어야 한다. 없으면 사용자는 앱을 껐다 켜야 한다.
func TestReopenAfterDatabaseAppears(t *testing.T) {
	dir := t.TempDir()
	path := store.PathIn(dir)

	r, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { r.Close() }) //nolint:errcheck // 테스트 정리
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
	defer db.Close() //nolint:errcheck // 테스트 정리

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
