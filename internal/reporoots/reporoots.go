// Package reporoots finds the Git repositories below configured root folders
// and maps a repository path to its folder name below a root. It only reads
// directory entries; it never opens repositories.
package reporoots

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JtheGunner/ticktack/internal/config"
)

// Discover returns the immediate children of every root that are Git
// repositories (a ".git" directory or file), as sorted, deduplicated absolute
// paths. Roots are written as in the config ("~/..." allowed). A root that
// cannot be expanded or read contributes nothing; `ticktack doctor` reports it.
func Discover(roots []string, home string) []string {
	seen := map[string]bool{}
	var repos []string
	for _, raw := range roots {
		root, err := config.ExpandRepoPath(raw, home)
		if err != nil {
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			path := filepath.Join(root, e.Name())
			if seen[path] || !e.IsDir() || !hasGit(path) {
				continue
			}
			seen[path] = true
			repos = append(repos, path)
		}
	}
	sort.Strings(repos)
	return repos
}

func hasGit(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}

// FolderOf returns the first path element of repo below the longest root that
// contains it, so a subdirectory or a worktree below a repository resolves to
// that repository's folder. It reports false when repo is outside every root or
// equals a root. It does not touch the file system.
func FolderOf(roots []string, home, repo string) (string, bool) {
	repo = filepath.Clean(repo)
	best := ""
	for _, raw := range roots {
		root, err := config.ExpandRepoPath(raw, home)
		if err != nil {
			continue
		}
		if strings.HasPrefix(repo, root+string(filepath.Separator)) && len(root) > len(best) {
			best = root
		}
	}
	if best == "" {
		return "", false
	}
	rest := strings.TrimPrefix(repo, best+string(filepath.Separator))
	folder, _, _ := strings.Cut(rest, string(filepath.Separator))
	if folder == "" {
		return "", false
	}
	return folder, true
}
