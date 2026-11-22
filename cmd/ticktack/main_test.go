package main

import (
	"bytes"
	"context"
	"errors"
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

func TestFinishReportsFailuresAndInterruptionWithTheRightExitCode(t *testing.T) {
	const secret = "perm:SECRET-TOKEN"
	failure := errors.New("ABC-1: HTTP 401 unauthorized")
	tests := []struct {
		name     string
		err      error
		wantCode int
		want     []string
		wantNot  []string
	}{
		{"only cancellation", context.Canceled, cli.ExitInterrupted,
			[]string{"error: interrupted:"}, []string{"401"}},
		{"failure plus cancellation", errors.Join(failure, context.Canceled), 1,
			[]string{"error: ABC-1: HTTP 401 unauthorized", "error: interrupted:"}, []string{secret}},
		{"cancellation first then failure", errors.Join(context.Canceled, failure), 1,
			[]string{"ABC-1: HTTP 401 unauthorized", "interrupted:"}, nil},
		{"failure only", failure, 1,
			[]string{"error: ABC-1: HTTP 401 unauthorized"}, []string{"interrupted"}},
		{"lock active", cli.ErrBookingRunActive, cli.ExitBookingRunActive,
			[]string{"another ticktack booking run"}, []string{"interrupted"}},
		{"lock active and cancelled", errors.Join(cli.ErrBookingRunActive, context.Canceled), cli.ExitBookingRunActive,
			[]string{"another ticktack booking run"}, nil},
		{"success", nil, 0, nil, []string{"error"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			if code := finish(tt.err, &stderr); code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d (stderr %q)", code, tt.wantCode, stderr.String())
			}
			for _, w := range tt.want {
				if !strings.Contains(stderr.String(), w) {
					t.Errorf("stderr %q lacks %q", stderr.String(), w)
				}
			}
			for _, w := range tt.wantNot {
				if strings.Contains(stderr.String(), w) {
					t.Errorf("stderr %q contains %q", stderr.String(), w)
				}
			}
		})
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
