package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/sink"
	"github.com/JtheGunner/ticktack/internal/store"
)

type recordingSink struct {
	calls []string
	items map[string]sink.WorkItem
}

func (r *recordingSink) Create(_ context.Context, w sink.WorkItem) (string, error) {
	r.calls = append(r.calls, "create")
	id := w.Issue + "/1"
	r.items[id] = w
	return id, nil
}

func (r *recordingSink) Update(_ context.Context, id string, w sink.WorkItem) error {
	r.calls = append(r.calls, "update")
	r.items[id] = w
	return nil
}

func (r *recordingSink) Delete(_ context.Context, id string) error {
	r.calls = append(r.calls, "delete")
	delete(r.items, id)
	return nil
}

func (r *recordingSink) FindByMarker(context.Context, string, string) (string, bool, error) {
	r.calls = append(r.calls, "find")
	return "", false, nil
}

func syncTestDeps(t *testing.T) (syncDeps, *recordingSink) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "ticktack.db")
	seed, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	ctx := context.Background()
	if err := seed.Append(ctx, event.Event{At: start, Source: "test", Kind: event.KindContext, Ticket: "ABC-1"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= 60; i++ {
		e := event.Event{At: start.Add(time.Duration(i) * time.Minute), Source: "test", Kind: event.KindPresence}
		if err := seed.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
	rec := &recordingSink{items: map[string]sink.WorkItem{}}
	return syncDeps{
		LoadConfig: func() (config.Config, string, error) {
			return config.Config{YouTrackURL: "https://example.invalid", Location: time.UTC, GapThreshold: 5 * time.Minute, MinBlockDuration: 10 * time.Minute, Rounding: 15 * time.Minute}, "config.toml", nil
		},
		OpenStore: func() (*store.Store, error) { return store.Open(dbPath) },
		NewSink:   func(config.Config, string) (sink.TimeSink, error) { return rec, nil },
		Now:       func() time.Time { return time.Date(2026, 1, 5, 20, 0, 0, 0, time.UTC) },
	}, rec
}

func execCommand(t *testing.T, deps syncDeps, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	return execCommandWithInput(t, deps, "", args...)
}

func execCommandWithInput(t *testing.T, deps syncDeps, input string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := NewRootCommand()
	root.SetIn(strings.NewReader(input))
	for _, c := range root.Commands() {
		switch c.Name() {
		case "sync":
			root.RemoveCommand(c)
			root.AddCommand(newSyncCommandWith(deps))
		case "undo":
			root.RemoveCommand(c)
			root.AddCommand(newUndoCommandWith(deps))
		}
	}
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)
	err = root.Execute()
	return out.String(), errOut.String(), err
}

// confirmDay confirms every attributed item of the seeded day through the
// review command, as a user would.
func confirmDay(t *testing.T, deps syncDeps, extra ...string) string {
	t.Helper()
	st, err := deps.OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, _ := deps.LoadConfig()
	input := strings.Join(append(extra, "y", "q"), "\n") + "\n"
	out := &bytes.Buffer{}
	rd := reviewDeps{
		OpenStore:  func() (reviewStore, error) { return st, nil },
		LoadConfig: func() (config.Config, error) { return cfg, nil },
		Now:        deps.Now,
		In:         strings.NewReader(input),
		Out:        out,
	}
	if err := runReview(context.Background(), rd, "2026-01-05"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestSyncBooksNothingWithoutConfirmation(t *testing.T) {
	deps, rec := syncTestDeps(t)
	for _, args := range [][]string{{"sync", "--day", "2026-01-05"}, {"sync", "--day", "2026-01-05", "--dry-run"}} {
		out, _, err := execCommand(t, deps, args...)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "ticktack review") {
			t.Fatalf("missing review hint:\n%s", out)
		}
	}
	if len(rec.calls) != 0 || len(rec.items) != 0 {
		t.Fatalf("unconfirmed day touched the sink: %v", rec.calls)
	}
}

func TestSyncRejectsRemovedUnreviewedFlag(t *testing.T) {
	deps, _ := syncTestDeps(t)
	if _, _, err := execCommand(t, deps, "sync", "--unreviewed"); err == nil {
		t.Fatal("--unreviewed must not exist any more")
	}
}

func TestSyncDryRunPlansConfirmedItems(t *testing.T) {
	deps, rec := syncTestDeps(t)
	confirmDay(t, deps)
	out, _, err := execCommand(t, deps, "sync", "--day", "2026-01-05", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ABC-1:2026-01-05:Development") || !strings.Contains(out, "dry-run-would-create") {
		t.Fatalf("plan missing:\n%s", out)
	}
	for _, c := range rec.calls {
		if c != "find" {
			t.Fatalf("mutating call in dry run: %v", rec.calls)
		}
	}
}

func TestSyncBooksConfirmedAndUndoReverts(t *testing.T) {
	deps, rec := syncTestDeps(t)
	confirmDay(t, deps)
	out, _, err := execCommand(t, deps, "sync", "--day", "2026-01-05")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Created 1") || len(rec.items) != 1 {
		t.Fatalf("stdout:\n%s\nitems=%d", out, len(rec.items))
	}
	out, _, err = execCommand(t, deps, "undo")
	if err != nil || !strings.Contains(out, "Deleted 1") || len(rec.items) != 0 {
		t.Fatalf("undo: %q err=%v items=%d", out, err, len(rec.items))
	}
	out, _, err = execCommand(t, deps, "undo")
	if err != nil || !strings.Contains(out, "Nothing to undo") {
		t.Fatalf("second undo: %q err=%v", out, err)
	}
}

func TestSyncCapBlockedUntilAcknowledged(t *testing.T) {
	deps, rec := syncTestDeps(t)
	inner := deps.LoadConfig
	deps.LoadConfig = func() (config.Config, string, error) {
		cfg, path, err := inner()
		cfg.DailyCap = 30 * time.Minute
		return cfg, path, err
	}
	confirmDay(t, deps)
	_, _, err := execCommand(t, deps, "sync", "--day", "2026-01-05")
	if err == nil || !strings.Contains(err.Error(), "daily cap") || len(rec.calls) != 0 {
		t.Fatalf("err=%v calls=%v", err, rec.calls)
	}
	confirmDay(t, deps, "a")
	if _, _, err := execCommand(t, deps, "sync", "--day", "2026-01-05"); err != nil || len(rec.items) != 1 {
		t.Fatalf("after ack err=%v items=%d", err, len(rec.items))
	}
}

func TestSyncRejectsInvalidDay(t *testing.T) {
	deps, _ := syncTestDeps(t)
	if _, _, err := execCommand(t, deps, "sync", "--day", "05.01.2026"); err == nil {
		t.Fatal("expected error for invalid day")
	}
}

func TestNewYouTrackSinkErrors(t *testing.T) {
	keyring.MockInit()
	if _, err := newYouTrackSink(config.Config{}, "config.toml"); err == nil || !strings.Contains(err.Error(), "youtrack_url") {
		t.Fatalf("missing URL error = %v", err)
	}
	_, err := newYouTrackSink(config.Config{YouTrackURL: "https://example.invalid"}, "config.toml")
	if err == nil || !strings.Contains(err.Error(), "ticktack init") {
		t.Fatalf("missing token error = %v", err)
	}
}

// overrideSeededHour stores a user correction that covers the seeded hour.
func overrideSeededHour(t *testing.T, deps syncDeps, ticket string, discard bool) {
	t.Helper()
	st, err := deps.OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	start := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	o := event.Override{Start: start, End: start.Add(61 * time.Minute), Ticket: ticket, Discard: discard}
	if err := st.AddOverride(context.Background(), o); err != nil {
		t.Fatal(err)
	}
}

// bookSeededDay confirms and books the seeded hour of ABC-1.
func bookSeededDay(t *testing.T, deps syncDeps, rec *recordingSink) {
	t.Helper()
	confirmDay(t, deps)
	if _, _, err := execCommand(t, deps, "sync", "--day", "2026-01-05"); err != nil {
		t.Fatal(err)
	}
	if got := rec.items["ABC-1/1"].Minutes; got != 60 || len(rec.items) != 1 {
		t.Fatalf("seeded booking = %v", rec.items)
	}
}

func TestSyncDeletesBookingOfReassignedBlockAndUndoRestoresIt(t *testing.T) {
	deps, rec := syncTestDeps(t)
	bookSeededDay(t, deps, rec)
	overrideSeededHour(t, deps, "ABC-2", false)
	confirmDay(t, deps)

	out, _, err := execCommand(t, deps, "sync", "--day", "2026-01-05")
	if err != nil {
		t.Fatal(err)
	}
	if got := rec.items["ABC-2/1"].Minutes; got != 60 || len(rec.items) != 1 {
		t.Fatalf("items after reassign = %v\n%s", rec.items, out)
	}
	if !strings.Contains(out, "ABC-1:2026-01-05:Development") || !strings.Contains(out, "deleted 1") {
		t.Fatalf("deletion not reported:\n%s", out)
	}

	out, _, err = execCommand(t, deps, "undo")
	if err != nil || !strings.Contains(out, "re-created 1") {
		t.Fatalf("undo: %q err=%v", out, err)
	}
	if got := rec.items["ABC-1/1"].Minutes; got != 60 || len(rec.items) != 1 {
		t.Fatalf("items after undo = %v", rec.items)
	}
}

func TestSyncDeletesBookingOfDiscardedDay(t *testing.T) {
	deps, rec := syncTestDeps(t)
	bookSeededDay(t, deps, rec)
	overrideSeededHour(t, deps, "", true)

	rec.calls = nil
	out, _, err := execCommand(t, deps, "sync", "--day", "2026-01-05", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ABC-1:2026-01-05:Development") || !strings.Contains(out, "dry-run-would-delete") {
		t.Fatalf("plan missing the deletion:\n%s", out)
	}
	for _, c := range rec.calls {
		if c != "find" {
			t.Fatalf("mutating call in dry run: %v", rec.calls)
		}
	}
	if len(rec.items) != 1 {
		t.Fatalf("dry run changed the sink: %v", rec.items)
	}

	out, _, err = execCommand(t, deps, "sync", "--day", "2026-01-05")
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.items) != 0 || !strings.Contains(out, "deleted 1") {
		t.Fatalf("items = %v\n%s", rec.items, out)
	}
	// A second run finds nothing left to reconcile.
	rec.calls = nil
	if _, _, err := execCommand(t, deps, "sync", "--day", "2026-01-05"); err != nil || len(rec.calls) != 0 {
		t.Fatalf("second run: err=%v calls=%v", err, rec.calls)
	}
}

