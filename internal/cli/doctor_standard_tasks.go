package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/reporoots"
	"github.com/JtheGunner/ticktack/internal/store"
)

// checkRepoRoots is skipped while repo_roots is empty. Otherwise it names each
// root that is not a readable directory and counts the repositories found.
func checkRepoRoots(_ context.Context, deps doctorDeps) checkResult {
	cfg, path, err := loadConfig(deps)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return skipResult(msgCheckRepoRoots, msgRootsConfigBad)
	}
	if len(cfg.RepoRoots) == 0 {
		return skipResult(msgCheckRepoRoots, msgRootsNone)
	}
	var problems []string
	for _, raw := range cfg.RepoRoots {
		expanded, err := config.ExpandRepoPath(raw, deps.Home)
		if err != nil {
			problems = append(problems, fmt.Sprintf(msgRootMissing, SanitizeForTerminal(raw)))
			continue
		}
		if info, err := os.Stat(expanded); err != nil || !info.IsDir() {
			problems = append(problems, fmt.Sprintf(msgRootMissing, SanitizeForTerminal(raw)))
		}
	}
	if len(problems) > 0 {
		return failResult(msgCheckRepoRoots,
			fmt.Sprintf(msgRootsFail, len(problems), len(cfg.RepoRoots), strings.Join(problems, msgReposDetailJoiner)),
			fmt.Sprintf(msgRootsFix, path))
	}
	found := len(reporoots.Discover(cfg.RepoRoots, deps.Home))
	return okResult(msgCheckRepoRoots, fmt.Sprintf(msgRootsOK, len(cfg.RepoRoots), found))
}

// projectCache is the part of the store the standard task check reads.
type projectCache interface {
	Projects(ctx context.Context) ([]store.Project, time.Time, error)
}

// checkStandardTasks reports the state of the cached YouTrack project list the
// folder-to-project match relies on. It is skipped while [default_issue] is
// not configured.
func checkStandardTasks(ctx context.Context, deps doctorDeps) checkResult {
	cfg, _, err := loadConfig(deps)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return skipResult(msgCheckStandardTasks, msgStdTasksConfigBad)
	}
	if cfg.DefaultIssue == nil {
		return skipResult(msgCheckStandardTasks, msgStdTasksOff)
	}
	dbPath, err := deps.DBPath()
	if err != nil {
		return failResult(msgCheckStandardTasks, fmt.Sprintf(msgStdTasksDBFail, err), msgDBFix)
	}
	st, err := deps.OpenStore(dbPath)
	if err != nil {
		return failResult(msgCheckStandardTasks, fmt.Sprintf(msgStdTasksDBFail, err), msgDBFix)
	}
	defer func() { _ = st.Close() }()
	cache, ok := st.(projectCache)
	if !ok {
		return skipResult(msgCheckStandardTasks, msgStdTasksNeverFetch)
	}
	projects, fetched, err := cache.Projects(ctx)
	if err != nil {
		return failResult(msgCheckStandardTasks, fmt.Sprintf(msgStdTasksDBFail, err), msgDBFix)
	}
	if len(projects) == 0 {
		return skipResult(msgCheckStandardTasks, msgStdTasksNeverFetch)
	}
	return okResult(msgCheckStandardTasks, fmt.Sprintf(msgStdTasksOK, len(projects), formatAge(deps.Now().Sub(fetched))))
}

// formatAge renders an age as minutes, hours or days.
func formatAge(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	}
}
