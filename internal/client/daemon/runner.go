// Package daemon runs pulsemetry's long-lived background process.
package daemon

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/your-org/pulsemetry/internal/client/installer"
)

type Options struct {
	StatePath string
	Interval  time.Duration
	Logger    *log.Logger
}

// Run loads the installation state and runs periodic work until ctx is canceled.
func Run(ctx context.Context, opts Options) error {
	if opts.Interval <= 0 {
		return fmt.Errorf("daemon interval must be positive")
	}
	if opts.Logger == nil {
		return fmt.Errorf("daemon logger is required")
	}

	state, err := installer.LoadState(opts.StatePath)
	if err != nil {
		return fmt.Errorf("load installation state: %w", err)
	}
	if state == nil || state.InstallationID == "" {
		return fmt.Errorf("pulsemetry is not enrolled: state file not found at %s", opts.StatePath)
	}

	opts.Logger.Printf("daemon started: installation_id=%s interval=%s", state.InstallationID, opts.Interval)
	ticker := time.NewTicker(opts.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			opts.Logger.Printf("daemon stopped: %v", ctx.Err())
			return nil
		case <-ticker.C:
			runPeriodicWork(opts.Logger, state)
		}
	}
}

func runPeriodicWork(logger *log.Logger, state *installer.State) {
	// The heartbeat API will replace this placeholder in the next phase.
	logger.Printf("periodic work: installation_id=%s config_revision=%d", state.InstallationID, state.ConfigRevision)
}
