package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"cloud.google.com/go/civil"
	"github.com/spf13/cobra"

	"github.com/JtheGunner/ticktack/internal/auto"
	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/ledger"
	"github.com/JtheGunner/ticktack/internal/render"
	"github.com/JtheGunner/ticktack/internal/review"
	"github.com/JtheGunner/ticktack/internal/sink"
	"github.com/JtheGunner/ticktack/internal/store"
)

func newAutoCommand() *cobra.Command {
	return newAutoCommandWith(defaultSyncDeps())
}

func newAutoCommandWith(deps syncDeps) *cobra.Command {
	var dayFlag string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "auto",
		Short: msgAutoAutoShort,
		Long:  msgAutoAutoLong,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAuto(cmd.Context(), cmd.OutOrStdout(), deps.forCommand(cmd), dayFlag, dryRun)
		},
	}
	cmd.Flags().StringVar(&dayFlag, "day", "", msgAutoDayFlag)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, msgAutoDryRunFlag)
	return cmd
}

func newDigestCommand() *cobra.Command {
	return newDigestCommandWith(defaultSyncDeps())
}

func newDigestCommandWith(deps syncDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "digest",
		Short: msgAutoDigestShort,
		Long:  msgAutoDigestLong,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDigest(cmd.Context(), cmd.OutOrStdout(), deps.forCommand(cmd))
		},
	}
	return cmd
}

func runAuto(ctx context.Context, out io.Writer, deps syncDeps, dayFlag string, dryRun bool) error {
	// The lock comes first: nothing read below may change under another run.
	release, err := deps.lockBookingRun(dryRun)
	if err != nil {
		return err
	}
	defer release()
	cfg, cfgPath, err := deps.LoadConfig()
	if err != nil {
		return err
	}
	loc := cfg.Location
	if loc == nil {
		loc = time.Local
	}
	now := deps.Now().In(loc)
	autopilot := cfg.Auto.Autopilot()
	var day civil.Date
	// The autopilot without --day works through the lookback window instead.
	if !autopilot || dayFlag != "" {
		if day, err = parseDay(dayFlag, now); err != nil {
			return err
		}
	}
	st, err := deps.OpenStore()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	if autopilot {
		if dayFlag == "" {
			return runAutopilotPass(ctx, out, deps, cfg, cfgPath, st, civil.DateOf(now), dryRun)
		}
		if !auto.Completed(day, civil.DateOf(now)) {
			return fmt.Errorf(msgAutopilotOpenDay, day)
		}
		if err := refuseArchivedDay(ctx, st, day); err != nil {
			return err
		}
		_, err := bookAutoDay(ctx, out, deps, cfg, cfgPath, st, day, dryRun, false)
		return err
	}

	if err := refuseArchivedDay(ctx, st, day); err != nil {
		return err
	}

	// The gate needs no more than the required days, so the walk stops there.
	streak, err := review.TrackRecord(ctx, st, cfg, now, auto.RequiredDays(cfg.Auto))
	if err != nil {
		return err
	}
	if lock := auto.Gate(cfg.Auto, streak); lock != auto.Unlocked {
		printAutoLocked(out, deps.Render, lock, cfgPath, streak, auto.RequiredDays(cfg.Auto))
		return nil
	}
	_, err = bookAutoDay(ctx, out, deps, cfg, cfgPath, st, day, dryRun, false)
	return err
}

