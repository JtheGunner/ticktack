//go:build unix

package runlock

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// helperLockEnv names the lock file a re-executed test binary takes and
// holds until it is killed.
const helperLockEnv = "TICKTACK_RUNLOCK_HELPER_PATH"

func TestAcquireRejectsSecondHolderUntilRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "booking.lock")
	first, err := Acquire(path)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if _, err := Acquire(path); !errors.Is(err, ErrHeld) {
		t.Fatalf("second acquire: err = %v, want ErrHeld", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	second, err := Acquire(path)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatalf("second release: %v", err)
	}
}

func TestAcquireReportsUnusablePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-directory", "booking.lock")
	_, err := Acquire(path)
	if err == nil {
		t.Fatal("acquire in a missing directory succeeded")
	}
	if errors.Is(err, ErrHeld) {
		t.Fatalf("an unusable path was reported as a held lock: %v", err)
	}
}

// TestHelperHoldsLock is not a test of its own: the crash test re-executes
// the test binary with helperLockEnv set, and this process then holds the
// lock until it is killed.
func TestHelperHoldsLock(t *testing.T) {
	path := os.Getenv(helperLockEnv)
	if path == "" {
		t.Skip("helper process of TestLockIsReleasedWhenTheHolderIsKilled")
	}
	if _, err := Acquire(path); err != nil {
		t.Fatalf("helper acquire: %v", err)
	}
	_, _ = os.Stdout.WriteString("locked\n")
	select {}
}

func TestLockIsReleasedWhenTheHolderIsKilled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "booking.lock")
	helper := exec.Command(os.Args[0], "-test.run=^TestHelperHoldsLock$")
	helper.Env = append(os.Environ(), helperLockEnv+"="+path)
	stdout, err := helper.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	killed := false
	t.Cleanup(func() {
		if !killed {
			_ = helper.Process.Kill()
			_ = helper.Wait()
		}
	})
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "locked\n" {
		t.Fatalf("helper did not take the lock: %q, %v", line, err)
	}
	if _, err := Acquire(path); !errors.Is(err, ErrHeld) {
		t.Fatalf("acquire while the helper runs: err = %v, want ErrHeld", err)
	}

	// SIGKILL leaves the holder no chance to clean up, like a crash.
	if err := helper.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = helper.Wait()
	killed = true

	lock, err := Acquire(path)
	if err != nil {
		t.Fatalf("acquire after the holder was killed: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
}
