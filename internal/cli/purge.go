package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/JtheGunner/ticktack/internal/render"
	"github.com/JtheGunner/ticktack/internal/retention"
)

func newPurgeCommand() *cobra.Command {
	var yes, expired, dryRun bool
	cmd := &cobra.Command{
		Use:   "purge",
		Short: msgPurgeShort,
		Long:  msgPurgeLong,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if dryRun && !expired {
				return errors.New(msgPurgeDryRunFlag)
			}
			if expired {
				return runPurgeExpired(cmd.Context(), defaultDailyDeps(cmd), yes, dryRun)
			}
			return runPurge(cmd.Context(), defaultDailyDeps(cmd), yes)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, msgPurgeYesFlag)
	cmd.Flags().BoolVar(&expired, "expired", false, msgPurgeExpiredFlag)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, msgPurgeDryRunHelp)
	return cmd
}

// runPurge deletes all local data after confirmation. Without --yes it asks
// on deps.In; anything but y/yes aborts, and a closed input is an error so
// scripts never delete by accident.
func runPurge(ctx context.Context, deps dailyDeps, yes bool) error {
	if !yes {
		confirmed, err := confirmDeletion(deps.In, deps.Out, msgPurgePrompt)
		if err != nil {
			return err
		}
		if !confirmed {
			_, err = fmt.Fprintln(deps.Out, deps.Render.Gutter(render.StatusInfo, msgPurgeAborted))
			return err
		}
	}
	st, err := deps.OpenStore()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	if err := st.Purge(ctx); err != nil {
		return err
	}
	_, err = fmt.Fprintln(deps.Out, deps.Render.Gutter(render.StatusOK, msgPurged))
	return err
}

// runPurgeExpired runs the retention cleanup by hand. A dry run only prints
// the counts. Otherwise it states what is lost and asks like purge does
// (--yes skips the question); nothing is asked when nothing would be deleted.
func runPurgeExpired(ctx context.Context, deps dailyDeps, yes, dryRun bool) error {
	cfg, err := deps.LoadConfig()
	if err != nil {
		return err
	}
	if cfg.RetentionDays <= 0 {
		_, err = fmt.Fprintln(deps.Out, deps.Render.Gutter(render.StatusInfo, msgPurgeExpiredOff))
		return err
	}
	if !dryRun && deps.AcquireLock != nil {
		release, err := deps.AcquireLock()
		if err != nil {
			return err
		}
		defer release()
	}
	st, err := deps.OpenStore()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	plan, err := retention.Run(ctx, st, cfg, deps.Now(), true)
	if err != nil {
		return err
	}
	if len(plan.Days) == 0 {
		_, err = fmt.Fprintln(deps.Out, deps.Render.Gutter(render.StatusInfo, fmt.Sprintf(msgPurgeExpiredNone, cfg.RetentionDays)))
		return err
	}
	line := msgPurgeExpiredPlan(plan.Events, len(plan.Days), cfg.RetentionDays, dryRun)
	if dryRun {
		_, err = fmt.Fprintln(deps.Out, deps.Render.Gutter(render.StatusInfo, line))
		return err
	}
	if _, err := fmt.Fprintln(deps.Out, deps.Render.Gutter(render.StatusWarn, line)); err != nil {
		return err
	}
	if !yes {
		confirmed, err := confirmDeletion(deps.In, deps.Out, msgPurgeExpiredPrompt)
		if err != nil {
			return err
		}
		if !confirmed {
			_, err = fmt.Fprintln(deps.Out, deps.Render.Gutter(render.StatusInfo, msgPurgeAborted))
			return err
		}
	}
	done, err := retention.Run(ctx, st, cfg, deps.Now(), false)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(deps.Out, deps.Render.Gutter(render.StatusOK, msgPurgeExpiredDone(done.Events, len(done.Days))))
	return err
}

// confirmDeletion prints prompt on out and reads a [y/N] answer from in. Only
// y/yes confirms. A closed input is an error, not a "no", so a script learns
// that nothing was asked.
func confirmDeletion(in io.Reader, out io.Writer, prompt string) (bool, error) {
	if _, err := fmt.Fprint(out, prompt); err != nil {
		return false, err
	}
	line, err := bufio.NewReader(in).ReadString('\n')
	// A final answer without a trailing newline still counts.
	partialAnswer := errors.Is(err, io.EOF) && strings.TrimSpace(line) != ""
	if err != nil && !partialAnswer {
		if errors.Is(err, io.EOF) {
			_, _ = fmt.Fprintln(out)
			return false, errors.New(msgNotInteractive)
		}
		return false, fmt.Errorf("read confirmation: %w", err)
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}