// bookAutoDay books the safe items of one day: the part of an auto run that
// is the same in the gated mode and the autopilot. The caller has decided that
// auto mode may book this day. With quiet set (the autopilot) a day without
// news prints nothing and a digest is stored only when the run changed or
// failed something, so the periodic runs do not flood the digest table. It
// reports whether it printed anything.
func bookAutoDay(ctx context.Context, out io.Writer, deps syncDeps, cfg config.Config, cfgPath string, st *store.Store, day civil.Date, dryRun, quiet bool) (bool, error) {
	d, err := review.LoadDay(ctx, st, cfg, day)
	if err != nil {
		return false, err
	}
	sel := auto.Select(d.Items, auto.LowestTiers(d.Blocks), d.CapMinutes)

	// A capped day needs a human, so nothing of it is changed, deletions included.
	var keep []string
	stale := false
	if !sel.CapExceeded {
		keep, stale, err = reconciliation(ctx, st, d.Items, day)
		if err != nil {
			return false, err
		}
	}

	var report ledger.Report
	var syncErr error
	if len(sel.Book) > 0 || stale {
		// The sink is built only now: the gate passed and there is something
		// to book or a stale booking to remove.
		target, err := deps.NewSink(cfg, cfgPath)
		if err != nil {
			return false, err
		}
		provided, provisioned, err := provisionForAuto(ctx, st, cfg, target, sel, dryRun, deps.Now)
		switch {
		case endsRun(ctx, err):
			// Nothing is sent; the digest lists every item as not sent.
			report, syncErr = ledger.NotSentReport(sel.Items()), err
		case err != nil:
			return false, err
		default:
			sel = provided
			// No booking of a project whose standard task could not be
			// provided is reconciled away: its issue may be unknown here.
			report, syncErr = ledger.Sync(ctx, sel.Items(), target, ledger.SyncOptions{
				Repo: st, DryRun: dryRun, Now: deps.Now,
				Reconcile: []civil.Date{day}, Keep: keep,
				HoldProjects: provisioned.FailedProjects, Failed: provisioned.Failed,
				Describer: deps.describer(cfg),
			})
		}
	}
	digest := auto.BuildDigest(day, sel, report, dryRun)
	text := renderDigest(digest, storedDigestRenderer())
	hasNews := digest.Changed() || digest.Failed > 0 || digest.NotSent > 0
	if quiet && syncErr == nil {
		// The ledger reports a failed item in its report only; the autopilot has
		// nobody reading the report, so the failure must surface as an error.
		syncErr = autopilotItemFailure(report)
	}
	if !dryRun && (!quiet || hasNews) {
		// The digest of a cancelled run is still stored: it lists what was not
		// sent, so the write must not use the cancelled context.
		digestCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), digestWriteTimeout)
		defer cancel()
		if _, err := st.AddDigest(digestCtx, store.Digest{Day: day.String(), CreatedAt: deps.Now(), Text: text}); err != nil {
			return false, err
		}
	}
	if len(sel.Book) == 0 && len(sel.Skipped) == 0 && len(report.Items) == 0 {
		if quiet {
			return false, syncErr
		}
		_, _ = fmt.Fprintln(out, deps.Render.Gutter(render.StatusInfo, fmt.Sprintf(msgAutoNothingToBook, day)))
		return true, syncErr
	}
	_, _ = fmt.Fprintln(out, renderDigest(digest, deps.Render))
	return true, syncErr
}

// autopilotItemFailure turns the failed and unsent items of a report into one
// error, or nil when every item went through. The error text is sanitised
// server text and carries the first failure only; the digest lists them all.
func autopilotItemFailure(report ledger.Report) error {
	if report.Failed == 0 && report.NotSent == 0 {
		return nil
	}
	first := ""
	for _, it := range report.Items {
		if it.Error != "" {
			first = SanitizeForTerminal(it.Error)
			break
		}
	}
	return fmt.Errorf(msgAutopilotItemsFailed, report.Failed, report.NotSent, first)
}

