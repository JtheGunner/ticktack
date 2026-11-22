package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"cloud.google.com/go/civil"
	"github.com/spf13/cobra"

	"github.com/JtheGunner/ticktack/internal/auto"
	"github.com/JtheGunner/ticktack/internal/ledger"
	"github.com/JtheGunner/ticktack/internal/render"
	"github.com/JtheGunner/ticktack/internal/review"
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
		Short: "Book the safe blocks of a day automatically (opt-in, needs a track record)",
		Long:  msgAutoLong + msgBookingLockHelpDryRun,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAuto(cmd.Context(), cmd.OutOrStdout(), deps.forCommand(cmd), dayFlag, dryRun)
		},
	}
	cmd.Flags().StringVar(&dayFlag, "day", "", "local day to book as YYYY-MM-DD (default: today)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the plan without booking anything")
	return cmd
}

func newDigestCommand() *cobra.Command {
	return newDigestCommandWith(defaultSyncDeps())
}

func newDigestCommandWith(deps syncDeps) *cobra.Command {
	var last bool
	cmd := &cobra.Command{
		Use:   "digest",
		Short: "Show the digest of the last auto run",
		Long:  msgDigestLong,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDigest(cmd.Context(), cmd.OutOrStdout(), deps.forCommand(cmd))
		},
	}
	cmd.Flags().BoolVar(&last, "last", true, "show the most recent digest (the default)")
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
	day, err := parseDay(dayFlag, now)
	if err != nil {
		return err
	}
	st, err := deps.OpenStore()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	// The gate needs no more than the required days, so the walk stops there.
	streak, err := review.TrackRecord(ctx, st, cfg, now, auto.RequiredDays(cfg.Auto))
	if err != nil {
		return err
	}
	if lock := auto.Gate(cfg.Auto, streak); lock != auto.Unlocked {
		printAutoLocked(out, deps.Render, lock, cfgPath, streak, auto.RequiredDays(cfg.Auto))
		return nil
	}

	d, err := review.LoadDay(ctx, st, cfg, day)
	if err != nil {
		return err
	}
	sel := auto.Select(d.Items, auto.LowestTiers(d.Blocks), d.CapMinutes)

	// A capped day needs a human, so nothing of it is changed, deletions included.
	var keep []string
	stale := false
	if !sel.CapExceeded {
		keep, stale, err = reconciliation(ctx, st, d.Items, day)
		if err != nil {
			return err
		}
	}

	var report ledger.Report
	var syncErr error
	if len(sel.Book) > 0 || stale {
		// The sink is built only now: the gate passed and there is something
		// to book or a stale booking to remove.
		target, err := deps.NewSink(cfg, cfgPath)
		if err != nil {
			return err
		}
		provided, provisioned, err := provisionForAuto(ctx, st, cfg, target, sel, dryRun, deps.Now)
		switch {
		case endsRun(ctx, err):
			// Nothing is sent; the digest lists every item as not sent.
			report, syncErr = ledger.NotSentReport(sel.Items()), err
		case err != nil:
			return err
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
	if !dryRun {
		// The digest of a cancelled run is still stored: it lists what was not
		// sent, so the write must not use the cancelled context.
		digestCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), digestWriteTimeout)
		defer cancel()
		if _, err := st.AddDigest(digestCtx, store.Digest{Day: day.String(), CreatedAt: deps.Now(), Text: text}); err != nil {
			return err
		}
	}
	if len(sel.Book) == 0 && len(sel.Skipped) == 0 && len(report.Items) == 0 {
		_, _ = fmt.Fprintln(out, deps.Render.Gutter(render.StatusInfo, fmt.Sprintf(msgAutoNothingToBook, day)))
		return syncErr
	}
	_, _ = fmt.Fprintln(out, renderDigest(digest, deps.Render))
	return syncErr
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
