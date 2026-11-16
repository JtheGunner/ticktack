package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"
)

// isolateGit keeps the user's git configuration and hooks out of the tests.
func isolateGit(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "Test User")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Test User")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")
}

func at(day, hour int) time.Time {
	return time.Date(2024, 3, day, hour, 0, 0, 0, time.UTC)
}

// run executes git in dir with the given fixed date for author and committer.
func run(t *testing.T, dir string, when time.Time, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	stamp := when.Format(time.RFC3339)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func commit(t *testing.T, dir string, when time.Time, file string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	run(t, dir, when, "add", file)
	run(t, dir, when, "commit", "-q", "-m", "add "+file)
}

func newRepo(t *testing.T) string {
	t.Helper()
	isolateGit(t)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run(t, dir, at(1, 9), "init", "-q", "-b", "main")
	return dir
}

func scan(t *testing.T, repo string) []event.Event {
	t.Helper()
	events, err := Scan(context.Background(), repo, time.Time{}, time.Time{}, nil)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return events
}

type want struct {
	at     time.Time
	branch string
	ticket string
}

func TestScanEmitsCheckoutAndCommitEvents(t *testing.T) {
	repo := newRepo(t)
	commit(t, repo, at(1, 9), "a.txt")
	run(t, repo, at(2, 9), "checkout", "-q", "-b", "feature/ABC-12-login")
	commit(t, repo, at(2, 10), "b.txt")
	run(t, repo, at(3, 9), "checkout", "-q", "main")

	events := scan(t, repo)

	wants := []want{
		{at(1, 9), "main", ""},
		{at(2, 9), "feature/ABC-12-login", "ABC-12"},
		{at(2, 10), "feature/ABC-12-login", "ABC-12"},
		{at(3, 9), "main", ""},
	}
	if len(events) != len(wants) {
		t.Fatalf("got %d events, want %d: %+v", len(events), len(wants), events)
	}
	for i, w := range wants {
		e := events[i]
		if !e.At.Equal(w.at) || e.Branch != w.branch || e.Ticket != w.ticket {
			t.Errorf("event %d = {%v %q %q}, want {%v %q %q}", i, e.At, e.Branch, e.Ticket, w.at, w.branch, w.ticket)
		}
		if e.Source != "git" || e.Kind != event.KindContext || e.Repo != repo || e.Key == "" {
			t.Errorf("event %d has wrong shape: %+v", i, e)
		}
	}
}

func TestScanRangeFiltersEvents(t *testing.T) {
	repo := newRepo(t)
	commit(t, repo, at(1, 9), "a.txt")
	commit(t, repo, at(2, 9), "b.txt")
	commit(t, repo, at(3, 9), "c.txt")

	events, err := Scan(context.Background(), repo, at(2, 0), at(2, 23), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || !events[0].At.Equal(at(2, 9)) {
		t.Fatalf("got %+v, want only the day-2 commit", events)
	}
}

func TestScanIsIdempotent(t *testing.T) {
	repo := newRepo(t)
	commit(t, repo, at(1, 9), "a.txt")
	run(t, repo, at(2, 9), "checkout", "-q", "-b", "feature/ABC-1")
	commit(t, repo, at(2, 10), "b.txt")

	first := scan(t, repo)
	second := scan(t, repo)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("rescan differs:\n%+v\n%+v", first, second)
	}
	seen := map[string]bool{}
	for _, e := range first {
		if seen[e.Key] {
			t.Errorf("duplicate key %q", e.Key)
		}
		seen[e.Key] = true
	}
}

func TestScanDetachedHeadHasNoTicket(t *testing.T) {
	repo := newRepo(t)
	commit(t, repo, at(1, 9), "a.txt")
	run(t, repo, at(2, 9), "checkout", "-q", "-b", "feature/ABC-5")
	commit(t, repo, at(2, 10), "b.txt")
	sha := run(t, repo, at(3, 9), "rev-parse", "--short=10", "main")
	run(t, repo, at(3, 9), "checkout", "-q", sha)

	events := scan(t, repo)
	last := events[len(events)-1]
	if !last.At.Equal(at(3, 9)) {
		t.Fatalf("last event at %v, want the detach checkout", last.At)
	}
	if last.Branch != "" || last.Ticket != "" {
		t.Errorf("detached event = %q/%q, want empty branch and ticket", last.Branch, last.Ticket)
	}
}

func TestScanRebaseDoesNotEmitPerCommitSwitches(t *testing.T) {
	repo := newRepo(t)
	commit(t, repo, at(1, 9), "a.txt")
	run(t, repo, at(2, 9), "checkout", "-q", "-b", "feature/ABC-7")
	commit(t, repo, at(2, 10), "f1.txt")
	commit(t, repo, at(2, 11), "f2.txt")
	commit(t, repo, at(2, 12), "f3.txt")
	run(t, repo, at(3, 9), "checkout", "-q", "main")
	commit(t, repo, at(3, 10), "m.txt")
	run(t, repo, at(4, 9), "checkout", "-q", "feature/ABC-7")
	run(t, repo, at(5, 9), "rebase", "-q", "main")

	var onRebaseDay []event.Event
	for _, e := range scan(t, repo) {
		if e.At.Equal(at(5, 9)) {
			onRebaseDay = append(onRebaseDay, e)
		}
	}
	if len(onRebaseDay) != 1 {
		t.Fatalf("rebase produced %d events, want 1: %+v", len(onRebaseDay), onRebaseDay)
	}
	if got := onRebaseDay[0]; got.Branch != "feature/ABC-7" || got.Ticket != "ABC-7" {
		t.Errorf("rebase event = %q/%q, want feature/ABC-7 and ABC-7", got.Branch, got.Ticket)
	}
}

func TestScanHandlesWorktrees(t *testing.T) {
	repo := newRepo(t)
	commit(t, repo, at(1, 9), "a.txt")
	wt, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wt = filepath.Join(wt, "wt")
	run(t, repo, at(2, 9), "worktree", "add", "-q", "-b", "feature/ABC-9", wt)
	commit(t, wt, at(2, 10), "w.txt")

	events := scan(t, wt)
	if len(events) == 0 {
		t.Fatal("no events for linked worktree")
	}
	last := events[len(events)-1]
	if last.Repo != wt {
		t.Errorf("Repo = %q, want worktree root %q", last.Repo, wt)
	}
	if !last.At.Equal(at(2, 10)) || last.Branch != "feature/ABC-9" || last.Ticket != "ABC-9" {
		t.Errorf("last event = %+v", last)
	}
	for _, e := range events {
		if e.Branch == "main" {
			t.Errorf("worktree scan leaked the main checkout's events: %+v", e)
		}
	}
}

func TestScanIgnoresRepoWithoutReflog(t *testing.T) {
	t.Run("fresh repo", func(t *testing.T) {
		repo := newRepo(t)
		if got := scan(t, repo); len(got) != 0 {
			t.Errorf("got %+v, want none", got)
		}
	})
	t.Run("logs disabled", func(t *testing.T) {
		repo := newRepo(t)
		run(t, repo, at(1, 9), "config", "core.logAllRefUpdates", "false")
		commit(t, repo, at(1, 9), "a.txt")
		if got := scan(t, repo); len(got) != 0 {
			t.Errorf("got %+v, want none", got)
		}
	})
	t.Run("bare repo", func(t *testing.T) {
		isolateGit(t)
		dir := t.TempDir()
		run(t, dir, at(1, 9), "init", "-q", "--bare")
		if got := scan(t, dir); len(got) != 0 {
			t.Errorf("got %+v, want none", got)
		}
	})
}

func TestScanRejectsNonRepository(t *testing.T) {
	isolateGit(t)
	_, err := Scan(context.Background(), t.TempDir(), time.Time{}, time.Time{}, nil)
	if err == nil || !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("err = %v, want a not-a-git-repository error", err)
	}
}

