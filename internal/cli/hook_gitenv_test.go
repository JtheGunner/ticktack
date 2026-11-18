package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// newHookRepo creates a throwaway repository on the given branch with one
// commit. It must run before the test sets any GIT_DIR-like variable.
func newHookRepo(t *testing.T, branch string) string {
	t.Helper()
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", branch},
		{"commit", "-q", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return repo
}

func TestGitRepoInfoIgnoresInheritedGitEnvironment(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_AUTHOR_NAME", "Test User")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Test User")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")

	other := newHookRepo(t, "feature/OTHER-1")
	repo := newHookRepo(t, "feature/ABC-7-x")

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
			out, err := gitRepoInfo(context.Background(), repo)
			if err != nil {
				t.Fatalf("gitRepoInfo: %v", err)
			}
			root, branch, ok := parseGitInfo(out)
			if !ok || root != repo || branch != "feature/ABC-7-x" {
				t.Errorf("got root %q branch %q ok=%v, want %q and feature/ABC-7-x", root, branch, ok, repo)
			}
		})
	}
}
