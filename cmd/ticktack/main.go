// Command ticktack is the entrypoint of the ticktack CLI.
package main

import (
	"fmt"
	"os"

	"github.com/JtheGunner/ticktack/internal/cli"
)

func main() {
	if err := cli.NewRootCommand().Execute(); err != nil {
		if !cli.IsSilent(err) {
			fmt.Fprintln(os.Stderr, "error:", err)
		}
		os.Exit(cli.ExitCode(err))
	}
}
