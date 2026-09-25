package home

import (
	"context"
	"fmt"
	"time"

	"github.com/your-org/pulsemetry/internal/dashboard"
)

type Builder struct{ source *dashboard.Service }

func NewBuilder(source *dashboard.Service) *Builder { return &Builder{source: source} }

// Snapshot은 같은 WAL 스냅샷에서 차트·합계·최근 활동을 읽는다.
func (b *Builder) Snapshot(ctx context.Context, q Query) (Snapshot, error) {
	boundaries, unit, size, err := queryWindows(q)
	if err != nil {
		return Snapshot{}, err
	}
	out := Snapshot{EndDate: q.End, Unit: unit, BucketSize: size}
	err = b.source.ReadSnapshot(ctx, func(db dashboard.SQLQuerier) error {
		out.DatabaseAvailable = db != nil
		var err error
		out.Usage, err = dashboard.ReadUsageBreakdown(ctx, db, boundaries)
		if err != nil {
			return err
		}
		start, end := boundaries[0], boundaries[len(boundaries)-1]
		out.Recent, out.RecentTruncated, err = dashboard.ReadRecentSessions(ctx, db, start, end, 7)
		if err != nil {
			return err
		}
		out.ActiveAgents, _, err = dashboard.ReadActiveAgents(ctx, db)
		if err != nil || db == nil {
			return err
		}
		return db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions
 WHERE started_at >= ? AND started_at < ? AND ended_at IS NULL`, start.Unix(), end.Unix()).Scan(&out.RunningSessions)
	})
	if err != nil {
		return Snapshot{}, err
	}
	return out, nil
}

// ValidateQuery는 HTTP 경계에서도 동일한 날짜·시간대 검증을 사용하게 한다.
func ValidateQuery(q Query) error {
	_, _, _, err := queryWindows(q)
	return err
}

func queryWindows(q Query) ([]time.Time, string, int, error) {
	loc, err := dashboard.LoadLocation(q.TZ)
	if err != nil {
		return nil, "", 0, err
	}
	start, err := time.ParseInLocation("2006-01-02", q.Start, loc)
	if err != nil {
		return nil, "", 0, fmt.Errorf("home: 시작 날짜가 잘못되었다")
	}
	last, err := time.ParseInLocation("2006-01-02", q.End, loc)
	if err != nil || last.Before(start) || last.After(start.AddDate(0, 0, 399)) {
		return nil, "", 0, fmt.Errorf("home: 조회 기간은 1~400일이어야 한다")
	}
	days := 1
	for d := start; d.Before(last); d = d.AddDate(0, 0, 1) {
		days++
	}
	unit, size := "day", 1
	switch {
	case days == 1:
		unit, size = "hour", 2
	case days == 2:
		unit, size = "hour", 6
	case days <= 31:
	case days <= 120:
		unit, size = "week", 7
	default:
		unit, size = "month", 1
	}
	end := last.AddDate(0, 0, 1)
	boundaries := []time.Time{start}
	for cur := start; cur.Before(end); {
		next := cur.AddDate(0, 0, size)
		if unit == "hour" {
			next = cur.Add(time.Duration(size) * time.Hour)
			// DST 전환일의 마지막 창을 자정에서 끊어 다음 날의 축을 유지한다.
			midnight := time.Date(cur.Year(), cur.Month(), cur.Day()+1, 0, 0, 0, 0, loc)
			if next.After(midnight) {
				next = midnight
			}
		}
		if unit == "month" {
			next = time.Date(cur.Year(), cur.Month()+1, 1, 0, 0, 0, 0, loc)
		}
		if next.After(end) {
			next = end
		}
		boundaries = append(boundaries, next)
		cur = next
	}
	return boundaries, unit, size, nil
}
