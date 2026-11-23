package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/JtheGunner/ticktack/internal/config"
)

// maxRemovedValueRunes cuts the old value shown for a removed key.
const maxRemovedValueRunes = 80

// configUpdateOptions are the flags of `ticktack config update`.
type configUpdateOptions struct {
	Yes    bool
	DryRun bool
}

// configUpdateDeps holds what `config update` touches outside of pure logic.
type configUpdateDeps struct {
	ConfigPath func() (string, error)
}

func newConfigCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: msgConfigShort,
	}
	cmd.AddCommand(newConfigUpdateCommand())
	return cmd
}

func newConfigUpdateCommand() *cobra.Command {
	var opts configUpdateOptions
	cmd := &cobra.Command{
		Use:   "update",
		Short: msgConfigUpdateShort,
		Long:  msgConfigUpdateLong,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			deps := configUpdateDeps{ConfigPath: defaultDoctorDeps(cmd).ConfigPath}
			return runConfigUpdate(ctx, deps, opts, bufio.NewReader(cmd.InOrStdin()), cmd.OutOrStdout())
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&opts.Yes, "yes", "y", false, msgConfigUpdateYesFlag)
	f.BoolVar(&opts.DryRun, "dry-run", false, msgConfigUpdateDryRunFlag)
	return cmd
}

// runConfigUpdate brings the config file in line with the option templates:
// it previews the plan and, unless --dry-run, applies it after confirmation.
// It never loads the config, so it also works on files Load rejects.
func runConfigUpdate(ctx context.Context, deps configUpdateDeps, opts configUpdateOptions, in *bufio.Reader, out io.Writer) error {
	path, err := deps.ConfigPath()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf(msgConfigUpdateNoConfig, path)
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	plan, err := config.PlanUpdate(string(data))
	if err != nil {
		return fmt.Errorf(msgConfigUpdatePlanFailed, path, err)
	}
	if !plan.Changed() {
		_, _ = fmt.Fprintf(out, msgConfigUpdateUpToDate+"\n", path)
		return nil
	}
	printConfigUpdatePreview(out, plan)
	if opts.DryRun {
		return nil
	}
	if !opts.Yes {
		confirmed, err := confirmConfigUpdate(ctx, in, out)
		if err != nil {
			return err
		}
		if !confirmed {
			_, _ = fmt.Fprintln(out, msgConfigUpdateAborted)
			return nil
		}
	}
	backupPath, err := config.ApplyUpdate(path, plan)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, msgConfigUpdateDone+"\n", path, backupPath)
	return nil
}

func printConfigUpdatePreview(out io.Writer, plan config.UpdatePlan) {
	for _, added := range plan.Added {
		_, _ = fmt.Fprintf(out, msgConfigUpdateAdded+"\n", SanitizeForTerminal(added.ID))
		for _, line := range added.Lines {
			_, _ = fmt.Fprintf(out, msgConfigUpdateAddedLine+"\n", SanitizeForTerminal(line))
		}
	}
	for _, removed := range plan.Removed {
		id := SanitizeForTerminal(removed.ID)
		value := truncateRunes(SanitizeForTerminal(removed.Value), maxRemovedValueRunes, "")
		if value == "" {
			_, _ = fmt.Fprintf(out, msgConfigUpdateRemovedNone+"\n", id)
			continue
		}
		_, _ = fmt.Fprintf(out, msgConfigUpdateRemoved+"\n", id, value)
	}
	_, _ = fmt.Fprintf(out, msgConfigUpdateSummary+"\n", len(plan.Added), len(plan.Removed))
}

// confirmConfigUpdate asks the [Y/n] question; an empty answer means yes, end
// of input means no. A read error other than end of input, such as the
// cancellation of ctx, is returned.
func confirmConfigUpdate(ctx context.Context, in *bufio.Reader, out io.Writer) (bool, error) {
	_, _ = fmt.Fprintf(out, msgConfigUpdateConfirm, msgConfigUpdateAsk)
	line, err := in.ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	if err != nil && answer == "" {
		_, _ = fmt.Fprintln(out)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return false, ctxErr
		}
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		return false, err
	}
	return answer == "" || answer == "y" || answer == "yes", nil
}
