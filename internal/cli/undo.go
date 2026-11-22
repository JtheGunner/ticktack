package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/JtheGunner/ticktack/internal/ledger"
	"github.com/JtheGunner/ticktack/internal/render"
)

func newUndoCommand() *cobra.Command {
	return newUndoCommandWith(defaultSyncDeps())
}

func newUndoCommandWith(deps syncDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "undo",
		Short: "Revert the last sync",
		Long: `Revert the last booking run: items it created are deleted from YouTrack,
items it updated are restored to their previous minutes and items it deleted
are booked again. Running undo again reverts the run before it.` + msgBookingLockHelp,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUndo(cmd.Context(), cmd.OutOrStdout(), deps.forCommand(cmd))
		},
	}
}

func runUndo(ctx context.Context, out io.Writer, deps syncDeps) error {
	// The lock comes first: nothing read below may change under another run.
	release, err := deps.lockBookingRun(false)
	if err != nil {
		return err
	}
	defer release()
	cfg, cfgPath, err := deps.LoadConfig()
	if err != nil {
		return err
	}
	st, err := deps.OpenStore()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	if _, found, err := st.LastSyncRun(ctx); err != nil {
		return err
	} else if !found {
		_, _ = fmt.Fprintln(out, deps.Render.Gutter(render.StatusInfo, msgNothingToUndo))
		return nil
	}
	target, err := deps.NewSink(cfg, cfgPath)
	if err != nil {
		return err
	}
	res, err := ledger.Undo(ctx, target, st, deps.Now)
	status := render.StatusOK
	if err != nil {
		status = render.StatusWarn
	}
	line := msgUndoResult(res.Deleted, res.Restored, res.Recreated, res.Forgotten)
	_, _ = fmt.Fprintln(out, deps.Render.Gutter(status, line))
	return err
}
