// Package cli defines the ticktack command tree.
package cli

import "github.com/spf13/cobra"

// Version is the build version. It can be overridden at build time with
// -ldflags "-X github.com/JtheGunner/ticktack/internal/cli.Version=...".
var Version = "dev"

// NewRootCommand builds the root `ticktack` command with all subcommands.
func NewRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "ticktack",
		Short:         msgRootTicktackShort,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().Bool(noColorFlag, false, msgNoColorUsage)
	root.AddCommand(newVersionCommand())
	root.AddCommand(newAgentCommand())
	root.AddCommand(newHookCommand())
	root.AddCommand(newReviewCommand())
	root.AddCommand(newSyncCommand())
	root.AddCommand(newAutoCommand())
	root.AddCommand(newDigestCommand())
	root.AddCommand(newUndoCommand())
	addDailyCommands(root)
	root.AddCommand(newInitCommand())
	root.AddCommand(newUninstallCommand())
	root.AddCommand(newDoctorCommand())
	root.AddCommand(newImportCommand())
	root.AddCommand(newScanCommand())
	root.AddCommand(newConfigCommand())
	return root
}
