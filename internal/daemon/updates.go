package daemon

import (
	"context"
	"runtime"
	"time"

	"github.com/your-org/pulsemetry/internal/installer"
	"github.com/your-org/pulsemetry/internal/updatecheck"
)

func (d *daemon) initUpdates() {
	checker := d.opts.UpdateChecker
	if checker == nil {
		checker = updatecheck.NewClient(d.state.ServerURL, installer.Version, runtime.GOOS, runtime.GOARCH)
	}
	d.updates = updatecheck.NewService(installer.Version, d.state.ServerURL != "", checker, d.opts.Now)
}

// startUpdates 는 기동 직후·24시간마다 순차 조회한다. 외부 서버 지연이 수집·flush를
// 막지 않게 전용 워커를 쓰며, 별도 수동 갱신 경로는 두지 않는다.
func (d *daemon) startUpdates(ctx context.Context) {
	if d.updates.Snapshot().Status == updatecheck.StatusDisabled {
		return
	}
	d.updatesDone = make(chan struct{})
	go func() {
		defer close(d.updatesDone)
		ticker := time.NewTicker(d.opts.UpdateInterval)
		defer ticker.Stop()
		for {
			if ctx.Err() != nil {
				return
			}
			checkCtx, cancel := context.WithTimeout(ctx, updatecheck.RequestTimeout)
			err := d.updates.Refresh(checkCtx)
			cancel()
			if err != nil && ctx.Err() == nil {
				d.log.Printf("경고: 업데이트 확인 실패: %s", updatecheck.FailureReason(err))
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// waitUpdates 는 종료의 기존 전체 예산 안에서만 기다린다.
func (d *daemon) waitUpdates(deadline time.Time) {
	if d.updatesDone == nil {
		return
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	select {
	case <-d.updatesDone:
	case <-ctx.Done():
		d.log.Print("경고: 종료 제한 시간 안에 업데이트 확인 워커가 종료되지 않았다")
	}
}
