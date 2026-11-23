package commitsubject

import (
	"context"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/JtheGunner/ticktack/internal/collector/git"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
)

type fakeReader struct {
	commits map[string]Commit
	calls   int
}

func (f *fakeReader) Read(_ context.Context, _, hash string) (Commit, error) {
	f.calls++
	c, ok := f.commits[hash]
	if !ok {
		return Commit{}, errors.New("unknown commit")
	}
	return c, nil
}

func itemWith(subjects ...Commit) (sessionizer.DayItem, *fakeReader) {
	it := sessionizer.DayItem{Ticket: "ABC-1", Description: "ABC-1 — 3 blocks"}
	r := &fakeReader{commits: map[string]Commit{}}
	for i, c := range subjects {
		h := fmt.Sprintf("%040x", i+1)
		r.commits[h] = c
		it.Commits = append(it.Commits, sessionizer.CommitRef{Repo: "/repo", Hash: h, At: time.Unix(int64(i), 0)})
	}
	return it, r
}

func TestDescribe(t *testing.T) {
	long := strings.Repeat("x", 300)
	tests := []struct {
		name    string
		commits []Commit
		want    string
	}{
		{"no commits keeps the old text", nil, "ABC-1 — 3 blocks"},
		{"one subject", []Commit{{Subject: "Fix parser"}}, "Fix parser"},
		{"order is kept and duplicates drop", []Commit{{Subject: "b"}, {Subject: "a"}, {Subject: "b"}}, "b; a"},
		{"merge commits are left out", []Commit{{Subject: "Merge branch x", Merge: true}, {Subject: "real"}}, "real"},
		{"autosquash commits are left out", []Commit{{Subject: "fixup! real"}, {Subject: "squash! real"}, {Subject: "amend! real"}, {Subject: "real"}}, "real"},
		{"only filtered commits keeps the old text", []Commit{{Subject: "fixup! a"}, {Subject: "m", Merge: true}}, "ABC-1 — 3 blocks"},
		{"blank subject is left out", []Commit{{Subject: " \t "}, {Subject: "real"}}, "real"},
		{"five fit", []Commit{{Subject: "1"}, {Subject: "2"}, {Subject: "3"}, {Subject: "4"}, {Subject: "5"}}, "1; 2; 3; 4; 5"},
		{"more than five", []Commit{{Subject: "1"}, {Subject: "2"}, {Subject: "3"}, {Subject: "4"}, {Subject: "5"}, {Subject: "6"}, {Subject: "7"}}, "1; 2; 3; 4; 5; +2 more"},
		{"long subject is capped", []Commit{{Subject: long}}, strings.Repeat("x", 199) + "…"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			it, r := itemWith(tc.commits...)
			got := Describer{Reader: r}.Describe(context.Background(), it)
			if got != tc.want {
				t.Errorf("Describe = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDescribeUnreadableCommitsFallBack(t *testing.T) {
	it := sessionizer.DayItem{Description: "old", Commits: []sessionizer.CommitRef{{Repo: "/gone", Hash: strings.Repeat("a", 40)}}}
	got := Describer{Reader: &fakeReader{}}.Describe(context.Background(), it)
	if got != "old" {
		t.Errorf("Describe = %q, want the old text", got)
	}
}

func TestDescribeSkipsUnreadableAndKeepsReadable(t *testing.T) {
	it, r := itemWith(Commit{Subject: "kept"})
	it.Commits = append([]sessionizer.CommitRef{{Repo: "/gone", Hash: strings.Repeat("b", 40)}}, it.Commits...)
	if got := (Describer{Reader: r}).Describe(context.Background(), it); got != "kept" {
		t.Errorf("Describe = %q, want %q", got, "kept")
	}
}

func TestDescribeCapsReads(t *testing.T) {
	var commits []Commit
	for i := 0; i < maxCommitsRead+20; i++ {
		commits = append(commits, Commit{Subject: fmt.Sprintf("c%d", i)})
	}
	it, r := itemWith(commits...)
	Describer{Reader: r}.Describe(context.Background(), it)
	if r.calls != maxCommitsRead {
		t.Errorf("reads = %d, want %d", r.calls, maxCommitsRead)
	}
}

func TestCleanStripsControlCharactersAndDefusesMarkers(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"newline and tab", "a\nb\tc", "a b c"},
		{"escape sequence", "a\x1b[31mred", "a [31mred"},
		{"bidi override", "a" + string(rune(0x202e)) + "b", "a b"},
		{"invalid utf-8", "a\xffb", "a b"},
		{"marker lookalike", "see [ticktack:ABC-1:2026-01-01:Development] now", "see (ticktack:ABC-1:2026-01-01:Development] now"},
		{"collapses spaces", "  a   b  ", "a b"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Clean(tc.in)
			if got != tc.want {
				t.Errorf("Clean(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if !utf8.ValidString(got) || strings.Contains(got, "[ticktack:") {
				t.Errorf("Clean(%q) = %q is not safe", tc.in, got)
			}
		})
	}
}

func TestCleanCapsRunesNotBytes(t *testing.T) {
	got := Clean(strings.Repeat("ä", 500))
	if n := utf8.RuneCountInString(got); n != MaxSubjectRunes {
		t.Errorf("rune count = %d, want %d", n, MaxSubjectRunes)
	}
}

func TestPackageUsesNoNetwork(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range parsed.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if path == "net" || strings.HasPrefix(path, "net/") {
				t.Errorf("%s imports %s: reading commit subjects must stay local", f, path)
			}
		}
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestGitReaderReadsSubjectAndMergeFlag(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "commit", "-q", "--allow-empty", "-m", "first subject\n\nbody line")
	first := runGit(t, dir, "rev-parse", "HEAD")
	runGit(t, dir, "checkout", "-q", "-b", "side")
	runGit(t, dir, "commit", "-q", "--allow-empty", "-m", "side work")
	runGit(t, dir, "checkout", "-q", "main")
	runGit(t, dir, "commit", "-q", "--allow-empty", "-m", "main work")
	runGit(t, dir, "merge", "-q", "--no-ff", "-m", "Merge side", "side")
	merge := runGit(t, dir, "rev-parse", "HEAD")

	r := GitReader{}
	got, err := r.Read(context.Background(), dir, first)
	if err != nil {
		t.Fatal(err)
	}
	if got.Subject != "first subject" || got.Merge {
		t.Errorf("first = %+v", got)
	}
	got, err = r.Read(context.Background(), dir, merge)
	if err != nil {
		t.Fatal(err)
	}
	if got.Subject != "Merge side" || !got.Merge {
		t.Errorf("merge = %+v", got)
	}
}

func TestGitReaderRejectsUnsafeInput(t *testing.T) {
	r := GitReader{}
	good := strings.Repeat("a", 40)
	tests := []struct{ name, repo, hash string }{
		{"relative repo", "repo", good},
		{"option as hash", "/tmp", "--output=/tmp/x"},
		{"short hash", "/tmp", "abc123"},
		{"revision expression", "/tmp", "HEAD~1"},
		{"upper case hash", "/tmp", strings.Repeat("A", 40)},
		{"empty hash", "/tmp", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := r.Read(context.Background(), tc.repo, tc.hash); err == nil {
				t.Error("Read succeeded, want an error")
			}
		})
	}
}

// missingCommitWithPromisor returns a repository that lacks a commit which a
// local-path promisor remote holds, and that commit's hash. A lazy fetch would
// succeed, so a read error proves the remote was not used.
func missingCommitWithPromisor(t *testing.T) (local, hash string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	remote := t.TempDir()
	runGit(t, remote, "init", "-q", "-b", "main")
	runGit(t, remote, "config", "uploadpack.allowAnySHA1InWant", "true")
	runGit(t, remote, "commit", "-q", "--allow-empty", "-m", "only on the remote")
	hash = runGit(t, remote, "rev-parse", "HEAD")

	local = t.TempDir()
	runGit(t, local, "init", "-q", "-b", "main")
	runGit(t, local, "config", "remote.origin.url", remote)
	runGit(t, local, "config", "remote.origin.promisor", "true")
	runGit(t, local, "config", "remote.origin.partialclonefilter", "blob:none")
	runGit(t, local, "config", "extensions.partialClone", "origin")

	// Control: without the guard git fetches the commit, which proves the
	// setup would reach the remote.
	control := exec.Command("git", "-C", local, "log", "-1", "--no-walk", "--format=%s", hash, "--")
	control.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := control.CombinedOutput(); err != nil {
		t.Skipf("this git does not lazy-fetch commits here: %v\n%s", err, out)
	}
	runGit(t, local, "gc", "-q", "--prune=now") // keeps the fetched pack; drop it again below
	for _, p := range []string{"objects/pack", "objects/" + hash[:2]} {
		if err := os.RemoveAll(filepath.Join(local, ".git", p)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(local, ".git", "objects", "pack"), 0o755); err != nil {
		t.Fatal(err)
	}
	return local, hash
}

// A commit that is missing locally but present on a promisor remote must not
// be fetched: the read fails and no network call is made.
func TestGitReaderDoesNotLazyFetchFromPromisorRemote(t *testing.T) {
	local, hash := missingCommitWithPromisor(t)

	got, err := GitReader{}.Read(context.Background(), local, hash)
	if err == nil {
		t.Fatalf("Read fetched the commit from the promisor remote: %+v", got)
	}
}

// The protocol guard works on its own: with GIT_NO_LAZY_FETCH removed, as on a
// git that ignores it, GIT_ALLOW_PROTOCOL=none still refuses the fetch.
func TestCommandEnvBlocksLazyFetchWithoutNoLazyFetch(t *testing.T) {
	local, hash := missingCommitWithPromisor(t)

	cmd := exec.Command("git", "-C", local, "log", "-1", "--no-walk", "--format=%s", hash, "--")
	for _, kv := range git.CommandEnv() {
		if !strings.HasPrefix(kv, "GIT_NO_LAZY_FETCH=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("git fetched the commit through the promisor remote: %s", out)
	}
}

func TestGitReaderMissingRepositoryFails(t *testing.T) {
	_, err := GitReader{}.Read(context.Background(), filepath.Join(t.TempDir(), "gone"), strings.Repeat("a", 40))
	if err == nil {
		t.Error("Read succeeded for a missing repository")
	}
}
