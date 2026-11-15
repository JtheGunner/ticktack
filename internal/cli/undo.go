package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/JtheGunner/ticktack/internal/ledger"
)

func newUndoCommand() *cobra.Command {
	return newUndoCommandWith(defaultSyncDeps())
}

func newUndoCommandWith(deps syncDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "undo",
		Short: "Revert the last sync",
		Long: `Revert the last booking run: items it created are deleted from YouTrack and
items it updated are restored to their previous minutes. Running undo again
reverts the run before it.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUndo(cmd.Context(), cmd.OutOrStdout(), deps)
		},
	}
}

func runUndo(ctx context.Context, out io.Writer, deps syncDeps) error {
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
		_, _ = fmt.Fprintln(out, "Nothing to undo.")
		return nil
	}
	target, err := deps.NewSink(cfg, cfgPath)
	if err != nil {
		return err
	}
	res, err := ledger.Undo(ctx, target, st, deps.Now)
	_, _ = fmt.Fprintf(out, "Deleted %d, restored %d, forgotten %d.\n", res.Deleted, res.Restored, res.Forgotten)
	return err
}
