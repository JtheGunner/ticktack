package cli

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/JtheGunner/ticktack/internal/collector/presence"
	"github.com/JtheGunner/ticktack/internal/collector/title"
	"github.com/JtheGunner/ticktack/internal/config"
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
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	defer func() { _ = logFile.Close() }()
	logger := log.New(logFile, "agent: ", log.LstdFlags)

	st, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if collector := loadTitleCollector(st, interval, pausePath, logger.Printf); collector != nil {
		go func() { _ = collector.Run(ctx) }()
	}

	agent := newPresenceAgent(presence.NewSampler(), st, interval, pausePath, logger.Printf)
	return agent.Run(ctx)
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

// loadTitleCollector starts the optional window-title collector when the
// config enables it. A broken config disables only the collector, never the
// presence agent.
func loadTitleCollector(sink title.Appender, interval time.Duration, pausePath string, logf func(format string, args ...any)) *title.Collector {
	path, err := config.DefaultPath()
	if err != nil {
		return nil
	}
	cfg, err := config.LoadOrDefault(path)
	if err != nil {
		logf("title hints disabled: config not loadable: %v", err)
		return nil
	}
	return newTitleCollector(cfg, title.NewSource(), sink, pausePath, interval, logf)
}

// newTitleCollector builds the window-title collector, or returns nil while
// the feature is disabled. It honours the same pause flag as the presence
// agent.
func newTitleCollector(cfg config.Config, source title.Source, sink title.Appender, pausePath string, interval time.Duration, logf func(format string, args ...any)) *title.Collector {
	if !cfg.TitleHint.Enabled {
		return nil
	}
	return &title.Collector{
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
