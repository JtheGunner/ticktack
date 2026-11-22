package cli

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JtheGunner/ticktack/internal/runlock"
)

// lockedTestDeps returns unlocked auto deps whose booking lock lives in a
// temporary directory, and the path of that lock file.
func lockedTestDeps(t *testing.T) (syncDeps, *recordingSink, string) {
	t.Helper()
	deps, rec := autoTestDeps(t, true, 2)
	seedCleanDays(t, deps, 2)
	path := filepath.Join(t.TempDir(), "booking.lock")
	deps.AcquireLock = func() (func(), error) { return acquireBookingLockAt(path) }
	return deps, rec, path
}

// holdLock takes the booking lock as another run would.
func holdLock(t *testing.T, path string) *runlock.Lock {
	t.Helper()
	held, err := runlock.Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = held.Release() })
	return held
}

func execBookingCommand(t *testing.T, deps syncDeps, args ...string) (string, error) {
	t.Helper()
	root := NewRootCommand()
	for _, c := range root.Commands() {
		switch c.Name() {
		case "sync":
			root.RemoveCommand(c)
			root.AddCommand(newSyncCommandWith(deps))
		case "auto":
			root.RemoveCommand(c)
			root.AddCommand(newAutoCommandWith(deps))
		case "undo":
			root.RemoveCommand(c)
			root.AddCommand(newUndoCommandWith(deps))
		}
	}
	var out strings.Builder
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestBookingRunIsRejectedWhileAnotherHoldsTheLock(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"sync", []string{"sync", "--day", "2026-01-05"}},
		{"auto", []string{"auto", "--day", "2026-01-05"}},
		{"undo", []string{"undo"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps, rec, path := lockedTestDeps(t)
			confirmDay(t, deps)
			holdLock(t, path)

			out, err := execBookingCommand(t, deps, tt.args...)
			if !errors.Is(err, ErrBookingRunActive) {
				t.Fatalf("err = %v, want ErrBookingRunActive (output %q)", err, out)
			}
			if got := ExitCode(err); got != ExitBookingRunActive {
				t.Errorf("exit code = %d, want %d", got, ExitBookingRunActive)
			}
			if err.Error() != msgBookingRunActive {
				t.Errorf("message = %q, want %q", err.Error(), msgBookingRunActive)
			}
			if len(rec.calls) != 0 {
				t.Errorf("the rejected run reached the booking target: %v", rec.calls)
			}
		})
	}
}

func TestDryRunNeedsNoBookingLock(t *testing.T) {
	for _, command := range []string{"sync", "auto"} {
		t.Run(command, func(t *testing.T) {
			deps, rec, path := lockedTestDeps(t)
			confirmDay(t, deps)
			holdLock(t, path)

			out, err := execBookingCommand(t, deps, command, "--day", "2026-01-05", "--dry-run")
			if err != nil {
				t.Fatalf("dry run failed while the lock is held: %v (output %q)", err, out)
			}
			if !strings.Contains(out, "ABC-1") {
				t.Errorf("dry run printed no plan:\n%s", out)
			}
			if len(rec.items) != 0 {
				t.Errorf("dry run booked: %v", rec.items)
			}
		})
	}
}

func TestBookingLockIsReleasedAfterEachRun(t *testing.T) {
	deps, rec, path := lockedTestDeps(t)
	confirmDay(t, deps)
	held := holdLock(t, path)
	if _, err := execBookingCommand(t, deps, "sync", "--day", "2026-01-05"); !errors.Is(err, ErrBookingRunActive) {
		t.Fatalf("err = %v, want ErrBookingRunActive", err)
	}
	if err := held.Release(); err != nil {
		t.Fatal(err)
	}

	// Each run must give the lock back, or the next one would be rejected.
	for _, args := range [][]string{
		{"sync", "--day", "2026-01-05"},
		{"undo"},
		{"auto", "--day", "2026-01-05"},
		{"sync", "--day", "not-a-day"},
		{"undo"},
	} {
		_, err := execBookingCommand(t, deps, args...)
		if errors.Is(err, ErrBookingRunActive) {
			t.Fatalf("%v: the previous run kept the lock", args)
		}
	}
	if len(rec.calls) == 0 {
		t.Error("no run reached the booking target after the lock was released")
	}
}

func TestBookingLockFailureIsNotReportedAsActiveRun(t *testing.T) {
	deps, _, _ := lockedTestDeps(t)
	missing := filepath.Join(t.TempDir(), "missing-directory", "booking.lock")
	deps.AcquireLock = func() (func(), error) { return acquireBookingLockAt(missing) }
	_, err := execBookingCommand(t, deps, "undo")
	if err == nil || errors.Is(err, ErrBookingRunActive) {
		t.Fatalf("err = %v, want a plain lock error", err)
	}
	if got := ExitCode(err); got != 1 {
		t.Errorf("exit code = %d, want 1", got)
	}
}

func TestDefaultBookingLockLivesInTheDataDirectory(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	release, err := acquireBookingLock()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := runlock.Acquire(filepath.Join(data, "ticktack", "booking.lock")); !errors.Is(err, runlock.ErrHeld) {
		t.Fatalf("err = %v, want the default lock file to be held", err)
	}
}
