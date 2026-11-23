// Package runlock provides an exclusive lock between processes that is backed
// by an advisory lock of the operating system on a file. The kernel drops the
// lock when the holding process ends, however it ends, so a crash never
// leaves a stale lock behind.
package runlock

import (
	"errors"
	"fmt"
	"os"
)

// ErrHeld is returned by Acquire when another holder has the lock.
var ErrHeld = errors.New("lock is held by another process")

// Lock is a held lock. Release gives it back.
type Lock struct {
	file *os.File
}

// Acquire takes the lock on the file at path without waiting and creates the
// file when it is missing. It returns ErrHeld when the lock is taken. The
// file is kept after Release: removing it would let two processes lock two
// different files under the same name.
func Acquire(path string) (*Lock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := lockFile(file); err != nil {
		_ = file.Close()
		if errors.Is(err, ErrHeld) {
			return nil, ErrHeld
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return &Lock{file: file}, nil
}

// Release gives the lock back by closing the file.
func (l *Lock) Release() error {
	if err := l.file.Close(); err != nil {
		return fmt.Errorf("release lock: %w", err)
	}
	return nil
}
