package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/JtheGunner/ticktack/internal/cli"
	"github.com/JtheGunner/ticktack/internal/paths"
	"github.com/JtheGunner/ticktack/internal/runlock"
)

func isolate(t *testing.T) {
	t.Helper()
	keyring.MockInit()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("SHELL", "/bin/zsh")
}

func TestDoctorExitCodeNonZeroOnFailure(t *testing.T) {
	isolate(t)              // no config, so the checks fail without any network access
	t.Setenv("LC_ALL", "C") // ASCII glyphs, whatever the developer's locale
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"doctor"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("exit code 0 although checks failed:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "x  config:") {
		t.Errorf("failure not reported on stdout:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("failure printed a second time on stderr: %q", stderr.String())
	}
}

func TestRunReturnsZeroOnSuccessAndPrintsErrorsOnce(t *testing.T) {
	isolate(t)
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("version exit code = %d, stderr %q", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(context.Background(), []string{"no-such-command"}, &stdout, &stderr); code == 0 {
		t.Fatal("unknown command returned 0")
	}
	if strings.Count(stderr.String(), "error:") != 1 {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestBookingCommandsExitWithDefinedCodeWhileAnotherRunIsActive(t *testing.T) {
	isolate(t)
	lockPath, err := paths.BookingLockPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		t.Fatal(err)
	}
	held, err := runlock.Acquire(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Release() }()

	// The lock is checked before the configuration, so no command gets as
	// far as YouTrack.
	for _, command := range []string{"sync", "auto", "undo"} {
		t.Run(command, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), []string{command}, &stdout, &stderr)
			if code != cli.ExitBookingRunActive {
				t.Errorf("exit code = %d, want %d", code, cli.ExitBookingRunActive)
			}
			want := "error: another ticktack booking run (sync, auto or undo) is active: try again when it has finished\n"
			if stderr.String() != want {
				t.Errorf("stderr = %q, want %q", stderr.String(), want)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want it empty", stdout.String())
			}
		})
	}
}
