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

	agent := &presence.Agent{
		Sampler:  presence.NewSampler(),
		Sink:     st,
		Interval: interval,
		Now:      time.Now,
		Paused: func() bool {
			_, err := os.Stat(pausePath)
			return err == nil
		},
		Logf: logger.Printf,
	}
	return agent.Run(ctx)
}
