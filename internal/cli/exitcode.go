package cli

import "errors"

// ErrChecksFailed is returned by `doctor` when at least one check failed.
var ErrChecksFailed = errors.New("one or more checks failed")

// ExitCode maps an error returned by the root command to a process exit code.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	return 1
}

// IsSilent reports whether err was already explained on stdout, so main need
// not print it again.
func IsSilent(err error) bool {
	return errors.Is(err, ErrChecksFailed) || errors.Is(err, ErrUninstallIncomplete)
}
