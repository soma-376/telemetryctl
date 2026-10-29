package daemon

import (
	"context"
	"crypto/rand"
	"runtime"
	"time"

	"github.com/your-org/pulsemetry/internal/heartbeat"
	"github.com/your-org/pulsemetry/internal/installer"
	"github.com/your-org/pulsemetry/internal/otlpdecode"
)

// startHeartbeat는 수집 루프와 독립된 워커 하나에서 즉시·매분 보고한다.
// 네트워크나 키링이 지연돼도 새 워커를 겹쳐 만들지 않는다.
func (d *daemon) startHeartbeat(ctx context.Context) {
	if d.state.ServerURL == "" {
		d.log.Print("경고: 등록 서버 주소가 없어 설치 상태를 보고하지 않는다")
		return
	}
	reporter := d.opts.HeartbeatReporter
	if reporter == nil {
		reporter = heartbeat.NewClient(d.state.ServerURL, d.state.InstallationID)
	}
	runID := rand.Text()
	d.heartbeatDone = make(chan struct{})
	go func() {
		defer close(d.heartbeatDone)
		ticker := time.NewTicker(d.opts.HeartbeatInterval)
		defer ticker.Stop()
		for {
			if ctx.Err() != nil {
				return
			}
			reportCtx, cancel := context.WithTimeout(ctx, heartbeat.RequestTimeout)
			response, err := reporter.Send(reportCtx, d.heartbeatReport(runID))
			cancel()
			if d.policy != nil {
				d.policy.Record(response, err)
			}
			if err != nil && ctx.Err() == nil {
				d.log.Printf("경고: 설치 상태 보고 실패: %s", heartbeat.FailureReason(err))
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (d *daemon) heartbeatReport(runID string) heartbeat.Report {
	c := heartbeat.Collection{Mode: "direct"}
	if d.state.Local.Enabled {
		c.Mode = "local"
	}
	if d.srv != nil {
		started := d.receivingSince
		c.ReceivingSince = &started
	}
	// 직결 경로에서는 로컬 전달기가 있어도 회사로 가는 전체 전송을 관측하지 못한다.
	c.Forwarding = d.fwd != nil
	if c.Forwarding && c.Mode == "local" {
		s := d.fwd.Stats()
		c.Delivered = s.Sent
		c.Pending = s.Pending
		c.Lost = s.DroppedQueueFull + s.DroppedScrub + s.DroppedShutdown + s.Discarded + s.Failed
		if !s.LastSentAt.IsZero() {
			c.LastDeliveredAt = &s.LastSentAt
		}
		if d.srv != nil {
			signals := d.state.Manifest.Signals
			if signals.Logs {
				c.Lost += d.srv.Lost(otlpdecode.PayloadLogs)
			}
			if signals.Metrics {
				c.Lost += d.srv.Lost(otlpdecode.PayloadMetrics)
			}
			if signals.Traces {
				c.Lost += d.srv.Lost(otlpdecode.PayloadTraces)
			}
		}
	}
	// 스냅샷을 읽은 뒤 시각을 잡는다. OS 시계가 뒤로 보정돼도 API가 거부하는
	// sent_at 이후의 본문 시각을 보내지 않는다.
	now := d.opts.Now().UTC()
	for _, timestamp := range []*time.Time{c.ReceivingSince, c.LastDeliveredAt} {
		if timestamp != nil && timestamp.After(now) {
			*timestamp = now
		}
	}
	return heartbeat.Report{
		SentAt: now,
		Daemon: heartbeat.Daemon{
			Version: installer.Version, Platform: runtime.GOOS, Architecture: runtime.GOARCH, RunID: runID,
		},
		AppliedConfigRevision: d.state.ConfigRevision,
		Collection:            c,
	}
}

func (d *daemon) waitHeartbeat(deadline time.Time) {
	if d.heartbeatDone == nil {
		return
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	select {
	case <-d.heartbeatDone:
	case <-ctx.Done():
		d.log.Print("경고: 종료 제한 시간 안에 설치 상태 보고 워커가 종료되지 않았다")
	}
}
