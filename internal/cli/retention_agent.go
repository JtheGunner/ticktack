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

// loadRetentionConfig reads the configuration file anew. The agent calls it
// before every retention run, so a changed retention_days takes effect without
// a restart.
func loadRetentionConfig() (config.Config, error) {
	path, err := config.DefaultPath()
	if err != nil {
		return config.Config{}, err
	}
	return config.LoadOrDefault(path)
}

// newRetentionJob builds the daily retention cleanup of the agent. Like the
// scan job it runs once at start and then every interval. Before each run it
// loads the configuration anew through loadCfg: while retention_days is 0 the
// run does nothing (no lock, no log line), and a configuration that cannot be
// loaded skips the run with a log line, so a broken edit never deletes
// anything. The run holds the booking lock so it never runs beside a sync;
// while another run holds it, the cleanup is skipped until the next interval.
// A failure is logged and never stops the agent: the log lines carry counts
// and error text only, no event content.
func newRetentionJob(loadCfg func() (config.Config, error), st retention.Store, acquireLock func() (func(), error), interval time.Duration, now func() time.Time, logf func(format string, args ...any)) backgroundJob {
	return func(ctx context.Context) {
		once := func() {
			cfg, err := loadCfg()
			if err != nil {
				logf(msgRetentionLogConfig, err)
				return
			}
			if cfg.RetentionDays <= 0 {
				return
			}
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
