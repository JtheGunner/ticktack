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
)

func newPurgeCommand() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "purge",
		Short: "Delete all local ticktack data",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runPurge(cmd.Context(), defaultDailyDeps(cmd), yes)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "do not ask for confirmation")
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
