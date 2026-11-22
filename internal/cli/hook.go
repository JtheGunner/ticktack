package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	gitscan "github.com/JtheGunner/ticktack/internal/collector/git"
	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/logfile"
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
	// AppendBatch stores all events in one transaction.
	AppendBatch(ctx context.Context, events []event.Event) error
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
	// Pattern returns the configured ticket pattern. A nil pattern or an error
	// falls back to the default pattern; the hook never fails on it.
	Pattern func() (*regexp.Regexp, error)
	// ConfigErrorDue reports whether a config error may be logged now; nil
	// means always. It keeps a broken config from adding a log line per prompt.
	ConfigErrorDue func() bool
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
			return pauseFlagCheck(p)()
		},
		Pattern:        configuredTicketPattern,
		ConfigErrorDue: configErrorLogDue,
		Logf:           fileLogf,
	}
}

// configuredTicketPattern reads only the ticket pattern from the config file.
// The file is read and parsed per prompt without touching the database; a
// missing file yields the default pattern.
func configuredTicketPattern() (*regexp.Regexp, error) {
	path, err := config.DefaultPath()
	if err != nil {
		return nil, err
	}
	cfg, err := config.LoadOrDefault(path)
	if err != nil {
		return nil, err
	}
	return cfg.TicketPattern, nil
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
	// Without the inherited GIT_DIR and friends: a prompt inside a hook, an
	// alias or a `git rebase --exec` shell would otherwise report the outer
	// repository instead of the one in cwd.
	cmd.Env = gitscan.CommandEnv()
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// hookConfigErrorEvery is the shortest time between two logged config errors.
const hookConfigErrorEvery = 10 * time.Minute

// configErrorLogDue throttles the config error line using the modification
// time of a marker file next to the log file.
func configErrorLogDue() bool {
	logPath, err := paths.LogPath()
	if err != nil {
		return false
	}
	return markerDue(filepath.Join(filepath.Dir(logPath), "hook-config-error.marker"), time.Now(), hookConfigErrorEvery)
}

// markerDue reports whether more than every has passed since the marker file
// was last touched, and touches it when it has. A missing marker is due. A
// marker from the future (clock change) is due. When the marker cannot be
// written the answer is still true: the log write itself then fails quietly.
func markerDue(marker string, now time.Time, every time.Duration) bool {
	if info, err := os.Stat(marker); err == nil {
		if age := now.Sub(info.ModTime()); age >= 0 && age < every {
			return false
		}
	}
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		return true
	}
	f, err := os.OpenFile(marker, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return true
	}
	_ = f.Close()
	_ = os.Chtimes(marker, now, now)
	return true
}

// fileLogf appends to the size-limited log file and stays silent when it
// cannot. A message repeated at every prompt is throttled across the hook
// processes.
func fileLogf(format string, args ...any) {
	logPath, err := paths.LogPath()
	if err != nil {
		return
	}
	logfile.New(logPath, "hook: ").Printf(format, args...)
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

	pattern, err := deps.Pattern()
	if err != nil {
		// Fall back to the default pattern; the prompt must keep working.
		if deps.ConfigErrorDue == nil || deps.ConfigErrorDue() {
			// %q keeps a multi-line TOML or regex error on one log line.
			deps.Logf("load ticket pattern: %q", err.Error())
		}
		pattern = nil
	}

	now := deps.Now()
	events := []event.Event{
		{At: now, Source: hookSource, Kind: event.KindContext, Repo: root, Branch: branch, Ticket: ticketid.Extract(branch, pattern)},
		{At: now, Source: hookSource, Kind: event.KindPresence},
	}
	// One transaction: the context and the presence event are stored together
	// or not at all.
	if err := sink.AppendBatch(ctx, events); err != nil {
		deps.Logf("append events: %v", err)
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
