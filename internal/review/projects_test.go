package review

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/resolver"
	"github.com/JtheGunner/ticktack/internal/sink"
	"github.com/JtheGunner/ticktack/internal/store"
)

type fakeProvisioner struct {
	projects []sink.ProjectRef
	err      error
}

func (f fakeProvisioner) Projects(context.Context) ([]sink.ProjectRef, error) {
	return f.projects, f.err
}

func (fakeProvisioner) FindIssueByMarker(context.Context, string, string) (string, bool, error) {
	return "", false, errors.New("not used")
}

func (fakeProvisioner) CreateIssue(context.Context, string, string, string) (string, error) {
	return "", errors.New("not used")
}

func TestRefreshProjectsStoresList(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	now := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)

	stale, err := RefreshProjects(ctx, fakeProvisioner{projects: []sink.ProjectRef{{Key: "A", Name: "Alpha"}}}, st, now)
	if err != nil || stale {
		t.Fatalf("RefreshProjects = (%v, %v), want fresh", stale, err)
	}
	got, at, err := st.Projects(ctx)
	if err != nil || len(got) != 1 || got[0] != (store.Project{Key: "A", Name: "Alpha"}) || !at.Equal(now) {
		t.Fatalf("cache = (%v, %v, %v)", got, at, err)
	}
}

func TestRefreshProjectsKeepsCacheOnError(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	old := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	if err := st.ReplaceProjects(ctx, []store.Project{{Key: "OLD", Name: "Old"}}, old); err != nil {
		t.Fatal(err)
	}

	boom := errors.New("network down")
	stale, err := RefreshProjects(ctx, fakeProvisioner{err: boom}, st, old.Add(time.Hour))
	if !stale || !errors.Is(err, boom) {
		t.Fatalf("RefreshProjects = (%v, %v), want stale with the cause", stale, err)
	}
	got, at, _ := st.Projects(ctx)
	if len(got) != 1 || got[0].Key != "OLD" || !at.Equal(old) {
		t.Errorf("cache changed on error: %v at %v", got, at)
	}
}

func TestRefreshProjectsKeepsCacheWhenListIsEmpty(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	old := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	if err := st.ReplaceProjects(ctx, []store.Project{{Key: "OLD", Name: "Old"}}, old); err != nil {
		t.Fatal(err)
	}
	stale, err := RefreshProjects(ctx, fakeProvisioner{}, st, old.Add(time.Hour))
	if !stale || err == nil {
		t.Fatalf("RefreshProjects = (%v, %v), want stale with an error", stale, err)
	}
	if got, _, _ := st.Projects(ctx); len(got) != 1 {
		t.Errorf("an empty answer wiped the cache: %v", got)
	}
}

func TestLoadRulesIncludesDefaults(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	if err := st.ReplaceProjects(ctx, []store.Project{{Key: "TICKT", Name: "ticktack"}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.SetDefaultTask(ctx, "TICKT", "TICKT-7"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", "/home/me")
	cfg := config.Config{
		RepoRoots:    []string{"~/Projects"},
		DefaultIssue: &config.DefaultIssue{Project: "GEN", Summary: "General"},
	}

	rules, err := LoadRules(ctx, st, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if rules.GlobalProject != "GEN" || rules.DefaultTasks["TICKT"] != "TICKT-7" || len(rules.Projects) != 1 {
		t.Fatalf("rules = %+v", rules)
	}
	if folder, ok := rules.FolderOf("/home/me/Projects/ticktack/.worktrees/x"); !ok || folder != "ticktack" {
		t.Errorf("FolderOf = (%q, %v), want ticktack", folder, ok)
	}
	d := resolver.Decide(resolver.ContextSignal{Repo: "/home/me/Projects/ticktack"}, rules)
	if d.Ticket != "TICKT-7" || d.Rule != resolver.RuleProjectDefault {
		t.Errorf("Decide = %+v, want the cached standard task", d)
	}
}

func TestLoadRulesWithoutDefaultIssueKeepsDefaultsOff(t *testing.T) {
	st := openStore(t)
	rules, err := LoadRules(context.Background(), st, config.Config{RepoRoots: []string{"/work"}})
	if err != nil {
		t.Fatal(err)
	}
	if rules.GlobalProject != "" {
		t.Errorf("GlobalProject = %q, want empty", rules.GlobalProject)
	}
	d := resolver.Decide(resolver.ContextSignal{Repo: "/work/app"}, rules)
	if d.Tier != resolver.TierUnknown {
		t.Errorf("Decide = %+v, want unknown while default_issue is absent", d)
	}
}
