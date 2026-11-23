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
	"time"

	"github.com/spf13/cobra"

	"github.com/JtheGunner/ticktack/internal/collector"
	gitscan "github.com/JtheGunner/ticktack/internal/collector/git"
	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/pause"
	"github.com/JtheGunner/ticktack/internal/render"
	"github.com/JtheGunner/ticktack/internal/reporoots"
)

const (
	// scanDefaultLookbackDays is how far back a scan without --since reaches.
	// Every scanned event has a deterministic key, so re-reading is harmless;
	// a fixed window also picks up repositories added to repo_paths later.
	scanDefaultLookbackDays = 7
	// scanRepoTimeout bounds the git work for one repository.
	scanRepoTimeout = time.Minute
	// scanFutureSlack bounds the scan and the new-event count at now plus this.
	scanFutureSlack = time.Minute
)

// repoScanner reads context events of one repository within [from, to].
// pattern extracts ticket IDs from branch names; nil means the default.
type repoScanner func(ctx context.Context, repo string, from, to time.Time, pattern *regexp.Regexp) ([]event.Event, error)

// defaultRepoScanner runs the read-only reflog scanner. It reports a missing
// git binary as its own error instead of "not a repository".
func defaultRepoScanner(ctx context.Context, repo string, from, to time.Time, pattern *regexp.Regexp) ([]event.Event, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, errGitMissing
	}
	return gitscan.Scan(ctx, repo, from, to, pattern)
}

var errGitMissing = errors.New(msgRepoGitMissing)

// repoProblem describes why a configured repository path cannot be scanned.
type repoProblem struct {
	Reason string
	// Fix names the remedy; it may contain a %s for the config path.
	Fix string
}

// inspectRepoPath checks that the absolute path is a readable directory that
// holds a .git entry (a directory, or a file for linked worktrees). It
// returns nil when the path can be scanned. It never writes.
func inspectRepoPath(path string) *repoProblem {
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return &repoProblem{Reason: msgRepoMissing, Fix: msgReposFixMissing}
	case err != nil:
		return &repoProblem{Reason: fmt.Sprintf(msgRepoUnreadable, err), Fix: msgReposFixUnread}
	case !info.IsDir():
		return &repoProblem{Reason: msgRepoNotDir, Fix: msgReposFixNotDir}
	}
	dir, err := os.Open(path)
	if err != nil {
		return &repoProblem{Reason: fmt.Sprintf(msgRepoUnreadable, err), Fix: msgReposFixUnread}
	}
	_ = dir.Close()
	if _, err := os.Stat(filepath.Join(path, ".git")); errors.Is(err, os.ErrNotExist) {
		return &repoProblem{Reason: msgRepoNotGit, Fix: msgReposFixNotGit}
	} else if err != nil {
		return &repoProblem{Reason: fmt.Sprintf(msgRepoUnreadable, err), Fix: msgReposFixUnread}
	}
	return nil
}

// scanRepositories makes one pass of the git collector over the configured
// paths and stores the events through sink; see gitscan.Collector.Run. Events
// inside a pause are dropped before storing.
func scanRepositories(ctx context.Context, repos []string, home string, from, to time.Time, pattern *regexp.Regexp, scan repoScanner, paused pause.Intervals, sink collector.Sink) (gitscan.Report, error) {
	var report gitscan.Report
	c := newGitCollector(repos, home, pattern, scan, func(time.Time) (time.Time, time.Time) { return from, to })
	c.LoadPauses = func(time.Time) (pause.Intervals, error) { return paused, nil }
	c.OnReport = func(r gitscan.Report, _ error) { report = r }
	err := c.Run(ctx, sink)
	return report, err
}

// newGitCollector builds the git collector for the configured entries. The
// caller sets Now, LoadPauses and OnReport as it needs them.
func newGitCollector(repos []string, home string, pattern *regexp.Regexp, scan repoScanner, window func(now time.Time) (from, to time.Time)) *gitscan.Collector {
	return &gitscan.Collector{
		Repos: repos,
		Resolve: func(entry string) (string, string) {
			path, err := config.ExpandRepoPath(entry, home)
			if err != nil {
				return "", fmt.Sprintf(msgRepoBadPath, err)
			}
			if problem := inspectRepoPath(path); problem != nil {
				return "", problem.Reason
			}
			return path, ""
		},
		ScanRepo:    scan,
		Describe:    describeScanError,
		Pattern:     pattern,
		Window:      window,
		Now:         func() time.Time { return time.Now().Round(0) },
		RepoTimeout: scanRepoTimeout,
	}
}

func describeScanError(err error, timedOut bool) string {
	switch {
	case timedOut:
		return msgRepoScanTimeout
	case errors.Is(err, errGitMissing):
		return msgRepoGitMissing
	case errors.Is(err, gitscan.ErrNotRepository):
		return msgRepoScanNotRepo
	default:
		return fmt.Sprintf(msgRepoScanFailed, err)
	}
}