func TestSyncKeepsBookingOfItemAwaitingConfirmation(t *testing.T) {
	deps, rec := syncTestDeps(t)
	bookSeededDay(t, deps, rec)
	// The ticket gets more time after the booking, so its confirmation is stale.
	st, err := deps.OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	for i := 61; i <= 90; i++ {
		e := event.Event{At: time.Date(2026, 1, 5, 9, i, 0, 0, time.UTC), Source: "test", Kind: event.KindPresence}
		if err := st.Append(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	rec.calls = nil
	out, _, err := execCommand(t, deps, "sync", "--day", "2026-01-05")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ticktack review") || len(rec.calls) != 0 {
		t.Fatalf("calls=%v\n%s", rec.calls, out)
	}
	if got := rec.items["ABC-1/1"].Minutes; got != 60 || len(rec.items) != 1 {
		t.Fatalf("booking of an item that still exists was touched: %v", rec.items)
	}
}

func TestSyncDeletesBookingOfItemRoundedToZero(t *testing.T) {
	deps, rec := syncTestDeps(t)
	bookSeededDay(t, deps, rec)
	// With a coarser rounding step the seeded hour rounds to zero minutes.
	load := deps.LoadConfig
	deps.LoadConfig = func() (config.Config, string, error) {
		cfg, path, err := load()
		cfg.Rounding = 4 * time.Hour
		return cfg, path, err
	}
	out, _, err := execCommand(t, deps, "sync", "--day", "2026-01-05")
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.items) != 0 || !strings.Contains(out, "deleted 1") {
		t.Fatalf("items = %v\n%s", rec.items, out)
	}
}
