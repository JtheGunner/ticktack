package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"cloud.google.com/go/civil"
	"github.com/spf13/cobra"

	"github.com/JtheGunner/ticktack/internal/auto"
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
		// Held even when the undo failed half way: the autopilot must not book a
		// day again that the user was in the middle of taking back.
		held, holdErr := holdUndoneDays(ctx, st, last, deps.Now(), cfg.Location)
		if holdErr != nil {
			return errors.Join(err, holdErr)
		}
		if len(held) > 0 {
			_, _ = fmt.Fprintln(out, deps.Render.Gutter(render.StatusInfo, fmt.Sprintf(msgAutopilotUndoHeld, strings.Join(held, ", "))))
		}
	}
	return err
}

// holdUndoneDays holds, for the autopilot, every completed day (before today
// in loc) the undone run touched, and returns them in ascending order. The rule
// is deliberately independent of who made the run: whatever booked a day, the
// user who takes it back while the autopilot is active does not want it booked
// again behind their back. A day of a run the user booked by hand is a manual
// day the autopilot skips anyway, so holding it changes nothing. Today and
// future days are never booked by the autopilot and need no hold.
func holdUndoneDays(ctx context.Context, st *store.Store, run store.SyncRun, now time.Time, loc *time.Location) ([]string, error) {
	if loc == nil {
		loc = time.Local
	}
	today := civil.DateOf(now.In(loc))
	seen := map[string]bool{}
	var held []string
	for _, e := range run.Entries {
		day, err := civil.ParseDate(e.Date)
		if err != nil || !auto.Completed(day, today) || seen[e.Date] {
			continue
		}
		seen[e.Date] = true
		if err := st.HoldDay(ctx, e.Date, now); err != nil {
			return nil, err
		}
		held = append(held, e.Date)
	}
	sort.Strings(held)
	return held, nil
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