// runAutopilotPass books every completed day of the lookback window, oldest
// first, that is not the user's own: the autopilot has no trust gate. A day
// the user reviewed or booked by hand is skipped, an archived day too (it can
// no longer be derived). An error on one day never stops the other days; the
// errors are joined and returned, and the next pass retries. A failing sink
// (no token, unreachable instance) would fail every remaining day the same
// way, so it ends the pass after the first.
func runAutopilotPass(ctx context.Context, out io.Writer, deps syncDeps, cfg config.Config, cfgPath string, st *store.Store, today civil.Date, dryRun bool) error {
	manual, err := manualDays(ctx, st)
	if err != nil {
		return err
	}
	var sinkErr error
	newSink := deps.NewSink
	deps.NewSink = func(c config.Config, path string) (sink.TimeSink, error) {
		target, err := newSink(c, path)
		if err != nil {
			sinkErr = err
		}
		return target, err
	}
	var errs []error
	printed := false
	for _, day := range auto.AutopilotDays(today, auto.LookbackDays(cfg.Auto)) {
		if ctx.Err() != nil {
			break
		}
		if manual[day.String()] {
			continue
		}
		archived, err := isArchivedDay(ctx, st, day)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", day, err))
			continue
		}
		if archived {
			continue
		}
		did, err := bookAutoDay(ctx, out, deps, cfg, cfgPath, st, day, dryRun, true)
		printed = printed || did
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", day, err))
		}
		if sinkErr != nil {
			break
		}
	}
	if !printed && len(errs) == 0 {
		_, _ = fmt.Fprintln(out, deps.Render.Gutter(render.StatusInfo, msgAutopilotNothingToBook))
	}
	return errors.Join(errs...)
}

// manualDays returns the days that are the user's own decision and which the
// autopilot therefore never touches: a day with a confirmation made through
// `ticktack review` (the only way to book by hand with `ticktack sync`), and a
// day whose review session confirmed or corrected something. A session that
// only looked at a day does not claim it. The autopilot itself records
// neither, so the days it booked stay its own.
func manualDays(ctx context.Context, st *store.Store) (map[string]bool, error) {
	records, err := st.ReviewRecords(ctx)
	if err != nil {
		return nil, err
	}
	manual := map[string]bool{}
	for _, r := range records {
		if r.Confirmed > 0 || r.Corrections > 0 {
			manual[r.Day] = true
		}
	}
	days, err := st.ConfirmedDays(ctx)
	if err != nil {
		return nil, err
	}
	for _, d := range days {
		manual[d] = true
	}
	return manual, nil
}

func isArchivedDay(ctx context.Context, st archivedDayReader, day civil.Date) (bool, error) {
	_, found, err := st.ArchivedDay(ctx, day.String())
	return found, err
}

// digestWriteTimeout bounds the digest write after a cancelled run.
const digestWriteTimeout = 30 * time.Second

// digestStoredWidth keeps long reasons whole in the stored digest.
const digestStoredWidth = 400

// storedDigestRenderer renders the digest text kept in the database: ASCII,
// no colour and wide, so it does not depend on the terminal of the run that
// wrote it and `ticktack digest` can show it again unchanged.
func storedDigestRenderer() render.Renderer {
	return render.Renderer{Glyphs: render.Plain().Glyphs, Width: digestStoredWidth}
}

func printAutoLocked(out io.Writer, r render.Renderer, lock auto.LockReason, cfgPath string, streak, required int) {
	var lines []string
	if lock == auto.LockedDisabled {
		lines = append(lines, r.Gutter(render.StatusWarn, fmt.Sprintf(msgAutoLockedDisabled, SanitizeForTerminal(cfgPath))))
	}
	lines = append(lines,
		r.Gutter(render.StatusWarn, fmt.Sprintf(msgAutoLockedStreak, streak, required)),
		r.Gutter(render.StatusInfo, msgAutoLockedNothing),
		r.Gutter(render.StatusInfo, msgAutoLockedProceed))
	_, _ = fmt.Fprintln(out, strings.Join(lines, "\n"))
}

func runDigest(ctx context.Context, out io.Writer, deps syncDeps) error {
	st, err := deps.OpenStore()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	d, found, err := st.LastDigest(ctx)
	if err != nil {
		return err
	}
	if !found {
		_, err = fmt.Fprintln(out, deps.Render.Gutter(render.StatusInfo, msgDigestNone))
		return err
	}
	_, err = fmt.Fprintln(out, d.Text)
	return err
}
