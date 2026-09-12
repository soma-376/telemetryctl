package dashboard_test

import (
	"context"
	"reflect"
	"testing"

	. "github.com/your-org/pulsemetry/internal/dashboard"
	"github.com/your-org/pulsemetry/internal/dashboard/activity"
	"github.com/your-org/pulsemetry/internal/session"
	"github.com/your-org/pulsemetry/internal/store"
)

func TestActivityRunningFirstOrderAndCursor(t *testing.T) {
	f := TestNewFixture(t)
	ctx := context.Background()
	for _, row := range []struct {
		key                    string
		started, active, ended any
	}{
		{"completed-new", 500, 700, 800},
		{"running-old", 100, 900, nil},
		{"running-new", 600, 650, nil},
		{"running-tie", 200, 900, nil},
		{"running-unknown", nil, nil, nil},
		{"completed-old", 50, 950, 950},
		{"completed-unknown", nil, nil, 950},
	} {
		f.TestWrite(store.Batch{Sessions: []session.Session{TestNewSession(row.key, TestNow)}})
		if _, err := f.TestDB().SQL().ExecContext(ctx, `UPDATE sessions SET title=session_key, started_at=?, last_activity_at=?, ended_at=? WHERE session_key=?`, row.started, row.active, row.ended, row.key); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"running-tie", "running-old", "running-new", "running-unknown", "completed-new", "completed-old", "completed-unknown"}
	for _, limit := range []int{1, 2, 3, 20} {
		rows, _ := drainActivity(t, f.TestReader(), activity.Query{Limit: limit})
		if got := activityKeys(rows); !reflect.DeepEqual(got, want) {
			t.Fatalf("limit=%d keys=%v want=%v", limit, got, want)
		}
	}
	for _, tc := range []struct {
		query activity.Query
		want  []string
	}{
		{activity.Query{Status: []string{StatusRunning}, Limit: 1}, want[:4]},
		{activity.Query{Status: []string{StatusCompleted}, Limit: 1}, want[4:]},
		// 기간은 최근 활동이 아닌 시작 시각에 적용한다.
		{activity.Query{Since: 500, Until: 601, Limit: 1}, []string{"running-new", "completed-new"}},
		{activity.Query{Text: "running", Limit: 1}, want[:4]},
	} {
		rows, _ := drainActivity(t, f.TestReader(), tc.query)
		if got := activityKeys(rows); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("query=%+v keys=%v want=%v", tc.query, got, tc.want)
		}
	}
	// 마감·재개 후 첫 페이지 재조회는 새 상태와 활동 시각을 반영한다.
	if _, err := f.TestDB().SQL().ExecContext(ctx, `UPDATE sessions SET ended_at=950 WHERE session_key='running-tie'`); err != nil {
		t.Fatal(err)
	}
	page, err := readActivity(f.TestPath(), ctx, activity.Query{Limit: 1})
	if err != nil || len(page.Rows) != 1 || page.Rows[0].SessionKey != "running-old" {
		t.Fatalf("after close: page=%+v err=%v", page, err)
	}
	if _, err := f.TestDB().SQL().ExecContext(ctx, `UPDATE sessions SET ended_at=NULL, last_activity_at=1000 WHERE session_key='completed-old'`); err != nil {
		t.Fatal(err)
	}
	page, err = readActivity(f.TestPath(), ctx, activity.Query{Limit: 1})
	if err != nil || len(page.Rows) != 1 || page.Rows[0].SessionKey != "completed-old" {
		t.Fatalf("after resume: page=%+v err=%v", page, err)
	}
}
