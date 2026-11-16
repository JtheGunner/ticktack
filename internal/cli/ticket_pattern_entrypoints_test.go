package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/collector/git"
	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/resolver"
	"github.com/JtheGunner/ticktack/internal/review"
)

// TestTicketPatternIsAppliedIdenticallyByEveryEntryPoint pins the one rule:
// the configured ticket_pattern decides, case-sensitively, in scan, hook,
// resolver and review.
func TestTicketPatternIsAppliedIdenticallyByEveryEntryPoint(t *testing.T) {
	def := regexp.MustCompile(config.DefaultTicketPattern)
	custom := regexp.MustCompile(`PRJ_\d+`)
	tests := []struct {
		name    string
		branch  string
		pattern *regexp.Regexp
		want    string
	}{
		{"lower case key is no ticket", "feat/abc-12", def, ""},
		{"upper case key is a ticket", "feat/ABC-12-x", def, "ABC-12"},
		{"lower case utf-8 is no ticket", "fix/utf-8-handling", def, ""},
		{"nil pattern behaves like the default", "feat/ABC-12-x", nil, "ABC-12"},
		{"custom pattern is honoured", "feat/PRJ_7-x", custom, "PRJ_7"},
		{"custom pattern ignores the default shape", "feat/ABC-12-x", custom, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := scanTicketFor(t, tt.branch, tt.pattern); got != tt.want {
				t.Errorf("scan ticket = %q, want %q", got, tt.want)
			}
			if got := hookTicketFor(t, tt.branch, tt.pattern); got != tt.want {
				t.Errorf("hook ticket = %q, want %q", got, tt.want)
			}
			got := resolver.Resolve(resolver.ContextSignal{Repo: "/r", Branch: tt.branch}, resolver.Rules{TicketPattern: tt.pattern}).Ticket
			if got != tt.want {
				t.Errorf("resolver ticket = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReviewNormalizeTicketUsesTheConfiguredPattern(t *testing.T) {
	tests := []struct {
		name    string
		pattern *regexp.Regexp
		ticket  string
		wantErr bool
	}{
		{"default accepts a key", regexp.MustCompile(config.DefaultTicketPattern), "ABC-12", false},
		{"default rejects a non-key", regexp.MustCompile(config.DefaultTicketPattern), "nokey", true},
		{"custom accepts its shape", regexp.MustCompile(`PRJ_\d+`), "PRJ_7", false},
		{"custom rejects the default shape", regexp.MustCompile(`PRJ_\d+`), "ABC-12", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := review.NormalizeTicket(config.Config{TicketPattern: tt.pattern}, tt.ticket)
			if (err != nil) != tt.wantErr {
				t.Errorf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func hookTicketFor(t *testing.T, branch string, pattern *regexp.Regexp) string {
	t.Helper()
	sink := &fakeSink{}
	deps, _ := newTestDeps(sink, "/work/repo\n"+branch+"\n", nil)
	deps.Pattern = func() (*regexp.Regexp, error) { return pattern, nil }
	if err := runHook(context.Background(), deps, "/work/repo"); err != nil {
		t.Fatal(err)
	}
	for _, e := range sink.events {
		if e.Kind == event.KindContext {
			return e.Ticket
		}
	}
	t.Fatal("hook wrote no context event")
	return ""
}

func scanTicketFor(t *testing.T, branch string, pattern *regexp.Regexp) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "repo")
	day := func(d int) time.Time { return time.Date(2026, 2, d, 9, 0, 0, 0, time.UTC) }
	newBranchRepo(t, dir, branch, day)
	events, err := git.Scan(context.Background(), dir, day(1), day(28), pattern)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Branch == branch {
			return e.Ticket
		}
	}
	t.Fatalf("scan found no event on branch %q: %+v", branch, events)
	return ""
}

// newBranchRepo creates a repository at dir with one commit on main and a
// checkout of branch, both inside the scan window.
func newBranchRepo(t *testing.T, dir, branch string, day func(int) time.Time) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed; skipping scan tests that need a real repository")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, day(10), "init", "-q", "-b", "main")
	gitCommit(t, dir, day(10), "a.txt")
	gitIn(t, dir, day(11), "checkout", "-q", "-b", branch)
}
