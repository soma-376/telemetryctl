package activity

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/your-org/pulsemetry/internal/dashboard"
	"github.com/your-org/pulsemetry/internal/store"
)

func TestBuilderWithoutDatabase(t *testing.T) {
	svc := dashboard.NewService(filepath.Join(t.TempDir(), "missing.db"))
	t.Cleanup(func() { _ = svc.Stop() })
	b := NewBuilder(svc)
	page, err := b.List(context.Background(), Query{Text: "토큰"})
	if err != nil || page.Rows == nil || len(page.Rows) != 0 || page.HasMore || page.NextCursor.ID != 0 {
		t.Fatalf("page=%+v error=%v", page, err)
	}
	detail, err := b.Session(context.Background(), 1)
	if err != nil || detail.Detail.Found || detail.Detail.Tools == nil {
		t.Fatalf("detail=%+v error=%v", detail, err)
	}
}

func TestBuilderConcurrentReadsAndCancellation(t *testing.T) {
	path := store.PathIn(t.TempDir())
	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := dashboard.NewService(path)
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Stop() })
	b := NewBuilder(svc)
	results := make(chan error, 20)
	for range 20 {
		go func() { _, err := b.List(context.Background(), Query{}); results <- err }()
	}
	for range 20 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := b.List(ctx, Query{}); err == nil {
		t.Fatal("취소된 조회가 성공했다")
	}
	if _, err := b.Session(ctx, 1); err == nil {
		t.Fatal("취소된 상세 조회가 성공했다")
	}
}
