package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

func newPauseCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "pause",
		Short: "Pause all capture",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runPause(defaultDailyDeps(cmd))
		},
	}
}

func newResumeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "resume",
		Short: "Resume capture after a pause",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runResume(defaultDailyDeps(cmd))
		},
	}
}

// runPause creates the pause flag file. It is idempotent.
func runPause(deps dailyDeps) error {
	path, err := deps.PausePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create pause flag: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("create pause flag: %w", err)
	}
	_, err = fmt.Fprintln(deps.Out, msgPaused)
	return err
}

// runResume removes the pause flag file. A missing file is fine.
func runResume(deps dailyDeps) error {
	path, err := deps.PausePath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove pause flag: %w", err)
	}
	_, err = fmt.Fprintln(deps.Out, msgResumed)
	return err
}
