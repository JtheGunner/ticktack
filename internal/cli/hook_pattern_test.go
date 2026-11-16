package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestHookFallsBackToDefaultPatternWhenConfigFails(t *testing.T) {
	sink := &fakeSink{}
	deps, logs := newTestDeps(sink, "/r\nfeat/ABC-12-x\n", nil)
	deps.Pattern = func() (*regexp.Regexp, error) { return nil, errors.New("bad config") }
	if err := runHook(context.Background(), deps, "/r"); err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != 2 || sink.events[0].Ticket != "ABC-12" {
		t.Fatalf("events = %+v, want a context event with ABC-12", sink.events)
	}
	if !strings.Contains(logs.String(), "ticket pattern") {
		t.Errorf("log = %q, want the pattern failure recorded", logs.String())
	}
}

func TestConfiguredTicketPattern(t *testing.T) {
	custom := `ticket_pattern = "PRJ_\\d+"`
	invalid := `ticket_pattern = "[x"`
	tests := []struct {
		name    string
		config  *string // nil: no config file
		branch  string
		want    string
		wantErr bool
	}{
		{name: "no config file uses the default", branch: "feat/ABC-1", want: "ABC-1"},
		{name: "custom pattern", config: &custom, branch: "feat/PRJ_4", want: "PRJ_4"},
		{name: "invalid config reports an error", config: &invalid, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", dir)
			if tt.config != nil {
				if err := os.MkdirAll(filepath.Join(dir, "ticktack"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "ticktack", "config.toml"), []byte(*tt.config), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			pattern, err := configuredTicketPattern()
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got := strings.ToUpper(pattern.FindString(tt.branch)); got != tt.want {
				t.Errorf("match = %q, want %q", got, tt.want)
			}
		})
	}
}

// BenchmarkHookPromptCost measures what each shell prompt pays on top of the
// event write: loading the config and detecting the branch with git.
func BenchmarkHookPromptCost(b *testing.B) {
	dir := b.TempDir()
	b.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "ticktack"), 0o700); err != nil {
		b.Fatal(err)
	}
	cfg := "ticket_pattern = \"[A-Z]+-\\\\d+\"\nrepo_paths = [\"~/code\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "ticktack", "config.toml"), []byte(cfg), 0o600); err != nil {
		b.Fatal(err)
	}
	repo := b.TempDir()
	if err := exec.Command("git", "-C", repo, "init", "-q", "-b", "main").Run(); err != nil {
		b.Skip("git unavailable")
	}
	b.Run("config", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := configuredTicketPattern(); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("git", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, _ = gitRepoInfo(context.Background(), repo)
		}
	})
}

func TestMarkerDueThrottlesToOncePerWindow(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "logs", "marker")
	start := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	steps := []struct {
		name string
		at   time.Time
		want bool
	}{
		{"first error is logged", start, true},
		{"again right away is not", start.Add(time.Second), false},
		{"just before the window is not", start.Add(10*time.Minute - time.Second), false},
		{"after the window is logged again", start.Add(10 * time.Minute), true},
		{"and throttled again", start.Add(11 * time.Minute), false},
		{"a clock set back is logged", start.Add(-time.Hour), true},
	}
	for _, s := range steps {
		if got := markerDue(marker, s.at, hookConfigErrorEvery); got != s.want {
			t.Errorf("%s: due = %v, want %v", s.name, got, s.want)
		}
	}
}

func TestHookLogsConfigErrorOnOneLineAndHonoursThrottle(t *testing.T) {
	multiline := errors.New("parse config: line 1\nunexpected \"x\"\nline 3")
	tests := []struct {
		name    string
		due     func() bool
		wantLog bool
	}{
		{"due", func() bool { return true }, true},
		{"throttled", func() bool { return false }, false},
		{"no throttle configured", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sink := &fakeSink{}
			var lines []string
			deps, _ := newTestDeps(sink, "/r\nfeat/ABC-12-x\n", nil)
			deps.Pattern = func() (*regexp.Regexp, error) { return nil, multiline }
			deps.ConfigErrorDue = tt.due
			deps.Logf = func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }
			if err := runHook(context.Background(), deps, "/r"); err != nil {
				t.Fatal(err)
			}
			if len(sink.events) != 2 || sink.events[0].Ticket != "ABC-12" {
				t.Fatalf("events = %+v, want the default pattern fallback", sink.events)
			}
			if (len(lines) == 1) != tt.wantLog || len(lines) > 1 {
				t.Fatalf("log lines = %q, wantLog %v", lines, tt.wantLog)
			}
			if tt.wantLog && strings.Contains(lines[0], "\n") {
				t.Errorf("log entry spans several lines: %q", lines[0])
			}
		})
	}
}
