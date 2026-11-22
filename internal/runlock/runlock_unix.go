//go:build unix

package runlock

import (
	"errors"
	"os"
	"syscall"
)

// lockFile takes an exclusive flock on file without blocking. The lock
// belongs to the open file, so closing it or ending the process releases it.
func lockFile(file *os.File) error {
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, syscall.EINTR):
			continue
		case errors.Is(err, syscall.EWOULDBLOCK):
			return ErrHeld
		default:
			return err
		}
	}
}
