package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/JtheGunner/ticktack/internal/pause"
	"github.com/JtheGunner/ticktack/internal/render"
)

func newPauseCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "pause",
		Short: msgPauseShort,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runPause(defaultDailyDeps(cmd))
		},
	}
}

func newResumeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "resume",
		Short: msgResumeShort,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runResume(defaultDailyDeps(cmd))
		},
	}
}

// runPause starts a pause now. It is idempotent: a running pause keeps its
// start.
func runPause(deps dailyDeps) error {
	path, err := deps.PausePath()
	if err != nil {
		return err
	}
	if err := pause.Begin(path, deps.Now()); err != nil {
		return err
	}
	_, err = fmt.Fprintln(deps.Out, deps.Render.Gutter(render.StatusWarn, msgPaused))
	return err
}

// runResume ends the running pause now and keeps its interval, so the git
// scan and the ActivityWatch import leave that time out later. Without a
// running pause it changes nothing.
func runResume(deps dailyDeps) error {
	path, err := deps.PausePath()
	if err != nil {
		return err
	}
	if err := pause.End(path, deps.Now()); err != nil {
		return err
	}
	_, err = fmt.Fprintln(deps.Out, deps.Render.Gutter(render.StatusOK, msgResumed))
	return err
}
