package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/paths"
	"github.com/JtheGunner/ticktack/internal/store"
)

const secretCommitMessage = "SECRETMSG-zebra-quartz-9141"

// gitIn runs git in dir with a fixed author, committer and date.
func gitIn(t *testing.T, dir string, when time.Time, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	stamp := when.Format(time.RFC3339)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp,
		"GIT_AUTHOR_NAME=Test User", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test User", "GIT_COMMITTER_EMAIL=test@example.com",
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func gitCommit(t *testing.T, dir string, when time.Time, file string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, when, "add", file)
	gitIn(t, dir, when, "commit", "-q", "-m", secretCommitMessage+" "+file)
}

// newScanRepo creates a repository below parent named name with one commit on
// main (10 Feb 2026), a checkout of a ticket branch (26 Feb) and a commit on
// it (27 Feb). dailyNow is 2 Mar 2026, so the default window starts 23 Feb.
func newScanRepo(t *testing.T, parent, name string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed; skipping scan tests that need a real repository")
	}
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	day := func(d int) time.Time { return time.Date(2026, 2, d, 9, 0, 0, 0, time.UTC) }
	gitIn(t, dir, day(10), "init", "-q", "-b", "main")
	gitCommit(t, dir, day(10), "a.txt")
	gitIn(t, dir, day(26), "checkout", "-q", "-b", "feature/ABC-12-login")
	gitCommit(t, dir, day(27), "b.txt")
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

type scanEnv struct {
	deps   dailyDeps
	out    *bytes.Buffer
	dbPath string
	home   string
}

// newScanEnv returns deps whose store lives at a known path and whose config
// lists repoPaths.
func newScanEnv(t *testing.T, repoPaths ...string) *scanEnv {
	t.Helper()
	deps, _, out := newDailyEnv(t, "")
	dbPath := filepath.Join(t.TempDir(), "events.db")
	deps.OpenStore = func() (dailyStore, error) { return store.Open(dbPath) }
	load := deps.LoadConfig
	deps.LoadConfig = func() (config.Config, error) {
		cfg, err := load()
		cfg.RepoPaths = repoPaths
		return cfg, err
	}
	deps.ConfigPath = func() (string, error) { return "/cfg/ticktack/config.toml", nil }
	return &scanEnv{deps: deps, out: out, dbPath: dbPath, home: t.TempDir()}
}

