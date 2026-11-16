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
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
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
	checkoutLine = regexp.MustCompile(`^(?:checkout|switch): moving from (.+) to (\S+)$`)
	rebaseLine   = regexp.MustCompile(`^rebase(?: -i)? \((\w+)\)`)
	returningTo  = regexp.MustCompile(`returning to refs/heads/(.+)$`)
	commitLine   = regexp.MustCompile(`^commit(?: \([^)]*\))?:`)
)

// buildEvents walks entries (oldest first) and emits one context event per
// checkout, per commit and per finished rebase. Rebase replays are collapsed:
// only the final branch state at the end of the rebase is reported. Reset,
// pull and merge lines never change the branch. currentBranch seeds the
// branch for commits that precede the first checkout when no checkout exists.
func buildEvents(repo string, entries []reflogEntry, currentBranch string, isBranch func(string) bool, pattern *regexp.Regexp) []event.Event {
	branch := currentBranch
	for _, e := range entries {
		if m := checkoutLine.FindStringSubmatch(e.Subject); m != nil {
			branch = branchOrEmpty(m[1], isBranch)
			break
		}
	}

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

	inRebase := false
	for _, e := range entries {
		if m := rebaseLine.FindStringSubmatch(e.Subject); m != nil {
			switch m[1] {
			case "start":
				inRebase = true
			case "finish", "abort":
				inRebase = false
				if r := returningTo.FindStringSubmatch(e.Subject); r != nil {
					branch = r[1]
					emit(e, "rebase", branch)
				}
			}
			continue
		}
		if m := checkoutLine.FindStringSubmatch(e.Subject); m != nil {
			if inRebase {
				continue
			}
			branch = branchOrEmpty(m[2], isBranch)
			emit(e, "checkout", branch)
			continue
		}
		if commitLine.MatchString(e.Subject) {
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

func branchOrEmpty(name string, isBranch func(string) bool) string {
	if isBranch(name) {
		return name
	}
	return ""
}
