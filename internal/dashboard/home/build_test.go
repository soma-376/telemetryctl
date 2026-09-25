package home

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/dashboard"
	"github.com/your-org/pulsemetry/internal/store"
)

func TestQueryWindows(t *testing.T) {
	for _, tc := range []struct {
		start, end, tz, unit string
		windows              int
		hours                float64
	}{
		{"2026-09-20", "2026-09-20", "Asia/Seoul", "hour", 12, 24},
		{"2026-03-08", "2026-03-08", "America/New_York", "hour", 12, 23},
		{"2026-11-01", "2026-11-01", "America/New_York", "hour", 13, 25},
		{"2026-10-31", "2026-11-01", "America/New_York", "hour", 9, 49},
		{"2026-09-01", "2026-09-30", "Asia/Seoul", "day", 30, 720},
		{"2026-07-01", "2026-08-01", "UTC", "week", 5, 768},
		{"2026-02-15", "2026-07-10", "UTC", "month", 6, 3504},
	} {
		t.Run(tc.start+tc.end+tc.tz, func(t *testing.T) {
			bounds, unit, _, err := queryWindows(Query{TZ: tc.tz, Start: tc.start, End: tc.end})
			if err != nil {
				t.Fatal(err)
			}
			if unit != tc.unit || len(bounds)-1 != tc.windows || bounds[len(bounds)-1].Sub(bounds[0]).Hours() != tc.hours {
				t.Fatalf("경계: %v %s", bounds, unit)
			}
			if bounds[0].Format("2006-01-02") != tc.start || bounds[len(bounds)-1].AddDate(0, 0, -1).Format("2006-01-02") != tc.end {
				t.Fatal("양 끝 범위를 넓혔다")
			}
		})
	}
	for _, q := range []Query{
		{TZ: "Mars/Moon", Start: "2026-01-01", End: "2026-01-01"},
		{Start: "2026-02-30", End: "2026-03-01"}, {Start: "2026-09-02", End: "2026-09-01"},
		{Start: "2025-01-01", End: "2026-02-05"}, {},
	} {
		if ValidateQuery(q) == nil {
			t.Fatalf("잘못된 조건 허용: %+v", q)
		}
	}
	if err := ValidateQuery(Query{Start: "2025-01-01", End: "2026-02-04"}); err != nil {
		t.Fatal("400일 거부:", err)
	}
}

func TestSnapshotAbsentDatabase(t *testing.T) {
	svc := dashboard.NewService(filepath.Join(t.TempDir(), "missing.db"))
	t.Cleanup(func() { _ = svc.Stop() })
	out, err := NewBuilder(svc).Snapshot(context.Background(), Query{TZ: "Asia/Seoul", Start: "2026-09-20", End: "2026-09-20"})
	if err != nil || out.DatabaseAvailable || len(out.Usage.Windows) != 12 || out.Recent == nil || out.ActiveAgents == nil || out.Usage.Vendors == nil {
		t.Fatalf("빈 상태: %+v %v", out, err)
	}
}

func TestSnapshotRecentLimitAndPeriodCounts(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, store.PathIn(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	start := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC).Unix()
	if _, err := db.SQL().Exec(`INSERT INTO vendors VALUES ('codex', ?, ?, 'enabled')`, start, start); err != nil {
		t.Fatal(err)
	}
	for i := range 10 {
		// 9건은 기간 안, 마지막 1건은 기간 밖이지만 현재 진행 중인 세션이다.
		at := start + int64(i)
		if i == 9 {
			at = start - 1
		}
		if _, err := db.SQL().Exec(`INSERT INTO sessions(vendor_id,session_key,started_at,active_time_sec) VALUES ('codex', ?, ?, 60)`, i, at); err != nil {
			t.Fatal(err)
		}
	}
	svc := dashboard.NewService(db.Path())
	t.Cleanup(func() { _ = svc.Stop() })
	out, err := NewBuilder(svc).Snapshot(ctx, Query{TZ: "UTC", Start: "2026-09-20", End: "2026-09-20"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Recent) != 7 || !out.RecentTruncated || out.RunningSessions != 9 || out.Usage.Totals.SessionsStarted != 9 || out.Usage.Totals.ActiveSeconds != 540 || len(out.ActiveAgents) != 1 {
		t.Fatalf("기간·목록 집계: %+v", out)
	}
	for i, row := range out.Recent {
		if row.StartedAt != start+8-int64(i) {
			t.Fatalf("최근순 오류: %+v", out.Recent)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := NewBuilder(svc).Snapshot(canceled, Query{Start: "2026-09-20", End: "2026-09-20"}); err == nil {
		t.Fatal("취소된 조회 성공")
	}
}
