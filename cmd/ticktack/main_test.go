package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
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
	isolate(t) // no config, so the checks fail without any network access
	var stdout, stderr bytes.Buffer
	code := run([]string{"doctor"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("exit code 0 although checks failed:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "FAIL config:") {
		t.Errorf("failure not reported on stdout:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("failure printed a second time on stderr: %q", stderr.String())
	}
}

func TestRunReturnsZeroOnSuccessAndPrintsErrorsOnce(t *testing.T) {
	isolate(t)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("version exit code = %d, stderr %q", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"no-such-command"}, &stdout, &stderr); code == 0 {
		t.Fatal("unknown command returned 0")
	}
	if strings.Count(stderr.String(), "error:") != 1 {
		t.Errorf("stderr = %q", stderr.String())
	}
}
