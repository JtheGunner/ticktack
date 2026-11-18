// Package git scans the reflog of a repository and turns branch checkouts and
// commits into context events. It is strictly read-only: it never installs
// hooks, never writes to the repository and never reads commit messages,
// file names or file contents into events.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/ticketid"
)

// Source is the event source name used for every event emitted here.
const Source = "git"

const fieldSep = "\x1f"

// ErrNotRepository is returned when the scanned path is not a git repository.
var ErrNotRepository = errors.New("not a git repository")

// Scan reads the HEAD reflog of repo and returns the context events whose
// timestamp lies within [from, to]. A zero from or to leaves that side
// unbounded. repo may be a regular checkout or a linked worktree; the events
// carry the absolute worktree root. Repositories without a reflog (fresh,
// bare, or with logs disabled) yield no events and no error.
//
// pattern extracts the ticket ID from branch names; nil means the default
// pattern. The pattern is not part of the key, so events keep their identity
// across pattern changes.
//
// Events carry a deterministic Key, so appending the result of repeated scans
// to the store is harmless. The result is ordered by time, then key.
func Scan(ctx context.Context, repo string, from, to time.Time, pattern *regexp.Regexp) ([]event.Event, error) {
	bare, err := gitOutput(ctx, repo, "rev-parse", "--is-bare-repository")
	if err != nil {
		return nil, fmt.Errorf("scan %s: %w", repo, ErrNotRepository)
	}
	if bare == "true" {
		return nil, nil
	}
	root, err := gitOutput(ctx, repo, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("scan %s: resolve root: %w", repo, err)
	}

	hasLog, err := hasReflog(ctx, root)
	if err != nil || !hasLog {
		return nil, err
	}

	raw, err := gitOutput(ctx, root, "reflog", "show", "--date=unix",
		"--format=%gd"+"%x1f"+"%H"+"%x1f"+"%gs", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("scan %s: read reflog: %w", root, err)
	}
	entries := parseReflog(raw)

	current, err := currentBranch(ctx, root)
	if err != nil {
		return nil, err
	}
	known, err := branchSet(ctx, root)
	if err != nil {
		return nil, err
	}

	var events []event.Event
	for _, e := range buildEvents(root, entries, current, known, pattern) {
		if !from.IsZero() && e.At.Before(from) {
			continue
		}
		if !to.IsZero() && e.At.After(to) {
			continue
		}
		events = append(events, e)
	}
	return events, nil
}

// hasReflog reports whether the worktree's own HEAD reflog exists and is not empty.
func hasReflog(ctx context.Context, root string) (bool, error) {
	path, err := gitOutput(ctx, root, "rev-parse", "--git-path", "logs/HEAD")
	if err != nil {
		return false, fmt.Errorf("scan %s: locate reflog: %w", root, err)
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("scan %s: stat reflog: %w", root, err)
	}
	return info.Size() > 0, nil
}

// currentBranch returns the branch HEAD points to, or "" when detached.
func currentBranch(ctx context.Context, root string) (string, error) {
	out, err := gitOutput(ctx, root, "symbolic-ref", "--short", "-q", "HEAD")
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("scan %s: read HEAD: %w", root, err)
	}
	return out, nil
}

// branchSet returns a predicate that tells whether a checkout target names a
// local branch rather than a commit, tag or remote-tracking ref.
func branchSet(ctx context.Context, root string) (func(string) bool, error) {
	out, err := gitOutput(ctx, root, "for-each-ref", "--format=%(refname)",
		"refs/heads", "refs/tags", "refs/remotes")
	if err != nil {
		return nil, fmt.Errorf("scan %s: list refs: %w", root, err)
	}
	heads := map[string]bool{}
	other := map[string]bool{}
	for _, ref := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(ref, "refs/heads/"):
			heads[strings.TrimPrefix(ref, "refs/heads/")] = true
		case strings.HasPrefix(ref, "refs/tags/"):
			other[strings.TrimPrefix(ref, "refs/tags/")] = true
		case strings.HasPrefix(ref, "refs/remotes/"):
			other[strings.TrimPrefix(ref, "refs/remotes/")] = true
		}
	}
	return func(name string) bool {
		if heads[name] {
			return true
		}
		return name != "" && !strings.HasPrefix(name, "HEAD") && !other[name] && !looksLikeCommit(name)
	}, nil
}

var commitLike = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

func looksLikeCommit(name string) bool { return commitLike.MatchString(name) }

// gitOutput runs git read-only inside dir and returns trimmed stdout.
func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = CommandEnv()
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

// reflogEntry is one HEAD reflog line. It never holds commit messages: the
// subject is the reflog action text such as "checkout: moving from a to b".
type reflogEntry struct {
	At      time.Time
	Hash    string
	Subject string
}

// parseReflog parses git output (newest first) into entries ordered oldest first.
func parseReflog(raw string) []reflogEntry {
	var entries []reflogEntry
	for _, line := range strings.Split(raw, "\n") {
		if e, ok := parseReflogLine(line); ok {
			entries = append(entries, e)
		}
	}
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	return entries
}

