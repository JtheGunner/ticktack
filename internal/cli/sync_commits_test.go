package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/collector/git"
	"github.com/JtheGunner/ticktack/internal/commitsubject"
	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
)

const commitTestRepo = "/work/repo"

// mapReader serves commit subjects from memory.
type mapReader map[string]commitsubject.Commit

func (m mapReader) Read(_ context.Context, _, hash string) (commitsubject.Commit, error) {
	c, ok := m[hash]
	if !ok {
		return commitsubject.Commit{}, errors.New("unknown commit")
	}
	return c, nil
}

// seedCommit appends a scanner-style commit event to the seeded day.
func seedCommit(t *testing.T, deps syncDeps, hash string, at time.Time) {
	t.Helper()
	st, err := deps.OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	e := event.Event{
		At: at, Source: git.Source, Kind: event.KindContext, Repo: commitTestRepo, Branch: "feat/ABC-1", Ticket: "ABC-1",
		Key: fmt.Sprintf("git:%s:commit:%d:%s:feat/ABC-1", commitTestRepo, at.Unix(), hash),
	}
	if err := st.Append(context.Background(), e); err != nil {
		t.Fatal(err)
	}
}

func withCommitsConfig(deps syncDeps, on bool) syncDeps {
	load := deps.LoadConfig
	deps.LoadConfig = func() (config.Config, string, error) {
		cfg, path, err := load()
		cfg.DescriptionFromCommits = on
		return cfg, path, err
	}
	return deps
}

func TestSyncBooksCommitSubjectsAndKeepsTheMarkerAtTheEnd(t *testing.T) {
	deps, rec := syncTestDeps(t)
	start := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	h1, h2, h3 := strings.Repeat("1", 40), strings.Repeat("2", 40), strings.Repeat("3", 40)
	seedCommit(t, deps, h1, start.Add(10*time.Minute))
	seedCommit(t, deps, h2, start.Add(20*time.Minute))
	seedCommit(t, deps, h3, start.Add(30*time.Minute))
	deps.CommitReader = mapReader{
		h1: {Subject: "Add parser"},
		h2: {Subject: "Merge branch 'x'", Merge: true},
		h3: {Subject: "Fix\x1b[31m crash\nsecond line"},
	}
	deps = withCommitsConfig(deps, true)
	confirmDay(t, deps)

	if _, _, err := execCommand(t, deps, "sync", "--day", "2026-01-05"); err != nil {
		t.Fatal(err)
	}

	got := rec.items["ABC-1/1"].Text
	want := "Add parser; Fix [31m crash second line [ticktack:ABC-1:2026-01-05:Development]"
	if got != want {
		t.Errorf("text = %q, want %q", got, want)
	}
}

func TestSyncKeepsTheSummaryWhenTheSwitchIsOff(t *testing.T) {
	deps, rec := syncTestDeps(t)
	start := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	h := strings.Repeat("1", 40)
	seedCommit(t, deps, h, start.Add(10*time.Minute))
	deps.CommitReader = mapReader{h: {Subject: "Add parser"}}
	deps = withCommitsConfig(deps, false)
	confirmDay(t, deps)

	if _, _, err := execCommand(t, deps, "sync", "--day", "2026-01-05"); err != nil {
		t.Fatal(err)
	}

	got := rec.items["ABC-1/1"].Text
	if strings.Contains(got, "Add parser") || !strings.HasPrefix(got, "ABC-1 — ") {
		t.Errorf("text = %q, want the summary", got)
	}
}

func TestSyncKeepsTheSummaryWhenNoCommitIsReadable(t *testing.T) {
	deps, rec := syncTestDeps(t)
	start := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	seedCommit(t, deps, strings.Repeat("1", 40), start.Add(10*time.Minute))
	deps.CommitReader = mapReader{}
	deps = withCommitsConfig(deps, true)
	confirmDay(t, deps)

	if _, _, err := execCommand(t, deps, "sync", "--day", "2026-01-05"); err != nil {
		t.Fatal(err)
	}

	if got := rec.items["ABC-1/1"].Text; !strings.HasPrefix(got, "ABC-1 — ") {
		t.Errorf("text = %q, want the summary", got)
	}
}

func TestSyncUpdatesWhenANewCommitChangesTheTextAndUndoRestoresIt(t *testing.T) {
	deps, rec := syncTestDeps(t)
	start := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	h1, h2 := strings.Repeat("1", 40), strings.Repeat("2", 40)
	seedCommit(t, deps, h1, start.Add(10*time.Minute))
	deps.CommitReader = mapReader{h1: {Subject: "first"}, h2: {Subject: "second"}}
	deps = withCommitsConfig(deps, true)
	confirmDay(t, deps)
	if _, _, err := execCommand(t, deps, "sync", "--day", "2026-01-05"); err != nil {
		t.Fatal(err)
	}
	marker := " [ticktack:ABC-1:2026-01-05:Development]"

	out, _, err := execCommand(t, deps, "sync", "--day", "2026-01-05")
	if err != nil || !strings.Contains(out, "Skipped 1") {
		t.Fatalf("unchanged day: %q err=%v", out, err)
	}

	seedCommit(t, deps, h2, start.Add(20*time.Minute))
	out, _, err = execCommand(t, deps, "sync", "--day", "2026-01-05")
	if err != nil || !strings.Contains(out, "updated 1") {
		t.Fatalf("new commit: %q err=%v", out, err)
	}
	if got := rec.items["ABC-1/1"].Text; got != "first; second"+marker {
		t.Errorf("text after update = %q", got)
	}

	if _, _, err := execCommand(t, deps, "undo"); err != nil {
		t.Fatal(err)
	}
	if got := rec.items["ABC-1/1"].Text; got != "first"+marker {
		t.Errorf("text after undo = %q, want the earlier text", got)
	}
}
