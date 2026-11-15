// Command ticktack is the entrypoint of the ticktack CLI.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/JtheGunner/ticktack/internal/cli"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes the CLI and returns the process exit code.
func run(args []string, stdout, stderr io.Writer) int {
	root := cli.NewRootCommand()
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	err := root.Execute()
	if err != nil && !cli.IsSilent(err) {
		fmt.Fprintln(stderr, "error:", err)
	}
	return cli.ExitCode(err)
}