// parseReflogLine parses "HEAD@{<unix>}<sep><hash><sep><subject>".
func parseReflogLine(line string) (reflogEntry, bool) {
	parts := strings.SplitN(line, fieldSep, 3)
	if len(parts) != 3 {
		return reflogEntry{}, false
	}
	selector := parts[0]
	open := strings.Index(selector, "@{")
	if open < 0 || !strings.HasSuffix(selector, "}") {
		return reflogEntry{}, false
	}
	sec, err := strconv.ParseInt(selector[open+2:len(selector)-1], 10, 64)
	if err != nil {
		return reflogEntry{}, false
	}
	return reflogEntry{At: time.Unix(sec, 0).UTC(), Hash: parts[1], Subject: parts[2]}, true
}

var (
	// checkoutLine captures the target up to the end of the line: git records
	// it as typed, so a revision expression such as "main@{1 day ago}" or
	// ":/some text" holds spaces. The origin never does; it is a branch name
	// or a full commit hash.
	checkoutLine = regexp.MustCompile(`^(?:checkout|switch): moving from (\S+) to (.+)$`)
	// rebaseLine matches every line a rebase writes, in the current
	// ("rebase (pick): …", "rebase -i (finish): …") and the legacy
	// ("rebase: …", "rebase finished: …") wording.
	rebaseLine = regexp.MustCompile(`^rebase(?: -[a-z])?(?: \(\w+\)| finished)?:`)
	// returningTo matches only the closing line of a rebase. Replay lines
	// ("rebase (pick): <subject>") carry a commit subject, which may read
	// "returning to refs/heads/…" and must never move the branch.
	returningTo = regexp.MustCompile(`^rebase(?:(?: -[a-z])? \((?:finish|abort)\)| finished): returning to refs/heads/(\S+)$`)
	renameLine  = regexp.MustCompile(`^Branch: renamed refs/heads/(\S+) to refs/heads/(\S+)$`)
	commitLine  = regexp.MustCompile(`^commit(?: \([^)]*\))?:`)
)

// buildEvents walks entries (oldest first) and emits one context event per
// checkout, per commit, per rename of the checked-out branch and per rebase
// that returned to its branch. Rebase replays are collapsed: only the branch
// state on the closing (finish or abort) line of the rebase is reported. Reset, pull and merge lines
// never change the branch.
//
// Every line is judged on its own text. There is deliberately no "inside a
// rebase" state: `git rebase --quit` and a rebase that is still stopped write
// no closing line, and such a state would swallow every later checkout.
//
// currentBranch seeds the branch for commits that precede the first checkout
// or rename when the reflog holds neither.
func buildEvents(repo string, entries []reflogEntry, currentBranch string, isBranch func(string) bool, pattern *regexp.Regexp) []event.Event {
	branch := initialBranch(entries, currentBranch, isBranch)

	var events []event.Event
	seen := map[string]bool{}
	emit := func(e reflogEntry, kind, branchName string) {
		key := fmt.Sprintf("git:%s:%s:%d:%s:%s", repo, kind, e.At.Unix(), e.Hash, branchName)
		if seen[key] {
			return
		}
		seen[key] = true
		events = append(events, event.Event{
			At:     e.At,
			Source: Source,
			Kind:   event.KindContext,
			Repo:   repo,
			Branch: branchName,
			Ticket: ticketid.Extract(branchName, pattern),
			Key:    key,
		})
	}

	for _, e := range entries {
		switch {
		case rebaseLine.MatchString(e.Subject):
			if r := returningTo.FindStringSubmatch(e.Subject); r != nil {
				branch = r[1]
				emit(e, "rebase", branch)
			}
		case renameLine.MatchString(e.Subject):
			branch = renameLine.FindStringSubmatch(e.Subject)[2]
			emit(e, "rename", branch)
		case checkoutLine.MatchString(e.Subject):
			branch = branchOrEmpty(checkoutLine.FindStringSubmatch(e.Subject)[2], isBranch)
			emit(e, "checkout", branch)
		case commitLine.MatchString(e.Subject):
			emit(e, "commit", branch)
		}
	}

	sort.SliceStable(events, func(i, j int) bool {
		if !events[i].At.Equal(events[j].At) {
			return events[i].At.Before(events[j].At)
		}
		return events[i].Key < events[j].Key
	})
	return events
}

// initialBranch returns the branch HEAD was on before the first reflog line
// that names one: the origin of the first checkout or the old name of the
// first rename. Without such a line HEAD never left currentBranch.
func initialBranch(entries []reflogEntry, currentBranch string, isBranch func(string) bool) string {
	for _, e := range entries {
		if m := renameLine.FindStringSubmatch(e.Subject); m != nil {
			return m[1]
		}
		if m := checkoutLine.FindStringSubmatch(e.Subject); m != nil {
			return branchOrEmpty(m[1], isBranch)
		}
	}
	return currentBranch
}

// branchOrEmpty returns name when a checkout of it lands on a local branch and
// "" when it detaches HEAD.
func branchOrEmpty(name string, isBranch func(string) bool) string {
	if isRefExpression(name) || !isBranch(name) {
		return ""
	}
	return name
}

// isRefExpression reports whether a checkout target is a revision expression
// such as "main~1", "main^", "main@{1}", "main@{1 day ago}", "v1^{commit}" or
// "@" rather than a plain ref name. Git forbids these characters and sequences
// (whitespace included) in branch names, and checking out such an expression
// always detaches HEAD.
func isRefExpression(name string) bool {
	return name == "@" ||
		strings.ContainsAny(name, " \t~^:?*[\\") ||
		strings.Contains(name, "@{") ||
		strings.Contains(name, "..")
}
