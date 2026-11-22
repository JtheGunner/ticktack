package logfile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)

type fixture struct {
	logger *Logger
	now    time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	fx := &fixture{now: t0}
	fx.logger = New(filepath.Join(t.TempDir(), "state", "ticktack.log"), "agent: ")
	fx.logger.Now = func() time.Time { return fx.now }
	return fx
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n")
}

func TestPrintfWritesPrefixedTimestampedLine(t *testing.T) {
	fx := newFixture(t)
	fx.logger.Printf("append %s event failed: %v", "presence", "disk full")
	got := readLines(t, fx.logger.Path)
	want := "agent: 2026/01/05 09:00:00 append presence event failed: disk full"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %q, want [%q]", got, want)
	}
	info, err := os.Stat(fx.logger.Path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("log file mode %v, want 0600", perm)
	}
}

func TestIdenticalMessagesAreThrottled(t *testing.T) {
	fx := newFixture(t)
	for i := 0; i < 5; i++ {
		fx.logger.Printf("presence sample timed out after %s", 5*time.Second)
		fx.now = fx.now.Add(30 * time.Second)
	}
	if got := readLines(t, fx.logger.Path); len(got) != 1 {
		t.Fatalf("identical messages inside the window were not throttled: %q", got)
	}

	fx.now = t0.Add(DefaultThrottle)
	fx.logger.Printf("presence sample timed out after %s", 5*time.Second)
	got := readLines(t, fx.logger.Path)
	if len(got) != 2 {
		t.Fatalf("message after the window was not written: %q", got)
	}
	if !strings.HasSuffix(got[1], "presence sample timed out after 5s (4 identical messages suppressed)") {
		t.Fatalf("suppressed count missing: %q", got[1])
	}
}

func TestDifferentMessagesAreNotThrottled(t *testing.T) {
	fx := newFixture(t)
	fx.logger.Printf("first")
	fx.logger.Printf("second")
	fx.logger.Printf("first")
	fx.logger.Printf("second")
	if got := readLines(t, fx.logger.Path); len(got) != 2 {
		t.Fatalf("alternating messages: got %q, want one line each", got)
	}
}

func TestThrottleIsPerPrefix(t *testing.T) {
	fx := newFixture(t)
	hook := New(fx.logger.Path, "hook: ")
	hook.Now = fx.logger.Now
	fx.logger.Printf("open store: locked")
	hook.Printf("open store: locked")
	if got := readLines(t, fx.logger.Path); len(got) != 2 {
		t.Fatalf("got %q, want one line per writer", got)
	}
}

// The hook is a new process per prompt, so the throttle must hold across
// Logger values.
func TestThrottleHoldsAcrossLoggers(t *testing.T) {
	fx := newFixture(t)
	for i := 0; i < 3; i++ {
		logger := New(fx.logger.Path, "hook: ")
		logger.Now = fx.logger.Now
		logger.Printf("open store: locked")
	}
	if got := readLines(t, fx.logger.Path); len(got) != 1 {
		t.Fatalf("got %q, want one line", got)
	}
}

func TestThrottleStateDoesNotContainMessageText(t *testing.T) {
	fx := newFixture(t)
	fx.logger.Printf("open store: /Users/someone/secret-project")
	state, err := os.ReadFile(fx.logger.Path + throttleSuffix)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(state), "secret-project") || strings.Contains(string(state), "open store") {
		t.Fatalf("throttle state leaks message text: %q", state)
	}
}

func TestThrottleEdgeCases(t *testing.T) {
	tests := []struct {
		name      string
		prepare   func(fx *fixture)
		wantLines int
	}{
		{
			name: "damaged state file is ignored",
			prepare: func(fx *fixture) {
				mustWrite(t, fx.logger.Path+throttleSuffix, "garbage\nx y z\n\x00\n")
			},
			wantLines: 2,
		},
		{
			name: "state from the future does not suppress",
			prepare: func(fx *fixture) {
				fx.now = t0.Add(-time.Hour) // clock set back after the first message
			},
			wantLines: 2,
		},
		{
			name:      "throttling disabled",
			prepare:   func(fx *fixture) { fx.logger.Throttle = 0 },
			wantLines: 2,
		},
		{
			name:      "inside the window",
			prepare:   func(fx *fixture) { fx.now = t0.Add(DefaultThrottle - time.Second) },
			wantLines: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fx := newFixture(t)
			fx.logger.Printf("same")
			tt.prepare(fx)
			fx.logger.Printf("same")
			if got := readLines(t, fx.logger.Path); len(got) != tt.wantLines {
				t.Fatalf("got %d lines %q, want %d", len(got), got, tt.wantLines)
			}
		})
	}
}

