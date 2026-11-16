package cli

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/store"
)

const defaultIssueTOML = "[default_issue]\nproject = \"GEN\"\nsummary = \"Meetings / Admin\"\n"

func rootsTOML(roots ...string) string {
	quoted := make([]string, len(roots))
	for i, r := range roots {
		quoted[i] = strconv.Quote(r)
	}
	return "repo_roots = [" + strings.Join(quoted, ", ") + "]\n"
}

func TestCheckRepoRootsIsSkippedWithoutRoots(t *testing.T) {
	env := newTestEnv(t, "", "")
	if res := checkRepoRoots(context.Background(), env.deps.Doctor); res.Status != statusSkip {
		t.Fatalf("result = %+v, want skip", res)
	}
}

func TestCheckRepoRootsCountsRepositories(t *testing.T) {
	env := newTestEnv(t, "", "")
	root := t.TempDir()
	for _, name := range []string{"a", "b"} {
		if err := os.MkdirAll(filepath.Join(root, name, ".git"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "plain"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeRepoPathsConfig(t, rootsTOML(root))

	res := checkRepoRoots(context.Background(), env.deps.Doctor)
	if res.Status != statusOK || !strings.Contains(res.Detail, "2 repositories") {
		t.Fatalf("result = %+v, want ok with 2 repositories", res)
	}
}

func TestCheckRepoRootsNamesEachMissingRoot(t *testing.T) {
	env := newTestEnv(t, "", "")
	good := t.TempDir()
	missing := filepath.Join(t.TempDir(), "nope")
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	writeRepoPathsConfig(t, rootsTOML(good, missing, file))

	res := checkRepoRoots(context.Background(), env.deps.Doctor)
	if res.Status != statusFail {
		t.Fatalf("result = %+v, want fail", res)
	}
	for _, want := range []string{missing, file} {
		if !strings.Contains(res.Detail, want) {
			t.Errorf("detail %q does not name %s", res.Detail, want)
		}
	}
	if strings.Contains(res.Detail, good) {
		t.Errorf("detail names the healthy root: %q", res.Detail)
	}
	if !strings.Contains(res.Hint, "repo_roots") {
		t.Errorf("hint = %q, want it to point at repo_roots", res.Hint)
	}
}

func TestCheckStandardTasksIsSkippedWithoutDefaultIssue(t *testing.T) {
	env := newTestEnv(t, "", "")
	if res := checkStandardTasks(context.Background(), env.deps.Doctor); res.Status != statusSkip {
		t.Fatalf("result = %+v, want skip", res)
	}
}

func TestCheckStandardTasksSaysWhenTheProjectListWasNeverFetched(t *testing.T) {
	env := newTestEnv(t, "", "")
	writeRepoPathsConfig(t, defaultIssueTOML)

	res := checkStandardTasks(context.Background(), env.deps.Doctor)
	if res.Status != statusSkip || !strings.Contains(res.Detail, "ticktack review") {
		t.Fatalf("result = %+v, want skip pointing at ticktack review", res)
	}
}

func TestCheckStandardTasksReportsTheProjectCacheAge(t *testing.T) {
	env := newTestEnv(t, "", "")
	writeRepoPathsConfig(t, defaultIssueTOML)
	env.deps.Doctor.Now = func() time.Time { return time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC) }
	dbPath, err := env.deps.Doctor.DBPath()
	if err != nil {
		t.Fatal(err)
	}
	st, err := env.deps.Doctor.OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	cache, ok := st.(interface {
		ReplaceProjects(context.Context, []store.Project, time.Time) error
	})
	if !ok {
		t.Fatal("the store cannot cache projects")
	}
	fetched := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)
	if err := cache.ReplaceProjects(context.Background(), []store.Project{{Key: "A", Name: "Alpha"}, {Key: "B", Name: "Beta"}}, fetched); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	res := checkStandardTasks(context.Background(), env.deps.Doctor)
	if res.Status != statusOK || !strings.Contains(res.Detail, "2 projects") || !strings.Contains(res.Detail, "2h") {
		t.Fatalf("result = %+v, want ok with 2 projects refreshed 2h ago", res)
	}
}
