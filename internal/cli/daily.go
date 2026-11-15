package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/paths"
	"github.com/JtheGunner/ticktack/internal/resolver"
	"github.com/JtheGunner/ticktack/internal/review"
	"github.com/JtheGunner/ticktack/internal/store"
)

// dailyStore is the part of the store the daily commands use.
type dailyStore interface {
	Append(ctx context.Context, e event.Event) error
	Events(ctx context.Context, from, to time.Time) ([]event.Event, error)
	Overrides(ctx context.Context, from, to time.Time) ([]event.Override, error)
	LearnedRules(ctx context.Context) ([]store.LearnedRule, error)
	Purge(ctx context.Context) error
	Close() error
}

// dailyDeps holds everything the daily commands touch outside of pure logic.
type dailyDeps struct {
	// OpenStore opens the event store. The caller closes it.
	OpenStore func() (dailyStore, error)
	// LoadConfig returns the user configuration (defaults when absent).
	LoadConfig func() (config.Config, error)
	// Now returns the current time.
	Now func() time.Time
	// PausePath returns the location of the pause flag file.
	PausePath func() (string, error)
	// In supplies interactive answers.
	In io.Reader
	// Out receives all regular output.
	Out io.Writer
}

// defaultDailyDeps wires the real dependencies to the command's streams.
func defaultDailyDeps(cmd *cobra.Command) dailyDeps {
	return dailyDeps{
		OpenStore: openDailyStore,
		LoadConfig: func() (config.Config, error) {
			path, err := config.DefaultPath()
			if err != nil {
				return config.Config{}, err
			}
			return config.LoadOrDefault(path)
		},
		// Round(0) strips the monotonic reading so stored times are wall-clock.
		Now:       func() time.Time { return time.Now().Round(0) },
		PausePath: paths.PausePath,
		In:        cmd.InOrStdin(),
		Out:       cmd.OutOrStdout(),
	}
}

func openDailyStore() (dailyStore, error) {
	dbPath, err := paths.DBPath()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	return store.Open(dbPath)
}

// addDailyCommands registers status, today, switch, pause, resume and purge.
func addDailyCommands(root *cobra.Command) {
	root.AddCommand(
		newStatusCommand(),
		newTodayCommand(),
		newSwitchCommand(),
		newPauseCommand(),
		newResumeCommand(),
		newPurgeCommand(),
	)
}

// resolverRules combines the configured rules with the learned rules in st,
// so today, status, review and sync attribute time identically.
func resolverRules(ctx context.Context, st dailyStore, cfg config.Config) (resolver.Rules, error) {
	return review.LoadRules(ctx, st, cfg)
}

// isPaused reports whether the pause flag file exists.
func isPaused(deps dailyDeps) (bool, error) {
	path, err := deps.PausePath()
	if err != nil {
		return false, err
	}
	_, err = os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("check pause flag: %w", err)
}
