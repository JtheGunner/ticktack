package cli

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/JtheGunner/ticktack/internal/collector"
	gitscan "github.com/JtheGunner/ticktack/internal/collector/git"
	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/pause"
)

// pauseLoader returns the pauses known at now; a running pause lasts until now.
type pauseLoader func(now time.Time) (pause.Intervals, error)

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
// and then every interval, and does nothing while paused reports true.
// Events from an earlier pause are never stored; a pass is skipped when the
// pauses cannot be loaded.
func newScanJob(cfg config.Config, home string, sink collector.Sink, scan repoScanner, paused func() bool, pauses pauseLoader, interval time.Duration, now func() time.Time, logf func(format string, args ...any)) backgroundJob {
	if interval <= 0 || (len(cfg.RepoPaths) == 0 && len(cfg.RepoRoots) == 0) {
		return nil
	}
	return func(ctx context.Context) {
		// The repositories are resolved per pass, so new checkouts below
		// repo_roots are picked up without a restart.
		var passCollector collector.Collector = &perPassGit{build: func() *gitscan.Collector {
			c := newGitCollector(trackedRepos(cfg, home), home, cfg.TicketPattern, scan, func(current time.Time) (time.Time, time.Time) {
				return scanStartOfLookback(current, cfg.Location), current.Add(scanFutureSlack)
			})
			c.Now = now
			c.LoadPauses = pauses
			c.OnReport = func(report gitscan.Report, err error) {
				for _, s := range report.Skipped {
					logf(msgRepoLogSkipped, SanitizeForTerminal(s.Entry), SanitizeForTerminal(s.Reason))
				}
				if err == nil {
					logf(msgRepoLogDone, report.Scanned, report.Total, report.Found)
				}
			}
			return c
		}}
		passCollector = collector.SkipWhen(paused, passCollector)
		_ = collector.Every(interval, passCollector, func(err error) {
			if errors.Is(err, gitscan.ErrPausesUnavailable) {
				logf(msgRepoLogNoPauses, err)
				return
			}
			logf(msgRepoLogStoreFail, err)
		}).Run(ctx, sink)
	}
}

// perPassGit builds a fresh git collector for every pass.
type perPassGit struct{ build func() *gitscan.Collector }

func (p *perPassGit) Name() string { return gitscan.Source }

func (p *perPassGit) Run(ctx context.Context, sink collector.Sink) error {
	return p.build().Run(ctx, sink)
}
