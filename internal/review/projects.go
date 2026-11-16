package review

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/JtheGunner/ticktack/internal/sink"
	"github.com/JtheGunner/ticktack/internal/store"
)

// projectCache is the part of the store RefreshProjects writes.
type projectCache interface {
	ReplaceProjects(ctx context.Context, projects []store.Project, fetchedAt time.Time) error
}

// RefreshProjects replaces the cached YouTrack project list with a fresh one.
// It is read-only towards YouTrack. When the list cannot be fetched, or comes
// back empty, the cache stays as it was and stale is true together with the
// cause, so callers can continue with the old list and say so.
func RefreshProjects(ctx context.Context, src sink.TaskProvisioner, cache projectCache, now time.Time) (stale bool, err error) {
	refs, err := src.Projects(ctx)
	if err != nil {
		return true, fmt.Errorf("refresh project list: %w", err)
	}
	if len(refs) == 0 {
		return true, errors.New("refresh project list: YouTrack returned no projects")
	}
	projects := make([]store.Project, len(refs))
	for i, r := range refs {
		projects[i] = store.Project{Key: r.Key, Name: r.Name}
	}
	if err := cache.ReplaceProjects(ctx, projects, now); err != nil {
		return true, err
	}
	return false, nil
}
