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

	"github.com/JtheGunner/ticktack/internal/collector/presence"
	"github.com/JtheGunner/ticktack/internal/collector/title"
	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/logfile"
	"github.com/JtheGunner/ticktack/internal/paths"
	"github.com/JtheGunner/ticktack/internal/store"
)

func newAgentCommand() *cobra.Command {
	var interval time.Duration
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Run the presence agent in the foreground",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if interval <= 0 {
				return errors.New("--interval must be positive")
			}
			return runAgent(cmd, interval)
		},
	}
	cmd.Flags().DurationVar(&interval, "interval", presence.DefaultInterval, "sampling interval")
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

	cfg, cfgOK := loadAgentConfig(logger.Printf)
	var jobs []backgroundJob
	if cfgOK {
		titleStatusPath, statusErr := paths.TitleStatusPath()
		if statusErr != nil {
			logger.Printf("title probe status disabled: %v", statusErr)
		}
		if collector := newTitleCollector(cfg, title.NewSource(), st, pausePath, titleStatusPath, interval, logger.Printf); collector != nil {
			jobs = append(jobs, func(ctx context.Context) { _ = collector.Run(ctx) })
		}
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			logger.Printf("git scan disabled: %v", homeErr)
		} else {
			jobs = append(jobs, newScanJob(cfg, home, st, defaultRepoScanner, pauseFlagCheck(pausePath),
				cfg.Scan.Interval, func() time.Time { return time.Now().Round(0) }, logger.Printf))
		}
	}

	agent := newPresenceAgent(presence.NewSampler(), st, interval, pausePath, logger.Printf)
	// All collector goroutines finish before the deferred store close runs.
	return runWithBackground(ctx, jobs, agent.Run)
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
func newPresenceAgent(sampler presence.Sampler, sink presence.Appender, interval time.Duration, pausePath string, logf func(format string, args ...any)) *presence.Agent {
	return &presence.Agent{
		Sampler:  sampler,
		Sink:     sink,
		Interval: interval,
		// Round(0) strips the monotonic reading, which stops during system
		// sleep on macOS and would hide sleep gaps.
		Now:    func() time.Time { return time.Now().Round(0) },
		Paused: pauseFlagCheck(pausePath),
		Logf:   logf,
	}
}

// pauseFlagCheck returns a function that reports whether the pause flag file
// exists at path. The hook and the agent share it.
func pauseFlagCheck(path string) func() bool {
	return func() bool {
		_, err := os.Stat(path)
		return err == nil
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
func newTitleCollector(cfg config.Config, source title.Source, sink title.Appender, pausePath, statusPath string, interval time.Duration, logf func(format string, args ...any)) *title.Collector {
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
		Sink:          sink,
		Projects:      cfg.TitleHint.Projects,
		TicketPattern: cfg.TicketPattern,
		Interval:      interval,
		Now:           func() time.Time { return time.Now().Round(0) },
		Paused:        pauseFlagCheck(pausePath),
		Logf:          logf,
	}
}
