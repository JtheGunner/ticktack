package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/JtheGunner/ticktack/internal/paths"
	"github.com/JtheGunner/ticktack/internal/runlock"
)

// ErrBookingRunActive is returned by the sync, auto and undo commands when
// another booking run holds the lock. The process then exits with
// ExitBookingRunActive.
var ErrBookingRunActive = errors.New(msgBookingRunActive)

// acquireBookingLock takes the lock that keeps booking runs apart. The lock
// file sits next to the database the runs write to.
func acquireBookingLock() (func(), error) {
	path, err := paths.BookingLockPath()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	return acquireBookingLockAt(path)
}

// acquireBookingLockAt takes the booking lock on the file at path without
// waiting and returns the function that gives it back.
func acquireBookingLockAt(path string) (func(), error) {
	lock, err := runlock.Acquire(path)
	if errors.Is(err, runlock.ErrHeld) {
		return nil, ErrBookingRunActive
	}
	if err != nil {
		return nil, fmt.Errorf(msgBookingLockFailed, err)
	}
	// Closing a lock file that was never written to has nothing to report.
	return func() { _ = lock.Release() }, nil
}

// lockBookingRun takes the booking lock for a run that changes YouTrack or
// the ledger. A dry run changes neither and takes no lock, and neither do
// deps without AcquireLock.
func (d syncDeps) lockBookingRun(dryRun bool) (func(), error) {
	if dryRun || d.AcquireLock == nil {
		return func() {}, nil
	}
	return d.AcquireLock()
}
