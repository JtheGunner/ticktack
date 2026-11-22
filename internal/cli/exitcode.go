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

// ExitCode maps an error returned by the root command to a process exit code:
// 75 while another booking run is active, 130 when the only error is the
// cancellation, and 1 for any other failure. A real failure joined with the
// cancellation (for example an undo entry that failed before Ctrl-C) exits 1,
// so a script never mistakes it for a clean interruption.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	if errors.Is(err, ErrBookingRunActive) {
		return ExitBookingRunActive
	}
	if errors.Is(err, context.Canceled) && len(Failures(err)) == 0 {
		return ExitInterrupted
	}
	return 1
}

// Failures returns the leaf errors of err without the cancellation: it walks
// errors.Join trees and drops every error that is only a cancellation, so the
// real failures of a cancelled run can be reported next to the interruption.
func Failures(err error) []error {
	if err == nil || errors.Is(err, context.Canceled) && errors.Unwrap(err) == nil && !isJoin(err) {
		return nil
	}
	if join, ok := err.(interface{ Unwrap() []error }); ok {
		var kept []error
		for _, child := range join.Unwrap() {
			kept = append(kept, Failures(child)...)
		}
		return kept
	}
	if errors.Is(err, context.Canceled) {
		// A wrapper around the cancellation: a failure only if it wraps more.
		if len(Failures(errors.Unwrap(err))) == 0 {
			return nil
		}
	}
	return []error{err}
}

func isJoin(err error) bool {
	_, ok := err.(interface{ Unwrap() []error })
	return ok
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
