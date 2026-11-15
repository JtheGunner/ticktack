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
		Short:         "ticktack command line tool",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newVersionCommand())
	root.AddCommand(newAgentCommand())
	root.AddCommand(newHookCommand())
	addDailyCommands(root)
	root.AddCommand(newInitCommand())
	root.AddCommand(newDoctorCommand())
	return root
}
