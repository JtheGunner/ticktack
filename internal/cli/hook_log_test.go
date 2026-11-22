package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/JtheGunner/ticktack/internal/logfile"
	"github.com/JtheGunner/ticktack/internal/paths"
)

// The hook runs at every prompt: a persistent error must not add a line per
// prompt.
func TestFileLogfThrottlesRepeatedMessages(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	for i := 0; i < 5; i++ {
		fileLogf("open store: %v", "database is locked")
	}
	fileLogf("append %s event: %v", "context", "disk full")

	logPath, err := paths.LogPath()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %q", len(lines), lines)
	}
	if !strings.HasPrefix(lines[0], "hook: ") || !strings.HasSuffix(lines[0], "open store: database is locked") {
		t.Fatalf("unexpected first line %q", lines[0])
	}
}

func TestFileLogfRotatesOversizedLog(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	logPath, err := paths.LogPath()
	if err != nil {
		t.Fatal(err)
	}
	fileLogf("first") // creates the state directory
	oversized := strings.Repeat("x", int(logfile.DefaultMaxBytes)+1)
	if err := os.WriteFile(logPath, []byte(oversized), 0o600); err != nil {
		t.Fatal(err)
	}

	fileLogf("second")

	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > 200 {
		t.Fatalf("log file is %d bytes after rotation", info.Size())
	}
	backup, err := os.Stat(logPath + ".1")
	if err != nil {
		t.Fatalf("rotated backup missing: %v", err)
	}
	if backup.Size() > logfile.DefaultMaxBytes {
		t.Fatalf("backup is %d bytes, limit %d", backup.Size(), logfile.DefaultMaxBytes)
	}
}
