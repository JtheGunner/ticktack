package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/JtheGunner/ticktack/internal/auto"
	"github.com/JtheGunner/ticktack/internal/ledger"
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
		Long:  msgAutoLong,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAuto(cmd.Context(), cmd.OutOrStdout(), deps, dayFlag, dryRun)
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
			return runDigest(cmd.Context(), cmd.OutOrStdout(), deps)
		},
	}
	cmd.Flags().BoolVar(&last, "last", true, "show the most recent digest (the default)")
	return cmd
}

func runAuto(ctx context.Context, out io.Writer, deps syncDeps, dayFlag string, dryRun bool) error {
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

	streak, err := review.TrackRecord(ctx, st, now)
	if err != nil {
		return err
	}
	if lock := auto.Gate(cfg.Auto, streak); lock != auto.Unlocked {
		printAutoLocked(out, lock, cfgPath, streak, auto.RequiredDays(cfg.Auto))
		return nil
	}

	d, err := review.LoadDay(ctx, st, cfg, day)
	if err != nil {
		return err
	}
	sel := auto.Select(d.Items, auto.LowestTiers(d.Blocks), d.CapMinutes)

	var report ledger.Report
	var syncErr error
	if len(sel.Book) > 0 {
		// The sink is built only now: the gate passed and there is something to book.
		target, err := deps.NewSink(cfg, cfgPath)
		if err != nil {
			return err
		}
		report, syncErr = ledger.Sync(ctx, sel.Items(), target, ledger.SyncOptions{Repo: st, DryRun: dryRun, Now: deps.Now})
	}
	digest := auto.BuildDigest(day, sel, report, dryRun)
	text := renderDigest(digest)
	if !dryRun {
		if _, err := st.AddDigest(ctx, store.Digest{Day: day.String(), CreatedAt: deps.Now(), Text: text}); err != nil {
			return err
		}
	}
	if len(sel.Book) == 0 && len(sel.Skipped) == 0 {
		_, _ = fmt.Fprintf(out, msgAutoNothingToBook+"\n", day)
		return syncErr
	}
	_, _ = fmt.Fprintln(out, text)
	return syncErr
}

func printAutoLocked(out io.Writer, lock auto.LockReason, cfgPath string, streak, required int) {
	if lock == auto.LockedDisabled {
		_, _ = fmt.Fprintf(out, msgAutoLockedDisabled+"\n", cfgPath)
	}
	_, _ = fmt.Fprintf(out, msgAutoLockedStreak+"\n", streak, required)
	_, _ = fmt.Fprintln(out, msgAutoLockedNothing)
	_, _ = fmt.Fprintln(out, msgAutoLockedProceed)
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
		_, err = fmt.Fprintln(out, msgDigestNone)
		return err
	}
	_, err = fmt.Fprintln(out, d.Text)
	return err
}
