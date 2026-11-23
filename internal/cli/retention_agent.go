package cli

import (
	"context"
	"errors"
	"time"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/retention"
)

// retentionInterval is how often the agent runs the retention cleanup.
const retentionInterval = 24 * time.Hour

// newRetentionJob builds the daily retention cleanup of the agent, or returns
// nil while retention_days is 0. Like the scan job it runs once at start and
// then every interval. It holds the booking lock so it never runs beside a
// sync; while another run holds it, the cleanup is skipped until the next
// interval. A failure is logged and never stops the agent: the log lines carry
// counts and error text only, no event content.
func newRetentionJob(cfg config.Config, st retention.Store, acquireLock func() (func(), error), interval time.Duration, now func() time.Time, logf func(format string, args ...any)) backgroundJob {
	if cfg.RetentionDays <= 0 {
		return nil
	}
	return func(ctx context.Context) {
		once := func() {
			release := func() {}
			if acquireLock != nil {
				var err error
				release, err = acquireLock()
				if errors.Is(err, ErrBookingRunActive) {
					logf(msgRetentionLogBusy)
					return
				}
				if err != nil {
					logf(msgRetentionLogFail, err)
					return
				}
			}
			defer release()
			report, err := retention.Run(ctx, st, cfg, now(), false)
			if err != nil {
				if ctx.Err() == nil {
					logf(msgRetentionLogFail, err)
				}
				return
			}
			if len(report.Days) > 0 {
				logf(msgRetentionLogDone, report.Events, len(report.Days))
			}
		}
		once()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				once()
			}
		}
	}
}
