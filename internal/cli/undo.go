package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/JtheGunner/ticktack/internal/ledger"
	"github.com/JtheGunner/ticktack/internal/render"
	"github.com/JtheGunner/ticktack/internal/store"
)

func newUndoCommand() *cobra.Command {
	return newUndoCommandWith(defaultSyncDeps())
}

func newUndoCommandWith(deps syncDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "undo",
		Short: msgUndoUndoShort,
		Long:  msgUndoUndoLong,
		Args:  cobra.NoArgs,
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
	last, found, err := st.LastSyncRun(ctx)
	if err != nil {
		return err
	}
	if !found {
		_, _ = fmt.Fprintln(out, deps.Render.Gutter(render.StatusInfo, msgNothingToUndo))
		return nil
	}
	archivedDays, err := countArchivedDays(ctx, st, last)
	if err != nil {
		return err
	}
	if archivedDays > 0 {
		_, _ = fmt.Fprintln(out, deps.Render.Gutter(render.StatusInfo, fmt.Sprintf(msgUndoArchivedNote, archivedDays)))
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
	if cfg.Auto.Autopilot() {
		_, _ = fmt.Fprintln(out, deps.Render.Gutter(render.StatusInfo, msgAutopilotUndoHint))
	}
	return err
}

// countArchivedDays returns how many distinct days of the run's entries were
// archived by the retention cleanup.
func countArchivedDays(ctx context.Context, st archivedDayReader, run store.SyncRun) (int, error) {
	seen := map[string]bool{}
	count := 0
	for _, e := range run.Entries {
		if seen[e.Date] {
			continue
		}
		seen[e.Date] = true
		_, archived, err := st.ArchivedDay(ctx, e.Date)
		if err != nil {
			return 0, err
		}
		if archived {
			count++
		}
	}
	return count, nil
}
