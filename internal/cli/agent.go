package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/JtheGunner/ticktack/internal/collector"
	"github.com/JtheGunner/ticktack/internal/collector/presence"
	"github.com/JtheGunner/ticktack/internal/collector/title"
	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/logfile"
	"github.com/JtheGunner/ticktack/internal/paths"
	"github.com/JtheGunner/ticktack/internal/pause"
	"github.com/JtheGunner/ticktack/internal/store"
)

func newAgentCommand() *cobra.Command {
	var interval time.Duration
	cmd := &cobra.Command{
		Use:   "agent",
		Short: msgAgentAgentShort,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if interval <= 0 {
				return errors.New(msgAgentIntervalNotPositive)
			}
			return runAgent(cmd, interval)
		},
	}
	cmd.Flags().DurationVar(&interval, "interval", presence.DefaultInterval, msgAgentIntervalFlag)
	return cmd
}

func runAgent(cmd *cobra.Command, interval time.Duration) error {
	dbPath, err := paths.DBPath()
	if err != nil {
		return err
	}
	pausePath, err := paths.PausePath()
	if err != nil {
		return err
	}
	logPath, err := paths.LogPath()
	if err != nil {
		return err
	}
	for _, dir := range []string{filepath.Dir(dbPath), filepath.Dir(logPath)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	}
	logger := newAgentLogger(logPath)

	st, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Every collector of the agent writes through the one pause gate.
	sink := collector.PauseGate(st, pauseFlagCheck(pausePath))

	cfg, cfgOK := loadAgentConfig(logger.Printf)
	var jobs []backgroundJob
	if cfgOK {
		titleStatusPath, statusErr := paths.TitleStatusPath()
		if statusErr != nil {
			logger.Printf("title probe status disabled: %v", statusErr)
		}
		if titleCollector := newTitleCollector(cfg, title.NewSource(), pausePath, titleStatusPath, interval, logger.Printf); titleCollector != nil {
			jobs = append(jobs, collectorJob(titleCollector, sink, logger.Printf))
		}
		jobs = append(jobs, newRetentionJob(loadRetentionConfig, st, acquireBookingLock, retentionInterval, func() time.Time { return time.Now().Round(0) }, logger.Printf))
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			logger.Printf("git scan disabled: %v", homeErr)
		} else {
			jobs = append(jobs, newScanJob(cfg, home, sink, defaultRepoScanner, pauseFlagCheck(pausePath),
				func(now time.Time) (pause.Intervals, error) { return pause.Load(pausePath, now) },
				cfg.Scan.Interval, func() time.Time { return time.Now().Round(0) }, logger.Printf))
		}
	}

	agent := newPresenceAgent(presence.NewSampler(), interval, pausePath, logger.Printf)
	// All collector goroutines finish before the deferred store close runs.
	return runWithBackground(ctx, jobs, func(ctx context.Context) error { return agent.Run(ctx, sink) })
}

// newAgentLogger returns the logger of the agent and rotates the log once.
// launchd appends stdout and stderr of the agent to the same file, so it is
// bounded at start even when the agent itself has nothing to log. The rotation
// is best effort: a log that cannot be rotated (full disk, wrong permissions)
// must never keep the agent from recording presence, so the failure is only
// logged.
func newAgentLogger(logPath string) *logfile.Logger {
	logger := logfile.New(logPath, "agent: ")
	if err := logger.Rotate(); err != nil {
		logger.Printf("log rotation failed: %v", err)
	}
	return logger
}

// newPresenceAgent builds the production presence agent. It stores nothing
// while the pause flag file at pausePath exists.
func newPresenceAgent(sampler presence.Sampler, interval time.Duration, pausePath string, logf func(format string, args ...any)) *presence.Agent {
	return &presence.Agent{
		Sampler:  sampler,
		Interval: interval,
		// Round(0) strips the monotonic reading, which stops during system
		// sleep on macOS and would hide sleep gaps.
		Now:    func() time.Time { return time.Now().Round(0) },
		Paused: pauseFlagCheck(pausePath),
		Logf:   logf,
	}
}

// pauseFlagCheck returns the one pause check of the agent and the hook. It is
// fail-closed: a flag that cannot be read counts as paused.
func pauseFlagCheck(path string) func() bool {
	return func() bool { return pause.IsPaused(path) }
}

// collectorJob runs c until ctx is cancelled and logs a failure that ends it.
func collectorJob(c collector.Collector, sink collector.Sink, logf func(format string, args ...any)) backgroundJob {
	return func(ctx context.Context) {
		if err := c.Run(ctx, sink); err != nil && ctx.Err() == nil {
			logf("%s collector stopped: %v", c.Name(), err)
		}
	}
}

// loadAgentConfig loads the configuration for the optional collectors. A
// broken config disables only the collectors, never the presence agent.
func loadAgentConfig(logf func(format string, args ...any)) (config.Config, bool) {
	path, err := config.DefaultPath()
	if err != nil {
		return config.Config{}, false
	}
	cfg, err := config.LoadOrDefault(path)
	if err != nil {
		logf("optional collectors disabled: config not loadable: %v", err)
		return config.Config{}, false
	}
	return cfg, true
}

// newTitleCollector builds the window-title collector, or returns nil while
// the feature is disabled. It honours the same pause flag as the presence
// agent.
// The collector records the outcome of each title read in the status file at
// statusPath, which `ticktack doctor` reads; an empty path records nothing.
func newTitleCollector(cfg config.Config, source title.Source, pausePath, statusPath string, interval time.Duration, logf func(format string, args ...any)) *title.Collector {
	if !cfg.TitleHint.Enabled {
		return nil
	}
	var status title.StatusRecorder
	if statusPath != "" {
		status = &title.StatusFile{Path: statusPath}
	}
	return &title.Collector{
		Status:        status,
		Source:        source,
		Projects:      cfg.TitleHint.Projects,
		TicketPattern: cfg.TicketPattern,
		Interval:      interval,
		Now:           func() time.Time { return time.Now().Round(0) },
		Paused:        pauseFlagCheck(pausePath),
		Logf:          logf,
	}
}
