package cli

import (
	"context"
	"sync"
	"time"

	"github.com/JtheGunner/ticktack/internal/config"
)

// backgroundJob is a collector loop that runs until ctx is cancelled.
type backgroundJob func(ctx context.Context)

// runWithBackground runs main while the jobs run beside it. When main returns
// it cancels the jobs and waits for every one of them, so the caller can close
// the store afterwards without racing a final append. Nil jobs are ignored.
func runWithBackground(ctx context.Context, jobs []backgroundJob, main func(ctx context.Context) error) error {
	jobCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	for _, job := range jobs {
		if job == nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			job(jobCtx)
		}()
	}
	defer func() {
		cancel()
		wg.Wait()
	}()
	return main(jobCtx)
}

// newScanJob builds the periodic git scan of the agent, or returns nil while
// the scan is off or no repository is configured. The job scans once at start
// and then every interval, and does nothing while the pause flag exists.
func newScanJob(cfg config.Config, home string, sink eventAppender, scan repoScanner, paused func() bool, interval time.Duration, now func() time.Time, logf func(format string, args ...any)) backgroundJob {
	if interval <= 0 || (len(cfg.RepoPaths) == 0 && len(cfg.RepoRoots) == 0) {
		return nil
	}
	return func(ctx context.Context) {
		once := func() {
			if paused() {
				return
			}
			current := now()
			from := scanStartOfLookback(current, cfg.Location)
			report, err := scanRepositories(ctx, trackedRepos(cfg, home), home, from, current.Add(scanFutureSlack), cfg.TicketPattern, scan, sink)
			for _, s := range report.Skipped {
				logf(msgRepoLogSkipped, SanitizeForTerminal(s.Path), SanitizeForTerminal(s.Reason))
			}
			if err != nil {
				if ctx.Err() == nil {
					logf(msgRepoLogStoreFail, err)
				}
				return
			}
			logf(msgRepoLogDone, report.Scanned, report.Total, report.Found)
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
