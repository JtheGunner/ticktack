package reporoots

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func mkdir(t *testing.T, parts ...string) string {
	t.Helper()
	p := filepath.Join(parts...)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDiscoverFindsChildRepos(t *testing.T) {
	root := t.TempDir()
	mkdir(t, root, "app", ".git")
	linked := mkdir(t, root, "linked")
	if err := os.WriteFile(filepath.Join(linked, ".git"), []byte("gitdir: /elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mkdir(t, root, "plain")
	mkdir(t, root, "plain", "nested", ".git") // grandchild: ignored
	if err := os.WriteFile(filepath.Join(root, "file.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	got := Discover([]string{root}, "/unused")
	want := []string{filepath.Join(root, "app"), filepath.Join(root, "linked")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Discover = %v, want %v", got, want)
	}
}

func TestDiscoverExpandsTildeAndDedupes(t *testing.T) {
	home := t.TempDir()
	mkdir(t, home, "Projects", "app", ".git")

	got := Discover([]string{"~/Projects", filepath.Join(home, "Projects")}, home)
	want := []string{filepath.Join(home, "Projects", "app")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Discover = %v, want %v", got, want)
	}
}

func TestDiscoverIgnoresUnreadableRoot(t *testing.T) {
	if got := Discover([]string{filepath.Join(t.TempDir(), "missing")}, "/home"); len(got) != 0 {
		t.Errorf("Discover = %v, want none", got)
	}
}

func TestFolderOf(t *testing.T) {
	roots := []string{"/work", "~/Projects", "/work/deep"}
	home := "/home/me"
	tests := []struct {
		name string
		repo string
		want string
		ok   bool
	}{
		{"direct child", "/work/app", "app", true},
		{"subdirectory of a repo", "/work/app/sub/dir", "app", true},
		{"worktree below the repo", "/work/app/.worktrees/x/sub", "app", true},
		{"tilde root", "/home/me/Projects/ticktack", "ticktack", true},
		{"longest root wins", "/work/deep/inner/x", "inner", true},
		{"repo equals root", "/work", "", false},
		{"outside every root", "/other/app", "", false},
		{"prefix is not a boundary", "/workshop/app", "", false},
		{"trailing slash on repo", "/work/app/", "app", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := FolderOf(roots, home, tt.repo)
			if got != tt.want || ok != tt.ok {
				t.Errorf("FolderOf(%q) = (%q, %v), want (%q, %v)", tt.repo, got, ok, tt.want, tt.ok)
			}
		})
	}
}
