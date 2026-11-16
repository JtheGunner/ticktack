package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/review"
	"github.com/JtheGunner/ticktack/internal/sink"
	"github.com/JtheGunner/ticktack/internal/youtrack"
)

// fakeProjectSource is an issue source that can also list projects.
type fakeProjectSource struct {
	projects []sink.ProjectRef
	err      error
}

func (fakeProjectSource) MyOpenIssues(context.Context) ([]youtrack.Issue, error) { return nil, nil }

func (f fakeProjectSource) Projects(context.Context) ([]sink.ProjectRef, error) {
	return f.projects, f.err
}

func (fakeProjectSource) FindIssueByMarker(context.Context, string, string) (string, bool, error) {
	return "", false, errors.New("not used")
}

func (fakeProjectSource) CreateIssue(context.Context, string, string, string) (string, error) {
	return "", errors.New("not used")
}

func withStandardTasks(deps reviewDeps, src fakeProjectSource) reviewDeps {
	load := deps.LoadConfig
	deps.LoadConfig = func() (config.Config, error) {
		cfg, err := load()
		cfg.RepoRoots = []string{"/work"}
		cfg.DefaultIssue = &config.DefaultIssue{Project: "GEN", Summary: "Meetings / Admin"}
		return cfg, err
	}
	deps.NewIssueSource = func(config.Config) (review.IssueSource, error) { return src, nil }
	return deps
}

func TestTicketLabelShowsPlaceholderAsNewStandardTask(t *testing.T) {
	for in, want := range map[string]string{
		"TICKT:default": "TICKT · standard task (new)",
		"ABC-1":         "ABC-1",
		"":              msgUnassigned,
	} {
		if got := ticketLabel(in); got != want {
			t.Errorf("ticketLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReviewRefreshesProjectListAndShowsTheMatchingStandardTask(t *testing.T) {
	deps, st, out := repoOnlyDeps(t, "q\n")
	deps = withStandardTasks(deps, fakeProjectSource{projects: []sink.ProjectRef{{Key: "APP", Name: "App"}}})

	if err := runReview(context.Background(), deps, "2026-01-05"); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "APP · standard task (new)") {
		t.Errorf("review does not show the project's standard task:\n%s", got)
	}
	projects, _, err := st.Projects(context.Background())
	if err != nil || len(projects) != 1 || projects[0].Key != "APP" {
		t.Errorf("project cache = (%v, %v), want APP", projects, err)
	}
}

func TestReviewContinuesWithTheGlobalTaskWhenTheProjectListIsUnavailable(t *testing.T) {
	deps, _, out := repoOnlyDeps(t, "q\n")
	deps = withStandardTasks(deps, fakeProjectSource{err: errors.New("network down")})

	if err := runReview(context.Background(), deps, "2026-01-05"); err != nil {
		t.Fatalf("an unreachable YouTrack must not fail the review: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "project list") || !strings.Contains(got, "network down") {
		t.Errorf("review does not say the project list is stale:\n%s", got)
	}
	if !strings.Contains(got, "GEN · standard task (new)") {
		t.Errorf("review does not fall back to the global standard task:\n%s", got)
	}
}
