//go:build !unix

package runlock

import (
	"errors"
	"os"
)

// lockFile refuses to lock: without an operating system lock that survives a
// crash, a run must not pretend to be exclusive.
func lockFile(*os.File) error {
	return errors.New("file locks are not supported on this platform")
}