// scanStartOfLookback returns local midnight scanDefaultLookbackDays before now.
func scanStartOfLookback(now time.Time, loc *time.Location) time.Time {
	local := now.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day()-scanDefaultLookbackDays, 0, 0, 0, 0, loc)
}

func newScanCommand() *cobra.Command {
	var since string
	cmd := &cobra.Command{
		Use:   "scan",
		Short: msgScanScanShort,
		Long:  msgScanScanLong,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("resolve home directory: %w", err)
			}
			return runScan(cmd.Context(), defaultDailyDeps(cmd), home, defaultRepoScanner, since)
		},
	}
	cmd.Flags().StringVar(&since, "since", "", msgScanSinceFlag)
	return cmd
}

// trackedRepos returns the repositories to scan: the repo_paths entries as
// written, then the repositories discovered below repo_roots, without
// duplicates. Discovered repositories that equal an expanded repo_paths entry
// are dropped.
func trackedRepos(cfg config.Config, home string) []string {
	repos := append([]string(nil), cfg.RepoPaths...)
	seen := make(map[string]bool, len(repos))
	for _, raw := range repos {
		if expanded, err := config.ExpandRepoPath(raw, home); err == nil {
			seen[expanded] = true
		}
	}
	for _, found := range reporoots.Discover(cfg.RepoRoots, home) {
		if !seen[found] {
			repos = append(repos, found)
		}
	}
	return repos
}

// runScan runs the explicit, one-shot scan of the configured repositories. It
// honours the pause flag, never stores events from an earlier pause and is
// idempotent. since is empty for the default lookback.
func runScan(ctx context.Context, deps dailyDeps, home string, scan repoScanner, since string) error {
	cfg, err := deps.LoadConfig()
	if err != nil {
		return err
	}
	repos := trackedRepos(cfg, home)
	if len(repos) == 0 {
		path, pathErr := config.DefaultPath()
		if deps.ConfigPath != nil {
			path, pathErr = deps.ConfigPath()
		}
		if pathErr != nil {
			path = msgScanConfigPath
		}
		return fmt.Errorf(msgScanNoRepoPaths, path)
	}
	paused, err := isPaused(deps)
	if err != nil {
		return err
	}
	if paused {
		_, err = fmt.Fprintln(deps.Out, deps.Render.Gutter(render.StatusWarn, msgScanPaused))
		return err
	}

	now := deps.Now()
	pauseIntervals, err := loadPauses(deps, now)
	if err != nil {
		return err
	}
	from := scanStartOfLookback(now, cfg.Location)
	if since != "" {
		from, err = time.ParseInLocation(dayLayout, since, cfg.Location)
		if err != nil {
			return fmt.Errorf(msgScanBadSince, since)
		}
	}
	to := now.Add(scanFutureSlack)

	st, err := deps.OpenStore()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	before, err := countGitEvents(ctx, st, from, to)
	if err != nil {
		return err
	}
	report, err := scanRepositories(ctx, repos, home, from, to, cfg.TicketPattern, scan, pauseIntervals, st)
	if err != nil {
		return err
	}
	after, err := countGitEvents(ctx, st, from, to)
	if err != nil {
		return err
	}

	if _, err := fmt.Fprintln(deps.Out, formatScanReport(deps.Render, report, from.In(cfg.Location).Format(dayLayout), after-before)); err != nil {
		return err
	}
	if len(report.Skipped) > 0 {
		return fmt.Errorf(msgScanFailed, len(report.Skipped), report.Total)
	}
	return nil
}

// formatScanReport lays out the scan result: a heading, one gutter line per
// repository and the summary. Paths come from the configuration and error
// text may carry external text, so both are sanitised before they are styled.
func formatScanReport(r render.Renderer, report gitscan.Report, since string, newEvents int) string {
	lines := []string{r.Heading(msgScanHeading), ""}
	for _, repo := range report.Repos {
		lines = append(lines, r.Gutter(render.StatusOK, fmt.Sprintf(msgScanRepoDone, SanitizeForTerminal(repo.Entry), repo.Found)))
	}
	for _, s := range report.Skipped {
		lines = append(lines, r.Gutter(render.StatusFail, fmt.Sprintf(msgScanSkipped, SanitizeForTerminal(s.Entry), SanitizeForTerminal(s.Reason))))
	}
	status := render.StatusOK
	if len(report.Skipped) > 0 {
		status = render.StatusWarn
	}
	lines = append(lines, "", r.Gutter(status, fmt.Sprintf(msgScanDone, report.Scanned, report.Total, since, newEvents, report.Found)))
	return strings.Join(lines, "\n")
}

// countGitEvents counts stored events of the git scanner within [from, to).
func countGitEvents(ctx context.Context, st dailyStore, from, to time.Time) (int, error) {
	events, err := st.Events(ctx, from, to)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range events {
		if e.Source == gitscan.Source {
			n++
		}
	}
	return n, nil
}
