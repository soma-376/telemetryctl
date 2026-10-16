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
	// 진행 중은 시작 시각 오름차순(미상은 0이라 맨 앞), 종료는 내림차순이다 (ADR 0027).
	want := []string{"running-unknown", "running-old", "running-tie", "running-new", "completed-new", "completed-old", "completed-unknown"}
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
	// 마감·재개 후 재조회는 새 상태를 반영한다. 순서는 시작 시각이 정하므로 마감된 줄이
	// 진행 중 묶음에서 빠지고 재개된 줄이 그 묶음의 제자리로 들어가는지 본다.
	if _, err := f.TestDB().SQL().ExecContext(ctx, `UPDATE sessions SET ended_at=950 WHERE session_key='running-tie'`); err != nil {
		t.Fatal(err)
	}
	rows, _ := drainActivity(t, f.TestReader(), activity.Query{Status: []string{StatusRunning}, Limit: 1})
	if got := activityKeys(rows); !reflect.DeepEqual(got, []string{"running-unknown", "running-old", "running-new"}) {
		t.Fatalf("after close: keys=%v", got)
	}
	if _, err := f.TestDB().SQL().ExecContext(ctx, `UPDATE sessions SET ended_at=NULL, last_activity_at=1000 WHERE session_key='completed-old'`); err != nil {
		t.Fatal(err)
	}
	// completed-old 는 started_at=50 이라 running-unknown(0) 과 running-old(100) 사이다.
	rows, _ = drainActivity(t, f.TestReader(), activity.Query{Status: []string{StatusRunning}, Limit: 1})
	if got := activityKeys(rows); !reflect.DeepEqual(got, []string{"running-unknown", "completed-old", "running-old", "running-new"}) {
		t.Fatalf("after resume: keys=%v", got)
	}
}
