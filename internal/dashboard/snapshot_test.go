package dashboard

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/store"
)

func TestReadSnapshotSurvivesConcurrentCommit(t *testing.T) {
	f := newFixture(t)
	id := seedMetricsSession(f)
	svc := newServiceFor(f.reader, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := svc.ReadSnapshot(ctx, func(db SQLQuerier) error {
		detail, err := ReadSession(ctx, db, id)
		if err != nil {
			return err
		}
		if !detail.Found || detail.Session.InputTokens != 140 {
			t.Fatalf("before: %+v", detail.Session)
		}
		// 첫 조회 뒤 별도 쓰기 연결에서 커밋한다. 읽기 트랜잭션이 쓰기를 막으면 타임아웃된다.
		_, err = f.db.Write(ctx, store.Batch{Events: []store.EventRecord{
			promptRecord("s-metrics", "t-3", metricsAt.Add(2*time.Minute), 8, "새 턴"),
			llmRecord("s-metrics", "t-3", metricsAt.Add(2*time.Minute), 9, llmSpec{Model: "claude-sonnet-4-5", Input: 200, Output: 10}),
		}})
		if err != nil {
			return err
		}
		metrics, err := ReadSessionMetrics(ctx, db, SessionMetricsQuery{SessionID: id})
		if err != nil {
			return err
		}
		classification, err := ClassifySessions(ctx, db, []int64{id})
		if err != nil {
			return err
		}
		if metrics.Totals.LLMCalls != 2 || len(metrics.Turns) != 2 || len(classification[0].Turns) != 2 {
			t.Fatalf("snapshot changed: calls=%d turns=%d classified=%d", metrics.Totals.LLMCalls, len(metrics.Turns), len(classification[0].Turns))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = svc.ReadSnapshot(ctx, func(db SQLQuerier) error {
		detail, err := ReadSession(ctx, db, id)
		if err != nil {
			return err
		}
		metrics, err := ReadSessionMetrics(ctx, db, SessionMetricsQuery{SessionID: id})
		if err != nil {
			return err
		}
		classification, err := ClassifySessions(ctx, db, []int64{id})
		if err != nil {
			return err
		}
		if detail.Session.InputTokens != 340 || metrics.Totals.LLMCalls != 3 || len(classification[0].Turns) != 3 {
			t.Fatalf("new snapshot missed commit: tokens=%d calls=%d classified=%d", detail.Session.InputTokens, metrics.Totals.LLMCalls, len(classification[0].Turns))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestReadSnapshotReleasesConnectionOnFailure(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "callback error", true: "context canceled"}[canceled], func(t *testing.T) {
			f := newFixture(t)
			id := seedMetricsSession(f)
			db, _ := f.reader.db()
			db.SetMaxOpenConns(1)
			svc := newServiceFor(f.reader, nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			failure := errors.New("read failed")
			err := svc.ReadSnapshot(ctx, func(db SQLQuerier) error {
				if _, err := ReadSession(ctx, db, id); err != nil {
					return err
				}
				if canceled {
					cancel()
					return nil
				}
				return failure
			})
			if canceled {
				if err == nil {
					t.Fatal("cancellation succeeded")
				}
			} else if !errors.Is(err, failure) {
				t.Fatalf("error=%v", err)
			}
			next, done := context.WithTimeout(context.Background(), 5*time.Second)
			defer done()
			if err := svc.ReadSnapshot(next, func(db SQLQuerier) error { _, err := ReadSession(next, db, id); return err }); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReadSnapshotWithoutDatabase(t *testing.T) {
	svc := NewService(filepath.Join(t.TempDir(), "missing.db"))
	defer svc.Stop() //nolint:errcheck
	if err := svc.ReadSnapshot(context.Background(), func(db SQLQuerier) error {
		detail, err := ReadSession(context.Background(), db, 1)
		if detail.Found || detail.Tools == nil {
			t.Fatalf("empty detail: %+v", detail)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
