package dashboard

import (
	"context"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/rollup"
	"github.com/your-org/pulsemetry/internal/session"
	"github.com/your-org/pulsemetry/internal/store"
)

// 「Agent 사용 비율」 — rollup_hourly WHERE dim='vendor'.
func TestBreakdownByVendor(t *testing.T) {
	f := newFixture(t)
	at := time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC)
	f.write(store.Batch{Rollups: []rollup.Row{
		rollupRow(at, rollup.DimVendor, "claude_code", rollup.Bucket{CostUSD: 9, Prompts: 30}),
		rollupRow(at, rollup.DimVendor, "codex", rollup.Bucket{CostUSD: 3, Prompts: 10}),
		rollupRow(at, rollup.DimTotal, "", rollup.Bucket{CostUSD: 12, Prompts: 40}),
	}})

	rows, err := f.reader.Breakdown(context.Background(), BreakdownQuery{Dim: DimVendor, TZ: seoul})
	if err != nil {
		t.Fatalf("Breakdown: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("행 = %d, want 2 (%+v)", len(rows), rows)
	}
	if rows[0].Key != "claude_code" || rows[0].CostUSD != 9 {
		t.Errorf("1행 = %+v, want claude_code/9 (비용 내림차순)", rows[0])
	}
	if rows[1].Key != "codex" || rows[1].CostUSD != 3 {
		t.Errorf("2행 = %+v, want codex/3", rows[1])
	}
	if rows[0].Label != rows[0].Key {
		t.Errorf("Label = %q, want Key 와 같음", rows[0].Label)
	}
}

// dim='project' 의 key 는 해시라 그대로 보여 줄 수 없다. sessions 에서 이름을 붙여야 한다.
func TestBreakdownLabelsProjectHash(t *testing.T) {
	f := newFixture(t)
	at := time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC)
	f.write(store.Batch{
		Rollups: []rollup.Row{
			rollupRow(at, rollup.DimProject, "hash-a", rollup.Bucket{CostUSD: 2}),
			rollupRow(at, rollup.DimProject, "hash-unknown", rollup.Bucket{CostUSD: 1}),
		},
		Sessions: []session.Session{
			newSession("s1", testNow.Add(-2*time.Hour), func(s *session.Session) {
				s.ProjectHash = "hash-a"
				s.ProjectName = "telemetryctl"
			}),
		},
	})

	rows, err := f.reader.Breakdown(context.Background(), BreakdownQuery{Dim: DimProject, TZ: seoul})
	if err != nil {
		t.Fatalf("Breakdown: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("행 = %d, want 2", len(rows))
	}
	if rows[0].Label != "telemetryctl" {
		t.Errorf("Label = %q, want telemetryctl", rows[0].Label)
	}
	// 이름을 못 찾으면 해시가 그대로 남는다. 빈 문자열로 두면 화면에 이름 없는 줄이 생긴다.
	if rows[1].Label != "hash-unknown" {
		t.Errorf("이름 없는 프로젝트 Label = %q, want 해시 그대로", rows[1].Label)
	}
}

// 「시간대별 집중도」 — 현지 시각 기준으로 묶여야 한다.
//
// UTC 18:00 은 서울에서 다음 날 03시다. UTC 로 묶는 구현은 18시 칸에 값을 넣어 이 테스트를
// 통과하지 못한다.
func TestBreakdownHourOfDayUsesLocalHours(t *testing.T) {
	f := newFixture(t)
	f.write(store.Batch{Rollups: []rollup.Row{
		rollupRow(time.Date(2026, 8, 9, 18, 0, 0, 0, time.UTC), rollup.DimTotal, "", rollup.Bucket{Prompts: 7}),
		rollupRow(time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC), rollup.DimTotal, "", rollup.Bucket{Prompts: 2}),
	}})
	ctx := context.Background()

	seoulRows, err := f.reader.Breakdown(ctx, BreakdownQuery{TZ: seoul, Days: 2, Bucket: BucketHourOfDay})
	if err != nil {
		t.Fatalf("Breakdown(Seoul): %v", err)
	}
	if len(seoulRows) != 24 {
		t.Fatalf("행 = %d, want 24 (빈 시간대도 채워야 한다)", len(seoulRows))
	}
	if seoulRows[3].Key != "03" || seoulRows[3].Prompts != 7 {
		t.Errorf("서울 03시 = %+v, want prompts=7", seoulRows[3])
	}
	if seoulRows[10].Prompts != 2 {
		t.Errorf("서울 10시 prompts = %d, want 2", seoulRows[10].Prompts)
	}
	if seoulRows[18].Prompts != 0 {
		t.Errorf("서울 18시 prompts = %d, want 0 — UTC 시각으로 묶었다", seoulRows[18].Prompts)
	}

	utcRows, err := f.reader.Breakdown(ctx, BreakdownQuery{TZ: utc, Days: 2, Bucket: BucketHourOfDay})
	if err != nil {
		t.Fatalf("Breakdown(UTC): %v", err)
	}
	if utcRows[18].Prompts != 7 {
		t.Errorf("UTC 18시 prompts = %d, want 7", utcRows[18].Prompts)
	}
}