func TestThrottleStateIsBounded(t *testing.T) {
	fx := newFixture(t)
	for i := 0; i < 3*maxThrottleEntries; i++ {
		fx.logger.Printf("message %d", i)
	}
	if got := readLines(t, fx.logger.Path+throttleSuffix); len(got) > maxThrottleEntries {
		t.Fatalf("throttle state holds %d entries, limit %d", len(got), maxThrottleEntries)
	}
}

func TestLogFileStaysBounded(t *testing.T) {
	fx := newFixture(t)
	fx.logger.MaxBytes = 2000
	fx.logger.Throttle = 0
	for i := 0; i < 500; i++ {
		fx.logger.Printf("line %04d %s", i, strings.Repeat("x", 40))
	}
	// One message may be appended after the size check.
	const slack = 200
	for _, path := range []string{fx.logger.Path, fx.logger.Path + backupSuffix} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() > fx.logger.MaxBytes+slack {
			t.Fatalf("%s is %d bytes, limit %d", filepath.Base(path), info.Size(), fx.logger.MaxBytes)
		}
	}
	lines := readLines(t, fx.logger.Path)
	if last := lines[len(lines)-1]; !strings.Contains(last, "line 0499") {
		t.Fatalf("newest line missing after rotation: %q", last)
	}
	if backup := readLines(t, fx.logger.Path+backupSuffix); len(backup) == 0 {
		t.Fatal("backup is empty")
	}
	if _, err := os.Stat(fx.logger.Path + backupSuffix + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temporary rotation file left behind: %v", err)
	}
}

// launchd holds the log open in append mode. Rotation must keep that handle
// writing to the live file.
func TestRotationKeepsOpenAppendHandlesOnTheLiveFile(t *testing.T) {
	fx := newFixture(t)
	fx.logger.MaxBytes = 100
	mustWrite(t, fx.logger.Path, "")
	held, err := os.OpenFile(fx.logger.Path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Close() }()
	if _, err := fmt.Fprintln(held, strings.Repeat("panic output ", 20)); err != nil {
		t.Fatal(err)
	}

	if err := fx.logger.Rotate(); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintln(held, "after rotation"); err != nil {
		t.Fatal(err)
	}
	if got := readLines(t, fx.logger.Path); len(got) != 1 || got[0] != "after rotation" {
		t.Fatalf("live file = %q, want only the line written after rotation", got)
	}
	backup, err := os.Stat(fx.logger.Path + backupSuffix)
	if err != nil {
		t.Fatal(err)
	}
	if backup.Size() != fx.logger.MaxBytes {
		t.Fatalf("backup is %d bytes, want the last %d", backup.Size(), fx.logger.MaxBytes)
	}
}

func TestRotateEdgeCases(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		maxBytes   int64
		wantBackup bool
	}{
		{name: "missing file", maxBytes: 10},
		{name: "below the limit", content: "short\n", maxBytes: 10},
		{name: "exactly at the limit", content: "0123456789", maxBytes: 10},
		{name: "above the limit", content: "0123456789a", maxBytes: 10, wantBackup: true},
		{name: "rotation disabled", content: "0123456789a", maxBytes: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fx := newFixture(t)
			fx.logger.MaxBytes = tt.maxBytes
			if tt.content != "" {
				mustWrite(t, fx.logger.Path, tt.content)
			}
			if err := fx.logger.Rotate(); err != nil {
				t.Fatal(err)
			}
			_, err := os.Stat(fx.logger.Path + backupSuffix)
			if got := err == nil; got != tt.wantBackup {
				t.Fatalf("backup exists = %v, want %v", got, tt.wantBackup)
			}
		})
	}
}

func TestOversizedMessageIsTruncated(t *testing.T) {
	fx := newFixture(t)
	fx.logger.Printf("%s", strings.Repeat("ä", maxMessageBytes))
	got := readLines(t, fx.logger.Path)
	if len(got) != 1 || !strings.HasSuffix(got[0], "[truncated]") {
		t.Fatalf("message not truncated: %d lines", len(got))
	}
	if len(got[0]) > maxMessageBytes+100 {
		t.Fatalf("line is %d bytes, limit about %d", len(got[0]), maxMessageBytes)
	}
}

func TestPrintfIsSilentWhenTheLogCannotBeWritten(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	mustWrite(t, blocker, "")
	logger := New(filepath.Join(blocker, "ticktack.log"), "hook: ")
	logger.Printf("nowhere to go") // must neither panic nor create anything
	if _, err := os.Stat(logger.Path); err == nil {
		t.Fatal("log file unexpectedly created")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
