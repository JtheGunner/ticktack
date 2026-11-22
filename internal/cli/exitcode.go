package cli

import (
	"context"
	"errors"

	"github.com/JtheGunner/ticktack/internal/ledger"
)

// ErrChecksFailed is returned by `doctor` when at least one check failed.
var ErrChecksFailed = errors.New("one or more checks failed")

// ExitBookingRunActive is the exit code of the sync, auto and undo commands
// when another booking run is active. It is EX_TEMPFAIL of sysexits.h:
// nothing is wrong and the same command succeeds later, which lets a
// scheduler tell the case apart from a failure.
const ExitBookingRunActive = 75

// ExitInterrupted is the exit code of a run that a termination signal
// (SIGINT, SIGTERM) cancelled. It is 128 + SIGINT, the shell convention for a
// Ctrl-C. The item in flight was finished and the others were reported as not
// sent, so running the same command again continues the work.
const ExitInterrupted = 130

// ErrInterrupted is the error text of a cancelled run.
var ErrInterrupted = errors.New(msgInterrupted)

// ExitCode maps an error returned by the root command to a process exit code.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	if errors.Is(err, ErrBookingRunActive) {
		return ExitBookingRunActive
	}
	if errors.Is(err, context.Canceled) {
		return ExitInterrupted
	}
	return 1
}

// endsRun reports whether err ends a booking run before anything was sent: a
// stop error of the sink or a cancelled context.
func endsRun(ctx context.Context, err error) bool {
	return err != nil && (ledger.IsStopError(err) || ctx.Err() != nil)
}

// IsSilent reports whether err was already explained on stdout, so main need
// not print it again.
func IsSilent(err error) bool {
	return errors.Is(err, ErrChecksFailed) || errors.Is(err, ErrUninstallIncomplete)
}