func TestBreakdownByDayFillsGaps(t *testing.T) {
	f := newFixture(t)
	f.write(store.Batch{Rollups: []rollup.Row{
		// 서울 기준 08-10 (오늘)
		rollupRow(time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC), rollup.DimTotal, "", rollup.Bucket{CostUSD: 3}),
		// 서울 기준 08-08
		rollupRow(time.Date(2026, 8, 8, 3, 0, 0, 0, time.UTC), rollup.DimTotal, "", rollup.Bucket{CostUSD: 1}),
	}})

	rows, err := f.reader.Breakdown(context.Background(), BreakdownQuery{TZ: seoul, Days: 3, Bucket: BucketDay})
	if err != nil {
		t.Fatalf("Breakdown: %v", err)
	}
	want := []struct {
		key  string
		cost float64
	}{
		{"2026-08-08", 1},
		{"2026-08-09", 0}, // 값이 없는 날도 행이 있어야 그래프가 밀리지 않는다
		{"2026-08-10", 3},
	}
	if len(rows) != len(want) {
		t.Fatalf("행 = %d, want %d (%+v)", len(rows), len(want), rows)
	}
	for i, w := range want {
		if rows[i].Key != w.key || rows[i].CostUSD != w.cost {
			t.Errorf("%d행 = %s/%v, want %s/%v", i, rows[i].Key, rows[i].CostUSD, w.key, w.cost)
		}
		if rows[i].StartAt == 0 {
			t.Errorf("%d행 StartAt = 0 — 날짜 축은 구간 시작을 줘야 한다", i)
		}
	}
}

func TestBreakdownRejectsUnknownInput(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, err := f.reader.Breakdown(ctx, BreakdownQuery{Dim: "vender"}); err == nil {
		t.Error("알 수 없는 dim 이 에러를 내지 않았다 — 오타가 '데이터 없음' 으로 보인다")
	}
	if _, err := f.reader.Breakdown(ctx, BreakdownQuery{Bucket: "weekly"}); err == nil {
		t.Error("알 수 없는 bucket 이 에러를 내지 않았다")
	}
	if _, err := f.reader.Breakdown(ctx, BreakdownQuery{TZ: "Mars/Phobos"}); err == nil {
		t.Error("잘못된 시간대가 에러를 내지 않았다")
	}
}

// dim 을 비우면 total 로 본다 — 가장 흔한 호출이 기본값이어야 한다.
func TestBreakdownDefaultsToTotal(t *testing.T) {
	f := newFixture(t)
	f.write(store.Batch{Rollups: []rollup.Row{
		rollupRow(time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC), rollup.DimTotal, "", rollup.Bucket{CostUSD: 5}),
		rollupRow(time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC), rollup.DimVendor, "codex", rollup.Bucket{CostUSD: 5}),
	}})

	rows, err := f.reader.Breakdown(context.Background(), BreakdownQuery{TZ: seoul})
	if err != nil {
		t.Fatalf("Breakdown: %v", err)
	}
	if len(rows) != 1 || rows[0].Key != "" || rows[0].CostUSD != 5 {
		t.Fatalf("행 = %+v, want total 한 줄", rows)
	}
}
