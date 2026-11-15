package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
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
		confirmed, err := confirmPurge(deps)
		if err != nil {
			return err
		}
		if !confirmed {
			_, err = fmt.Fprintln(deps.Out, msgPurgeAborted)
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
	_, err = fmt.Fprintln(deps.Out, msgPurged)
	return err
}

func confirmPurge(deps dailyDeps) (bool, error) {
	if _, err := fmt.Fprint(deps.Out, msgPurgePrompt); err != nil {
		return false, err
	}
	line, err := bufio.NewReader(deps.In).ReadString('\n')
	// A final answer without a trailing newline still counts.
	partialAnswer := errors.Is(err, io.EOF) && strings.TrimSpace(line) != ""
	if err != nil && !partialAnswer {
		if errors.Is(err, io.EOF) {
			_, _ = fmt.Fprintln(deps.Out)
			return false, errors.New(msgNotInteractive)
		}
		return false, fmt.Errorf("read confirmation: %w", err)
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}
