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

	gitscan "github.com/JtheGunner/ticktack/internal/collector/git"
	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/render"
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

// eventAppender is the part of the store a scan writes to.
type eventAppender interface {
	Append(ctx context.Context, e event.Event) error
}

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

// skippedRepo is a configured path that was not scanned, with a reason.
type skippedRepo struct {
	Path   string
	Reason string
}

// scannedRepo is a configured path that was scanned, with the number of
// events found in it.
type scannedRepo struct {
	Path  string
	Found int
}

// scanReport is the outcome of one pass over the configured repositories.
type scanReport struct {
	Total   int
	Scanned int
	Found   int
	// Repos lists the scanned repositories in configuration order.
	Repos   []scannedRepo
	Skipped []skippedRepo
}

// scanRepositories scans every configured path and appends the events. A
// failing repository is recorded and does not stop the others. Only a failed
// append or a cancelled ctx aborts the pass. Appending is idempotent because
// every event carries a deterministic key.
func scanRepositories(ctx context.Context, repos []string, home string, from, to time.Time, pattern *regexp.Regexp, scan repoScanner, sink eventAppender) (scanReport, error) {
	report := scanReport{Total: len(repos)}
	for _, raw := range repos {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		path, err := config.ExpandRepoPath(raw, home)
		if err != nil {
			report.Skipped = append(report.Skipped, skippedRepo{raw, fmt.Sprintf(msgRepoBadPath, err)})
			continue
		}
		if problem := inspectRepoPath(path); problem != nil {
			report.Skipped = append(report.Skipped, skippedRepo{raw, problem.Reason})
			continue
		}
		repoCtx, cancel := context.WithTimeout(ctx, scanRepoTimeout)
		events, err := scan(repoCtx, path, from, to, pattern)
		timedOut := repoCtx.Err() != nil
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return report, ctx.Err()
			}
			report.Skipped = append(report.Skipped, skippedRepo{raw, describeScanError(err, timedOut)})
			continue
		}
		for _, e := range events {
			if err := sink.Append(ctx, e); err != nil {
				return report, err
			}
		}
		report.Scanned++
		report.Found += len(events)
		report.Repos = append(report.Repos, scannedRepo{Path: raw, Found: len(events)})
	}
	return report, nil
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
		Short: "Capture repository and branch context from the git reflog of the configured repositories",
		Long: "Reads the HEAD reflog of every repository listed in repo_paths and stores context events " +
			"(repository, branch, ticket hint from the branch name, timestamp). Commit messages, file names " +
			"and file contents are never read into events. Scanning again is harmless: known events are skipped.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("resolve home directory: %w", err)
			}
			return runScan(cmd.Context(), defaultDailyDeps(cmd), home, defaultRepoScanner, since)
		},
	}
	cmd.Flags().StringVar(&since, "since", "", "scan from this day as YYYY-MM-DD (default: the start of the day 7 days ago)")
	return cmd
}

// runScan runs the explicit, one-shot scan of the configured repositories. It
// honours the pause flag and is idempotent. since is empty for the default
// lookback.
func runScan(ctx context.Context, deps dailyDeps, home string, scan repoScanner, since string) error {
	cfg, err := deps.LoadConfig()
	if err != nil {
		return err
	}
	if len(cfg.RepoPaths) == 0 {
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
	report, err := scanRepositories(ctx, cfg.RepoPaths, home, from, to, cfg.TicketPattern, scan, st)
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
func formatScanReport(r render.Renderer, report scanReport, since string, newEvents int) string {
	lines := []string{r.Heading(msgScanHeading), ""}
	for _, repo := range report.Repos {
		lines = append(lines, r.Gutter(render.StatusOK, fmt.Sprintf(msgScanRepoDone, SanitizeForTerminal(repo.Path), repo.Found)))
	}
	for _, s := range report.Skipped {
		lines = append(lines, r.Gutter(render.StatusFail, fmt.Sprintf(msgScanSkipped, SanitizeForTerminal(s.Path), SanitizeForTerminal(s.Reason))))
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
