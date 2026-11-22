package cli

import "errors"

// ErrChecksFailed is returned by `doctor` when at least one check failed.
var ErrChecksFailed = errors.New("one or more checks failed")

// ErrInitIncomplete is returned by `init` when at least one step failed. The
// failures are already explained on stdout.
var ErrInitIncomplete = errors.New("init incomplete")

// ExitBookingRunActive is the exit code of the sync, auto and undo commands
// when another booking run is active. It is EX_TEMPFAIL of sysexits.h:
// nothing is wrong and the same command succeeds later, which lets a
// scheduler tell the case apart from a failure.
const ExitBookingRunActive = 75

// ExitCode maps an error returned by the root command to a process exit code.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	if errors.Is(err, ErrBookingRunActive) {
		return ExitBookingRunActive
	}
	return 1
}

// IsSilent reports whether err was already explained on stdout, so main need
// not print it again.
func IsSilent(err error) bool {
	return errors.Is(err, ErrChecksFailed) || errors.Is(err, ErrUninstallIncomplete) || errors.Is(err, ErrInitIncomplete)
}