func TestBuildEvents(t *testing.T) {
	isBranch := func(name string) bool { return !strings.HasPrefix(name, "deadbeef") }
	entry := func(sec int64, subject string) reflogEntry {
		return reflogEntry{At: time.Unix(sec, 0).UTC(), Hash: "h", Subject: subject}
	}

	tests := []struct {
		name    string
		entries []reflogEntry
		current string
		want    []string // "unix branch" per event
	}{
		{
			name:    "checkout and switch",
			entries: []reflogEntry{entry(1, "checkout: moving from main to a"), entry(2, "switch: moving from a to b")},
			current: "b",
			want:    []string{"1 a", "2 b"},
		},
		{
			name:    "commit uses branch from earlier checkout",
			entries: []reflogEntry{entry(1, "checkout: moving from main to a"), entry(2, "commit: x"), entry(3, "commit (amend): x")},
			current: "a",
			want:    []string{"1 a", "2 a", "3 a"},
		},
		{
			name:    "commit before any checkout uses origin branch",
			entries: []reflogEntry{entry(1, "commit: x"), entry(2, "checkout: moving from main to a")},
			current: "a",
			want:    []string{"1 main", "2 a"},
		},
		{
			name:    "detached target has empty branch",
			entries: []reflogEntry{entry(1, "checkout: moving from main to deadbeef1")},
			current: "",
			want:    []string{"1 "},
		},
		{
			name: "rebase emits only the finish state",
			entries: []reflogEntry{
				entry(1, "rebase (start): checkout main"),
				entry(2, "rebase (pick): x"),
				entry(3, "rebase (pick): y"),
				entry(4, "rebase (finish): returning to refs/heads/feat"),
			},
			current: "feat",
			want:    []string{"4 feat"},
		},
		{
			name: "legacy rebase checkout is suppressed",
			entries: []reflogEntry{
				entry(1, "rebase -i (start): checkout main"),
				entry(2, "checkout: moving from feat to deadbeef2"),
				entry(3, "rebase -i (finish): returning to refs/heads/feat"),
			},
			current: "feat",
			want:    []string{"3 feat"},
		},
		{
			name:    "reset pull merge are ignored",
			entries: []reflogEntry{entry(1, "reset: moving to HEAD~1"), entry(2, "pull: Fast-forward"), entry(3, "merge x: Fast-forward")},
			current: "main",
			want:    nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, e := range buildEvents("/r", tt.entries, tt.current, isBranch, nil) {
				got = append(got, strconv.FormatInt(e.At.Unix(), 10)+" "+e.Branch)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseReflogLine(t *testing.T) {
	e, ok := parseReflogLine("HEAD@{1710000000}\x1fabc123\x1fcheckout: moving from a to b")
	if !ok || e.At.Unix() != 1710000000 || e.Hash != "abc123" || e.Subject != "checkout: moving from a to b" {
		t.Errorf("got %+v ok=%v", e, ok)
	}
	for _, bad := range []string{"", "garbage", "HEAD@{x}\x1fh\x1fs"} {
		if _, ok := parseReflogLine(bad); ok {
			t.Errorf("parseReflogLine(%q) ok, want rejected", bad)
		}
	}
}
