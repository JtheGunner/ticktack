// Command ticktack is the entrypoint of the ticktack CLI.
package main

import (
	"fmt"
	"os"

	"github.com/JtheGunner/ticktack/internal/cli"
)

func main() {
	if err := cli.NewRootCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