func (e *scanEnv) events(t *testing.T) []event.Event {
	t.Helper()
	st, err := store.Open(e.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	events, err := st.Events(context.Background(), time.Unix(0, 0), dailyNow.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func (e *scanEnv) scan(since string) error {
	e.out.Reset()
	return runScan(context.Background(), e.deps, e.home, defaultRepoScanner, since)
}

func TestScanWritesContextEventsOnly(t *testing.T) {
	repo := newScanRepo(t, t.TempDir(), "proj")
	env := newScanEnv(t, repo)
	if err := env.scan(""); err != nil {
		t.Fatal(err)
	}
	events := env.events(t)
	if len(events) != 2 {
		t.Fatalf("events = %+v, want checkout and commit inside the default window", events)
	}
	for _, e := range events {
		if e.Source != "git" || e.Kind != event.KindContext || e.Repo != repo ||
			e.Branch != "feature/ABC-12-login" || e.Ticket != "ABC-12" || e.Key == "" {
			t.Errorf("unexpected event %+v", e)
		}
		if e.App != "" || e.IdleSeconds != 0 {
			t.Errorf("event carries more than repo, branch, ticket and time: %+v", e)
		}
	}
	if !strings.Contains(env.out.String(), "2 new events") {
		t.Errorf("summary = %q", env.out.String())
	}
}

func TestScanIsIdempotent(t *testing.T) {
	repo := newScanRepo(t, t.TempDir(), "proj")
	env := newScanEnv(t, repo)
	for i := 0; i < 3; i++ {
		if err := env.scan("2026-02-01"); err != nil {
			t.Fatal(err)
		}
		if got := len(env.events(t)); got != 3 {
			t.Fatalf("run %d: %d rows, want 3", i+1, got)
		}
	}
	if !strings.Contains(env.out.String(), "0 new events (3 found") {
		t.Errorf("second scan must report nothing new: %q", env.out.String())
	}
}

func TestScanSinceLimitsTheWindow(t *testing.T) {
	repo := newScanRepo(t, t.TempDir(), "proj")
	tests := []struct {
		name  string
		since string
		want  int
	}{
		{"default is the last 7 days", "", 2},
		{"earlier day reaches the first commit", "2026-02-01", 3},
		{"later day drops the checkout", "2026-02-27", 1},
		{"after everything", "2026-03-01", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newScanEnv(t, repo)
			if err := env.scan(tt.since); err != nil {
				t.Fatal(err)
			}
			if got := len(env.events(t)); got != tt.want {
				t.Errorf("events = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestScanRejectsBadSince(t *testing.T) {
	env := newScanEnv(t, "/work/a")
	err := env.scan("yesterday")
	if err == nil || !strings.Contains(err.Error(), "YYYY-MM-DD") {
		t.Fatalf("err = %v", err)
	}
}

func TestScanWhilePausedWritesNothing(t *testing.T) {
	repo := newScanRepo(t, t.TempDir(), "proj")
	env := newScanEnv(t, repo)
	pausePath, err := paths.PausePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(pausePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pausePath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := env.scan("2026-02-01"); err != nil {
		t.Fatal(err)
	}
	if got := len(env.events(t)); got != 0 || !strings.Contains(env.out.String(), "paused") {
		t.Fatalf("paused scan stored %d events, output %q", got, env.out.String())
	}
	if err := os.Remove(pausePath); err != nil {
		t.Fatal(err)
	}
	if err := env.scan("2026-02-01"); err != nil || len(env.events(t)) != 3 {
		t.Fatalf("resumed scan: err %v", err)
	}
}

func TestScanNeedsRepoPaths(t *testing.T) {
	env := newScanEnv(t)
	err := env.scan("")
	if err == nil || !strings.Contains(err.Error(), "repo_paths") || !strings.Contains(err.Error(), "/cfg/ticktack/config.toml") {
		t.Fatalf("err = %v, want a message naming repo_paths and the config file", err)
	}
}

func TestScanReportsBadPathsAndScansTheRest(t *testing.T) {
	parent := t.TempDir()
	good := newScanRepo(t, parent, "good")
	plainDir := filepath.Join(parent, "plain")
	if err := os.MkdirAll(plainDir, 0o700); err != nil {
		t.Fatal(err)
	}
	aFile := filepath.Join(parent, "afile")
	if err := os.WriteFile(aFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(parent, "missing\x1b[31m")
	subdir := filepath.Join(good, "sub")
	if err := os.MkdirAll(subdir, 0o700); err != nil {
		t.Fatal(err)
	}

	env := newScanEnv(t, missing, aFile, plainDir, subdir, good)
	err := env.scan("")
	if err == nil || !strings.Contains(err.Error(), "4 of 5") {
		t.Fatalf("err = %v, want the failed count", err)
	}
	text := env.out.String()
	for _, want := range []string{"does not exist", "is not a directory", "is not the root of a git repository", "Scanned 1 of 5"} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "\x1b") {
		t.Errorf("output contains an escape byte:\n%q", text)
	}
	if got := len(env.events(t)); got != 2 {
		t.Errorf("good repository must still be scanned, events = %d", got)
	}
}

func TestScanExpandsTilde(t *testing.T) {
	env := newScanEnv(t, "~/work/proj")
	newScanRepo(t, filepath.Join(env.home, "work"), "proj")
	if err := env.scan(""); err != nil {
		t.Fatal(err)
	}
	events := env.events(t)
	if len(events) != 2 || !strings.HasPrefix(events[0].Repo, "/") || strings.Contains(events[0].Repo, "~") {
		t.Fatalf("events = %+v", events)
	}
}

func TestScanStoresNoCommitMessages(t *testing.T) {
	repo := newScanRepo(t, t.TempDir(), "proj")
	env := newScanEnv(t, repo)
	if err := env.scan("2026-02-01"); err != nil {
		t.Fatal(err)
	}
	for _, e := range env.events(t) {
		if strings.Contains(strings.Join([]string{e.Source, string(e.Kind), e.Repo, e.Branch, e.Ticket, e.App, e.Key}, "\x00"), "SECRETMSG") {
			t.Errorf("commit message leaked into event %+v", e)
		}
	}
	if strings.Contains(env.out.String(), "SECRETMSG") {
		t.Errorf("commit message printed: %q", env.out.String())
	}
	entries, err := os.ReadDir(filepath.Dir(env.dbPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(filepath.Dir(env.dbPath), entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte("SECRETMSG")) || bytes.Contains(raw, []byte("a.txt")) {
			t.Errorf("%s contains commit message or file name text", entry.Name())
		}
	}
}

func TestScanDiscoversReposBelowRepoRoots(t *testing.T) {
	parent := t.TempDir()
	repo := newScanRepo(t, parent, "proj")
	env := newScanEnv(t)
	load := env.deps.LoadConfig
	env.deps.LoadConfig = func() (config.Config, error) {
		cfg, err := load()
		cfg.RepoRoots = []string{parent}
		return cfg, err
	}
	if err := env.scan(""); err != nil {
		t.Fatal(err)
	}
	events := env.events(t)
	if len(events) != 2 || events[0].Repo != repo {
		t.Fatalf("events = %+v, want the two events of %s", events, repo)
	}
}

func TestTrackedReposUnionsPathsAndRootsWithoutDuplicates(t *testing.T) {
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := newScanRepo(t, parent, "a")
	b := newScanRepo(t, parent, "b")
	cfg := config.Config{RepoPaths: []string{a, "/elsewhere/c"}, RepoRoots: []string{parent}}

	got := trackedRepos(cfg, t.TempDir())
	want := []string{a, "/elsewhere/c", b}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("trackedRepos = %v, want %v", got, want)
	}
}

func TestScanCommandIsRegistered(t *testing.T) {
	cmd, _, err := NewRootCommand().Find([]string{"scan"})
	if err != nil || cmd.Name() != "scan" || cmd.Flags().Lookup("since") == nil {
		t.Fatalf("scan command or --since flag missing: %v", err)
	}
}
