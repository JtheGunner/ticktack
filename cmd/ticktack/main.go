// Command ticktack is the entrypoint of the ticktack CLI.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/JtheGunner/ticktack/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	// The first signal cancels ctx, which makes a booking run finish the item
	// in flight and stop. stop() then restores the default handling, so a
	// second signal terminates the process at once instead of waiting.
	go func() {
		<-ctx.Done()
		stop()
	}()
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run executes the CLI and returns the process exit code. A cancelled ctx
// ends the command in an orderly way and yields cli.ExitInterrupted.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	root := cli.NewRootCommand()
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	// A prompt blocked on stdin must give up on the first signal as well.
	root.SetIn(cli.NewContextReader(ctx, os.Stdin))
	err := root.ExecuteContext(ctx)
	code := cli.ExitCode(err)
	if errors.Is(err, context.Canceled) {
		err = cli.ErrInterrupted
	}
	if err != nil && !cli.IsSilent(err) {
		_, _ = fmt.Fprintln(stderr, "error:", cli.SanitizeForTerminal(err.Error()))
	}
	return code
}
