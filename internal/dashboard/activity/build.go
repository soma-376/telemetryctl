package activity

import (
	"context"

	"github.com/your-org/pulsemetry/internal/dashboard"
)

type Builder struct{ source *dashboard.Service }

func NewBuilder(source *dashboard.Service) *Builder { return &Builder{source: source} }

func (b *Builder) List(ctx context.Context, q Query) (Page, error) {
	db, _ := b.source.Querier()
	return list(ctx, db, q)
}

func (b *Builder) Session(ctx context.Context, id int64) (Detail, error) {
	var out Detail
	err := b.source.ReadSnapshot(ctx, func(db dashboard.SQLQuerier) error {
		var err error
		out.Detail, err = dashboard.ReadSession(ctx, db, id)
		if err != nil || !out.Detail.Found {
			return err
		}
		out.Metrics, err = dashboard.ReadSessionMetrics(ctx, db, dashboard.SessionMetricsQuery{SessionID: id})
		if err != nil {
			return err
		}
		classified, err := dashboard.ClassifySessions(ctx, db, []int64{id})
		if err == nil {
			out.Classification = classified[0]
		}
		return err
	})
	if err != nil {
		return Detail{}, err
	}
	return out, nil
}
