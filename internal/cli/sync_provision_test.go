package cli

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/ledger"
	"github.com/JtheGunner/ticktack/internal/render"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
	"github.com/JtheGunner/ticktack/internal/sink"
	"github.com/JtheGunner/ticktack/internal/store"
)

// provisioningSink books like recordingSink and can also create standard
// tasks; existing maps a project to the standard task that already exists.
type provisioningSink struct {
	*recordingSink
	existing map[string]string
	created  []string
}

func (p *provisioningSink) Projects(context.Context) ([]sink.ProjectRef, error) { return nil, nil }

func (p *provisioningSink) FindIssueByMarker(_ context.Context, project, _ string) (string, bool, error) {
	key, ok := p.existing[project]
	return key, ok, nil
}

func (p *provisioningSink) CreateIssue(_ context.Context, project, summary, description string) (string, error) {
	if summary != "Meetings / Admin" || !strings.Contains(description, ledger.DefaultIssueMarker) {
		return "", context.DeadlineExceeded
	}
	p.created = append(p.created, project)
	return project + "-1", nil
}

// standardTaskDeps seeds a day of repository time without a ticket and turns
// the global standard task GEN on.
func standardTaskDeps(t *testing.T) (syncDeps, *provisioningSink) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "ticktack.db")
	seed, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	ctx := context.Background()
	if err := seed.Append(ctx, event.Event{At: start, Source: "test", Kind: event.KindContext, Repo: "/work/app", Branch: "spike"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= 60; i++ {
		if err := seed.Append(ctx, event.Event{At: start.Add(time.Duration(i) * time.Minute), Source: "test", Kind: event.KindPresence}); err != nil {
			t.Fatal(err)
		}
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
	prov := &provisioningSink{recordingSink: &recordingSink{items: map[string]sink.WorkItem{}}, existing: map[string]string{}}
	return syncDeps{
		LoadConfig: func() (config.Config, string, error) {
			return config.Config{
				YouTrackURL: "https://example.invalid", Location: time.UTC,
				GapThreshold: 5 * time.Minute, MinBlockDuration: 10 * time.Minute, Rounding: 15 * time.Minute,
				DefaultIssue: &config.DefaultIssue{Project: "GEN", Summary: "Meetings / Admin"},
			}, "config.toml", nil
		},
		OpenStore: func() (*store.Store, error) { return store.Open(dbPath) },
		NewSink:   func(config.Config, string) (sink.TimeSink, error) { return prov, nil },
		Now:       func() time.Time { return time.Date(2026, 1, 5, 20, 0, 0, 0, time.UTC) },
	}, prov
}

func TestSyncCreatesTheStandardTaskAfterConfirmationAndBooksOnIt(t *testing.T) {
	deps, prov := standardTaskDeps(t)
	review := confirmDay(t, deps)
	if !strings.Contains(review, "GEN · standard task (new)") {
		t.Fatalf("review does not offer the standard task:\n%s", review)
	}

	out, _, err := execCommandWithInput(t, deps, "y\n", "sync", "--day", "2026-01-05")
	if err != nil {
		t.Fatalf("sync: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Create standard task in: GEN") {
		t.Errorf("sync did not ask before creating:\n%s", out)
	}
	if len(prov.created) != 1 || prov.created[0] != "GEN" {
		t.Fatalf("created = %v, want GEN once", prov.created)
	}
	if _, ok := prov.items["GEN-1/1"]; !ok {
		t.Errorf("time was not booked on GEN-1: %v", prov.items)
	}

	// A second run finds the task through the cache and creates nothing new.
	if _, _, err := execCommandWithInput(t, deps, "", "sync", "--day", "2026-01-05"); err != nil {
		t.Fatal(err)
	}
	if len(prov.created) != 1 {
		t.Errorf("second sync created again: %v", prov.created)
	}
}

func TestSyncDeclinedCreationBooksNothing(t *testing.T) {
	deps, prov := standardTaskDeps(t)
	confirmDay(t, deps)

	out, _, err := execCommandWithInput(t, deps, "n\n", "sync", "--day", "2026-01-05")
	if err != nil {
		t.Fatal(err)
	}
	if len(prov.created) != 0 || len(prov.items) != 0 {
		t.Errorf("declined creation still acted: created %v booked %v", prov.created, prov.items)
	}
	if !strings.Contains(out, "not created") {
		t.Errorf("sync does not say the standard task was not created:\n%s", out)
	}
}

func TestSyncWithoutAnswerDeclines(t *testing.T) {
	deps, prov := standardTaskDeps(t)
	confirmDay(t, deps)
	if _, _, err := execCommandWithInput(t, deps, "", "sync", "--day", "2026-01-05"); err != nil {
		t.Fatal(err)
	}
	if len(prov.created) != 0 || len(prov.items) != 0 {
		t.Errorf("an empty answer must decline: created %v booked %v", prov.created, prov.items)
	}
}

func TestSyncDryRunNamesTheTasksItWouldCreate(t *testing.T) {
	deps, prov := standardTaskDeps(t)
	confirmDay(t, deps)

	out, _, err := execCommandWithInput(t, deps, "y\n", "sync", "--day", "2026-01-05", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if len(prov.created) != 0 || len(prov.items) != 0 {
		t.Errorf("dry run acted: created %v booked %v", prov.created, prov.items)
	}
	if !strings.Contains(out, "Would create the standard task in: GEN") {
		t.Errorf("dry run does not name the task:\n%s", out)
	}
}

func TestSyncDryRunNamesTheTimeItWouldBookOnANewStandardTask(t *testing.T) {
	deps, prov := standardTaskDeps(t)
	confirmDay(t, deps)

	out, _, err := execCommandWithInput(t, deps, "", "sync", "--day", "2026-01-05", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "No time to book") {
		t.Errorf("dry run claims there is nothing to book although time waits for a new task:\n%s", out)
	}
	if !strings.Contains(out, "GEN: 1h would be booked once the standard task exists") {
		t.Errorf("dry run does not name the project and the time:\n%s", out)
	}
	if len(prov.created) != 0 || len(prov.items) != 0 {
		t.Errorf("dry run acted: created %v booked %v", prov.created, prov.items)
	}
}

func TestSyncDryRunStillSaysNothingToBookWhenNothingWaits(t *testing.T) {
	deps, prov := standardTaskDeps(t)
	prov.existing["GEN"] = "GEN-5"
	// Nothing confirmed for the day, so there is truly nothing to book.
	out, _, err := execCommandWithInput(t, deps, "", "sync", "--day", "2026-01-05", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "would be booked once") {
		t.Errorf("dry run announces time that does not exist:\n%s", out)
	}
}

func TestSyncUsesAnExistingStandardTaskWithoutAsking(t *testing.T) {
	deps, prov := standardTaskDeps(t)
	prov.existing["GEN"] = "GEN-5"
	confirmDay(t, deps)

	out, _, err := execCommandWithInput(t, deps, "", "sync", "--day", "2026-01-05")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "Create standard task in") || len(prov.created) != 0 {
		t.Errorf("an existing task must not be recreated:\n%s", out)
	}
	if _, ok := prov.items["GEN-5/1"]; !ok {
		t.Errorf("time was not booked on GEN-5: %v", prov.items)
	}
}

func TestProvisionOutputNeverEchoesControlCharactersOfProjectKeys(t *testing.T) {
	deps, prov := standardTaskDeps(t)
	st, err := deps.OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	cfg, _, _ := deps.LoadConfig()
	evil := "EVIL\x1b[2J\x07"
	items := []sessionizer.DayItem{{Ticket: evil + ":default", Type: "Development", Minutes: 30}}

	for name, in := range map[string]io.Reader{"declined": strings.NewReader("n\n"), "approved": strings.NewReader("y\n")} {
		out := &bytes.Buffer{}
		if _, _, err := provisionStandardTasks(context.Background(), out, in, render.Renderer{}, cfg, st, prov, items, false); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.ContainsAny(out.String(), "\x1b\x07") {
			t.Errorf("%s: output contains control characters: %q", name, out.String())
		}
	}

	out := &bytes.Buffer{}
	if _, _, err := provisionStandardTasks(context.Background(), out, nil, render.Renderer{}, cfg, st, prov, items, true); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(out.String(), "\x1b\x07") {
		t.Errorf("dry run: output contains control characters: %q", out.String())
	}
}

func TestSyncSkipsPlaceholdersWhenTheSinkCannotCreateTasks(t *testing.T) {
	deps, _ := standardTaskDeps(t)
	confirmDay(t, deps)
	plain := &recordingSink{items: map[string]sink.WorkItem{}}
	deps.NewSink = func(config.Config, string) (sink.TimeSink, error) { return plain, nil }

	out, _, err := execCommandWithInput(t, deps, "", "sync", "--day", "2026-01-05")
	if err != nil {
		t.Fatal(err)
	}
	if len(plain.items) != 0 {
		t.Errorf("a placeholder was booked: %v", plain.items)
	}
	if !strings.Contains(out, "cannot create standard tasks") {
		t.Errorf("sync does not explain the skipped time:\n%s", out)
	}
}
