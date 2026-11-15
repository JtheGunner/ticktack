package cli

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/paths"
	"github.com/JtheGunner/ticktack/internal/store"
	"github.com/JtheGunner/ticktack/internal/ticketid"
)

const (
	hookSource     = "shell"
	hookGitTimeout = 500 * time.Millisecond
)

// eventSink is the part of the store the hook needs.
type eventSink interface {
	Append(ctx context.Context, e event.Event) error
	Close() error
}

// hookDeps holds everything runHook touches outside of pure logic.
type hookDeps struct {
	// OpenStore opens the event store.
	OpenStore func() (eventSink, error)
	// Git returns the output of `git rev-parse --show-toplevel --abbrev-ref HEAD`
	// run in the given directory.
	Git func(ctx context.Context, cwd string) (string, error)
	// Now returns the current time.
	Now func() time.Time
	// Paused reports whether tracking is paused.
	Paused func() bool
	// Logf records an error. It must never write to stdout or stderr.
	Logf func(format string, args ...any)
}

// newHookCommand builds the hidden `ticktack hook` command called by the shell
// prompt hooks. It never prints anything and always exits 0 so it cannot
// disturb the prompt; errors go to the log file.
func newHookCommand() *cobra.Command {
	var cwd string
	cmd := &cobra.Command{
		Use:    "hook",
		Short:  "Report the working directory from a shell prompt (internal)",
		Hidden: true,
		Args:   cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_ = runHook(cmd.Context(), defaultHookDeps(), cwd)
			return nil
		},
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	cmd.Flags().StringVar(&cwd, "cwd", "", "working directory of the shell")
	return cmd
}

func defaultHookDeps() hookDeps {
	return hookDeps{
		OpenStore: openHookStore,
		Git:       gitRepoInfo,
		// Round(0) strips the monotonic reading so stored times are wall-clock.
		Now: func() time.Time { return time.Now().Round(0) },
		Paused: func() bool {
			p, err := paths.PausePath()
			if err != nil {
				return false
			}
			_, err = os.Stat(p)
			return err == nil
		},
		Logf: fileLogf,
	}
}

func openHookStore() (eventSink, error) {
	dbPath, err := paths.DBPath()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	return store.Open(dbPath)
}

// gitRepoInfo resolves repo root and branch with a single git call: no shell,
// a short timeout and no optional locks so it never blocks other git work.
func gitRepoInfo(ctx context.Context, cwd string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, hookGitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", cwd, "rev-parse", "--show-toplevel", "--abbrev-ref", "HEAD")
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// fileLogf appends to the log file and stays silent when it cannot.
func fileLogf(format string, args ...any) {
	logPath, err := paths.LogPath()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	log.New(f, "hook: ", log.LstdFlags).Printf(format, args...)
}

// runHook records a context and a presence event for the repository that
// contains cwd. Outside a git repository, while paused, or on any error it
// writes no event and returns nil; errors are only logged.
func runHook(ctx context.Context, deps hookDeps, cwd string) error {
	if cwd == "" || deps.Paused() {
		return nil
	}
	out, err := deps.Git(ctx, cwd)
	if err != nil {
		// Not a repository (or a bare one): nothing to report.
		return nil
	}
	root, branch, ok := parseGitInfo(out)
	if !ok {
		return nil
	}

	sink, err := deps.OpenStore()
	if err != nil {
		deps.Logf("open store: %v", err)
		return nil
	}
	defer func() {
		if err := sink.Close(); err != nil {
			deps.Logf("close store: %v", err)
		}
	}()

	now := deps.Now()
	events := []event.Event{
		{At: now, Source: hookSource, Kind: event.KindContext, Repo: root, Branch: branch, Ticket: ticketid.Extract(branch, nil)},
		{At: now, Source: hookSource, Kind: event.KindPresence},
	}
	for _, e := range events {
		if err := sink.Append(ctx, e); err != nil {
			deps.Logf("append %s event: %v", e.Kind, err)
			return nil
		}
	}
	return nil
}

// parseGitInfo splits the output into repo root and branch. A detached HEAD
// ("HEAD") yields an empty branch.
func parseGitInfo(out string) (root, branch string, ok bool) {
	lines := strings.Split(strings.TrimRight(out, "\r\n"), "\n")
	if len(lines) != 2 || lines[0] == "" {
		return "", "", false
	}
	root, branch = lines[0], lines[1]
	if branch == "HEAD" {
		branch = ""
	}
	return root, branch, true
}
