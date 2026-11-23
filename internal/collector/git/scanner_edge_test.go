package git

import (
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

// runFails executes git and expects it to exit non-zero (a conflicting rebase).
func runFails(t *testing.T, dir string, when time.Time, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	stamp := when.Format(time.RFC3339)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp)
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("git %s succeeded, want a failure\n%s", strings.Join(args, " "), out)
	}
}

func writeFile(t *testing.T, dir, file, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// conflictingRebase leaves repo stopped in a rebase of feature/ABC-7 onto main.
func conflictingRebase(t *testing.T) string {
	t.Helper()
	repo := newRepo(t)
	commit(t, repo, at(1, 9), "a.txt")
	run(t, repo, at(2, 9), "checkout", "-q", "-b", "feature/ABC-7")
	writeFile(t, repo, "a.txt", "feature")
	run(t, repo, at(2, 10), "commit", "-q", "-am", "feature change")
	run(t, repo, at(3, 9), "checkout", "-q", "main")
	writeFile(t, repo, "a.txt", "main")
	run(t, repo, at(3, 10), "commit", "-q", "-am", "main change")
	run(t, repo, at(4, 9), "checkout", "-q", "feature/ABC-7")
	runFails(t, repo, at(5, 9), "rebase", "main")
	return repo
}

// eventsFrom returns "day/hour branch ticket" for every event at or after from.
func eventsFrom(events []event.Event, from time.Time) []string {
	var got []string
	for _, e := range events {
		if e.At.Before(from) {
			continue
		}
		got = append(got, e.At.Format("02/15")+" "+e.Branch+" "+e.Ticket)
	}
	return got
}

func TestScanAfterUnfinishedRebase(t *testing.T) {
	tests := []struct {
		name string
		end  []string // how the stopped rebase ends; nil leaves it unfinished
		want []string
	}{
		{
			name: "quit",
			end:  []string{"rebase", "--quit"},
			want: []string{"06/09 feature/ABC-8 ABC-8", "06/10 feature/ABC-8 ABC-8"},
		},
		{
			name: "abort",
			end:  []string{"rebase", "--abort"},
			want: []string{"05/10 feature/ABC-7 ABC-7", "06/09 feature/ABC-8 ABC-8", "06/10 feature/ABC-8 ABC-8"},
		},
		{
			name: "still stopped",
			end:  nil,
			want: []string{"06/09 feature/ABC-8 ABC-8", "06/10 feature/ABC-8 ABC-8"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := conflictingRebase(t)
			if tt.end != nil {
				run(t, repo, at(5, 10), tt.end...)
			}
			run(t, repo, at(6, 9), "checkout", "-q", "-f", "-b", "feature/ABC-8", "main")
			commit(t, repo, at(6, 10), "b.txt")

			got := eventsFrom(scan(t, repo), at(5, 10))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// headReflog returns the action text of every HEAD reflog line, oldest first.
func headReflog(t *testing.T, repo string) []string {
	t.Helper()
	lines := strings.Split(run(t, repo, at(1, 9), "reflog", "show", "--format=%gs", "HEAD"), "\n")
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	return lines
}

// TestScanIgnoresBranchReturnInReplayedSubject replays a commit whose subject
// reads like the closing line of a rebase. Only the real closing line may move
// the tracked branch.
func TestScanIgnoresBranchReturnInReplayedSubject(t *testing.T) {
	repo := newRepo(t)
	commit(t, repo, at(1, 9), "a.txt")
	run(t, repo, at(2, 9), "checkout", "-q", "-b", "feature/ABC-9")
	writeFile(t, repo, "b.txt", "b")
	run(t, repo, at(2, 10), "add", "b.txt")
	run(t, repo, at(2, 10), "commit", "-q", "-m", "returning to refs/heads/feature/XYZ-1")
	commit(t, repo, at(2, 11), "c.txt")
	run(t, repo, at(3, 9), "checkout", "-q", "main")
	commit(t, repo, at(3, 10), "d.txt")
	run(t, repo, at(4, 9), "checkout", "-q", "feature/ABC-9")
	run(t, repo, at(5, 9), "rebase", "-q", "main")
	commit(t, repo, at(5, 10), "e.txt")

	reflog := headReflog(t, repo)
	replayed := "rebase (pick): returning to refs/heads/feature/XYZ-1"
	if !containsLine(reflog, replayed) {
		t.Fatalf("reflog holds no line %q:\n%s", replayed, strings.Join(reflog, "\n"))
	}

	got := eventsFrom(scan(t, repo), at(4, 9))
	want := []string{"04/09 feature/ABC-9 ABC-9", "05/09 feature/ABC-9 ABC-9", "05/10 feature/ABC-9 ABC-9"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func containsLine(lines []string, line string) bool {
	for _, l := range lines {
		if l == line {
			return true
		}
	}
	return false
}

func TestScanFollowsBranchRename(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, repo string)
		want  []string
	}{
		{
			name: "renamed after a checkout",
			setup: func(t *testing.T, repo string) {
				commit(t, repo, at(1, 9), "a.txt")
				run(t, repo, at(2, 9), "checkout", "-q", "-b", "feature/ABC-1")
				commit(t, repo, at(2, 10), "b.txt")
				run(t, repo, at(3, 9), "branch", "-m", "feature/ABC-2-login")
				commit(t, repo, at(3, 10), "c.txt")
			},
			want: []string{
				"01/09 main ",
				"02/09 feature/ABC-1 ABC-1",
				"02/10 feature/ABC-1 ABC-1",
				"03/09 feature/ABC-2-login ABC-2",
				"03/10 feature/ABC-2-login ABC-2",
			},
		},
		{
			name: "renamed without any checkout",
			setup: func(t *testing.T, repo string) {
				commit(t, repo, at(1, 9), "a.txt")
				run(t, repo, at(2, 9), "branch", "-m", "feature/ABC-3")
				commit(t, repo, at(2, 10), "b.txt")
			},
			want: []string{
				"01/09 main ",
				"02/09 feature/ABC-3 ABC-3",
				"02/10 feature/ABC-3 ABC-3",
			},
		},
		{
			name: "renamed twice",
			setup: func(t *testing.T, repo string) {
				commit(t, repo, at(1, 9), "a.txt")
				run(t, repo, at(2, 9), "branch", "-m", "wip")
				run(t, repo, at(3, 9), "branch", "-m", "feature/ABC-4")
				commit(t, repo, at(3, 10), "b.txt")
			},
			want: []string{
				"01/09 main ",
				"02/09 wip ",
				"03/09 feature/ABC-4 ABC-4",
				"03/10 feature/ABC-4 ABC-4",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newRepo(t)
			tt.setup(t, repo)
			got := eventsFrom(scan(t, repo), time.Time{})
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestScanTreatsRefExpressionCheckoutAsDetached(t *testing.T) {
	tests := []struct {
		name   string
		target string
	}{
		{"ancestor", "feature/ABC-5~1"},
		{"parent", "feature/ABC-5^"},
		{"reflog selector", "feature/ABC-5@{1}"},
		{"peeled", "feature/ABC-5^{commit}"},
		{"dated reflog selector with spaces", "feature/ABC-5@{1 day ago}"},
		{"commit message search with spaces", ":/add b.txt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newRepo(t)
			commit(t, repo, at(1, 9), "a.txt")
			run(t, repo, at(2, 9), "checkout", "-q", "-b", "feature/ABC-5")
			commit(t, repo, at(2, 10), "b.txt")
			commit(t, repo, at(2, 11), "c.txt")
			run(t, repo, at(3, 9), "checkout", "-q", "main")
			run(t, repo, at(4, 9), "checkout", "-q", tt.target)
			commit(t, repo, at(4, 10), "d.txt")

			// Git records the target exactly as it was typed.
			recorded := "checkout: moving from main to " + tt.target
			if reflog := headReflog(t, repo); !containsLine(reflog, recorded) {
				t.Fatalf("reflog holds no line %q:\n%s", recorded, strings.Join(reflog, "\n"))
			}

			got := eventsFrom(scan(t, repo), at(4, 9))
			want := []string{"04/09  ", "04/10  "}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

func TestScanIgnoresInheritedGitEnvironment(t *testing.T) {
	other := newRepo(t)
	commit(t, other, at(1, 9), "other.txt")
	run(t, other, at(1, 10), "checkout", "-q", "-b", "feature/OTHER-1")

	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run(t, repo, at(2, 9), "init", "-q", "-b", "main")
	commit(t, repo, at(2, 9), "a.txt")
	run(t, repo, at(2, 10), "checkout", "-q", "-b", "feature/ABC-6")

	tests := []struct {
		name string
		env  map[string]string
	}{
		{"GIT_DIR", map[string]string{"GIT_DIR": filepath.Join(other, ".git")}},
		{"GIT_DIR and GIT_WORK_TREE", map[string]string{"GIT_DIR": filepath.Join(other, ".git"), "GIT_WORK_TREE": other}},
		{"GIT_COMMON_DIR", map[string]string{"GIT_COMMON_DIR": filepath.Join(other, ".git")}},
		{"GIT_INDEX_FILE", map[string]string{"GIT_INDEX_FILE": filepath.Join(other, ".git", "index")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			got := eventsFrom(scan(t, repo), time.Time{})
			want := []string{"02/09 main ", "02/10 feature/ABC-6 ABC-6"}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %q, want %q", got, want)
			}
			for _, e := range scan(t, repo) {
				if e.Repo != repo {
					t.Errorf("Repo = %q, want %q", e.Repo, repo)
				}
			}
		})
	}
}

func TestCommandEnv(t *testing.T) {
	t.Setenv("GIT_DIR", "/elsewhere/.git")
	t.Setenv("GIT_WORK_TREE", "/elsewhere")
	t.Setenv("GIT_INDEX_FILE", "/elsewhere/.git/index")
	t.Setenv("GIT_COMMON_DIR", "/elsewhere/.git")
	t.Setenv("GIT_OBJECT_DIRECTORY", "/elsewhere/.git/objects")
	t.Setenv("GIT_ALTERNATE_OBJECT_DIRECTORIES", "/elsewhere/alt")
	t.Setenv("GIT_DIRECTORY_LOOKALIKE", "kept")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_NO_LAZY_FETCH", "0")
	t.Setenv("GIT_ALLOW_PROTOCOL", "file")

	env := map[string]string{}
	for _, kv := range CommandEnv() {
		name, value, _ := strings.Cut(kv, "=")
		env[name] = value
	}

	for _, name := range []string{
		"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR",
		"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES",
	} {
		if _, ok := env[name]; ok {
			t.Errorf("%s was passed on to git", name)
		}
	}
	for name, want := range map[string]string{
		"GIT_DIRECTORY_LOOKALIKE": "kept",
		"GIT_CONFIG_GLOBAL":       os.DevNull,
		"GIT_OPTIONAL_LOCKS":      "0",
		"GIT_NO_LAZY_FETCH":       "1",
		"GIT_ALLOW_PROTOCOL":      "none",
		"GIT_TERMINAL_PROMPT":     "0",
	} {
		if env[name] != want {
			t.Errorf("%s = %q, want %q", name, env[name], want)
		}
	}
}

func TestBuildEventsEdgeCases(t *testing.T) {
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
			name: "rebase without finish does not block later checkouts",
			entries: []reflogEntry{
				entry(1, "checkout: moving from main to feat"),
				entry(2, "rebase (start): checkout main"),
				entry(3, "rebase (pick): x"),
				entry(4, "checkout: moving from deadbeef1 to other"),
				entry(5, "commit: y"),
			},
			current: "other",
			want:    []string{"1 feat", "4 other", "5 other"},
		},
		{
			name: "interactive rebase without finish does not block later checkouts",
			entries: []reflogEntry{
				entry(1, "rebase -i (start): checkout main"),
				entry(2, "rebase -i (pick): x"),
				entry(3, "checkout: moving from deadbeef1 to other"),
			},
			current: "other",
			want:    []string{"3 other"},
		},
		{
			name: "abort returns to the branch",
			entries: []reflogEntry{
				entry(1, "rebase (start): checkout main"),
				entry(2, "rebase (abort): returning to refs/heads/feat"),
				entry(3, "commit: x"),
			},
			current: "feat",
			want:    []string{"2 feat", "3 feat"},
		},
		{
			name: "rebase replay lines are never commits or checkouts",
			entries: []reflogEntry{
				entry(1, "rebase (start): checkout main"),
				entry(2, "rebase (pick): commit: looks like a commit"),
				entry(3, "rebase (reword): checkout: moving from a to b"),
				entry(4, "rebase (continue): x"),
				entry(5, "rebase (finish): returning to refs/heads/feat"),
			},
			current: "feat",
			want:    []string{"5 feat"},
		},
		{
			name: "replayed subject that reads like a branch return is ignored",
			entries: []reflogEntry{
				entry(1, "checkout: moving from main to feat"),
				entry(2, "rebase (start): checkout main"),
				entry(3, "rebase (pick): returning to refs/heads/foo"),
				entry(4, "rebase -i (reword): returning to refs/heads/foo"),
				entry(5, "rebase (continue): returning to refs/heads/foo"),
				entry(6, "rebase: returning to refs/heads/foo"),
				entry(7, "rebase (finish): returning to refs/heads/feat"),
				entry(8, "commit: x"),
			},
			current: "feat",
			want:    []string{"1 feat", "7 feat", "8 feat"},
		},
		{
			name: "interactive rebase finish and abort return to the branch",
			entries: []reflogEntry{
				entry(1, "rebase -i (finish): returning to refs/heads/feat"),
				entry(2, "rebase -i (abort): returning to refs/heads/other"),
			},
			current: "other",
			want:    []string{"1 feat", "2 other"},
		},
		{
			name: "legacy rebase finish returns to the branch",
			entries: []reflogEntry{
				entry(1, "rebase: checkout main"),
				entry(2, "rebase: x"),
				entry(3, "rebase finished: returning to refs/heads/feat"),
			},
			current: "feat",
			want:    []string{"3 feat"},
		},
		{
			name: "rename moves later commits to the new name",
			entries: []reflogEntry{
				entry(1, "checkout: moving from main to old"),
				entry(2, "commit: x"),
				entry(3, "Branch: renamed refs/heads/old to refs/heads/new"),
				entry(4, "commit: y"),
			},
			current: "new",
			want:    []string{"1 old", "2 old", "3 new", "4 new"},
		},
		{
			name: "commit before a rename uses the old name",
			entries: []reflogEntry{
				entry(1, "commit: x"),
				entry(2, "Branch: renamed refs/heads/old to refs/heads/new"),
				entry(3, "checkout: moving from new to other"),
			},
			current: "other",
			want:    []string{"1 old", "2 new", "3 other"},
		},
		{
			name: "rename is reported once",
			entries: []reflogEntry{
				entry(1, "Branch: renamed refs/heads/old to refs/heads/new"),
				entry(1, "Branch: renamed refs/heads/old to refs/heads/new"),
			},
			current: "new",
			want:    []string{"1 new"},
		},
		{
			name: "ref expressions are detached",
			entries: []reflogEntry{
				entry(1, "checkout: moving from main to main~1"),
				entry(2, "checkout: moving from main~1 to main^2"),
				entry(3, "checkout: moving from main^2 to main@{1}"),
				entry(4, "checkout: moving from main@{1} to v1^{commit}"),
				entry(5, "checkout: moving from v1^{commit} to @"),
				entry(6, "checkout: moving from @ to a..b"),
				entry(7, "checkout: moving from a..b to main:path"),
				entry(8, "checkout: moving from main:path to feat"),
			},
			current: "feat",
			want:    []string{"1 ", "2 ", "3 ", "4 ", "5 ", "6 ", "7 ", "8 feat"},
		},
		{
			name: "ref expressions with spaces are detached",
			entries: []reflogEntry{
				entry(1, "checkout: moving from main to main@{1 day ago}"),
				entry(2, "commit: x"),
				entry(3, "checkout: moving from deadbeef1 to :/some text"),
				entry(4, "checkout: moving from deadbeef1 to :/back to main"),
				entry(5, "checkout: moving from deadbeef1 to feat"),
			},
			current: "feat",
			want:    []string{"1 ", "2 ", "3 ", "4 ", "5 feat"},
		},
		{
			name: "commit before a checkout of a spaced ref expression uses the origin branch",
			entries: []reflogEntry{
				entry(1, "commit: x"),
				entry(2, "checkout: moving from main to :/back to feat"),
				entry(3, "commit: y"),
			},
			current: "",
			want:    []string{"1 main", "2 ", "3 "},
		},
		{
			name: "commit before a ref expression checkout uses the origin branch",
			entries: []reflogEntry{
				entry(1, "commit: x"),
				entry(2, "checkout: moving from main to main~1"),
				entry(3, "commit: y"),
			},
			current: "",
			want:    []string{"1 main", "2 ", "3 "},
		},
		{
			name: "commit before leaving a ref expression is detached",
			entries: []reflogEntry{
				entry(1, "commit: x"),
				entry(2, "checkout: moving from main~1 to main"),
			},
			current: "main",
			want:    []string{"1 ", "2 main"},
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

// TestBuildEventsKeepsStoredKeys pins the dedupe key of events that were
// already emitted before, so a re-scan never duplicates a stored event.
func TestBuildEventsKeepsStoredKeys(t *testing.T) {
	isBranch := func(name string) bool { return !strings.HasPrefix(name, "deadbeef") }
	entries := []reflogEntry{
		{At: time.Unix(1, 0).UTC(), Hash: "h1", Subject: "commit (initial): x"},
		{At: time.Unix(2, 0).UTC(), Hash: "h2", Subject: "checkout: moving from main to feat"},
		{At: time.Unix(3, 0).UTC(), Hash: "h3", Subject: "commit: y"},
		{At: time.Unix(4, 0).UTC(), Hash: "h4", Subject: "checkout: moving from feat to deadbeef1"},
		{At: time.Unix(5, 0).UTC(), Hash: "h5", Subject: "checkout: moving from deadbeef1 to feat"},
		{At: time.Unix(6, 0).UTC(), Hash: "h6", Subject: "rebase (start): checkout main"},
		{At: time.Unix(7, 0).UTC(), Hash: "h7", Subject: "rebase (pick): y"},
		{At: time.Unix(8, 0).UTC(), Hash: "h8", Subject: "rebase (finish): returning to refs/heads/feat"},
		{At: time.Unix(9, 0).UTC(), Hash: "h9", Subject: "commit (amend): y"},
	}
	want := []string{
		"git:/r:commit:1:h1:main",
		"git:/r:checkout:2:h2:feat",
		"git:/r:commit:3:h3:feat",
		"git:/r:checkout:4:h4:",
		"git:/r:checkout:5:h5:feat",
		"git:/r:rebase:8:h8:feat",
		"git:/r:commit:9:h9:feat",
	}

	var got []string
	for _, e := range buildEvents("/r", entries, "feat", isBranch, nil) {
		got = append(got, e.Key)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("keys changed:\n got %q\nwant %q", got, want)
	}
}
