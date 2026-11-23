package ledger

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/JtheGunner/ticktack/internal/sessionizer"
	"github.com/JtheGunner/ticktack/internal/store"
)

// textDescriber replaces the description of every item with a fixed text.
type textDescriber struct{ text string }

func (d *textDescriber) Describe(_ context.Context, _ sessionizer.DayItem) string { return d.text }

func withDescriber(repo Repository, d Describer) SyncOptions {
	o := reconciling(repo)
	o.Describer = d
	return o
}

func onlyItem(t *testing.T, s *fakeSink) (string, string) {
	t.Helper()
	if len(s.items) != 1 {
		t.Fatalf("sink holds %d items, want 1", len(s.items))
	}
	for id, it := range s.items {
		return id, it.item.Text
	}
	return "", ""
}

func TestSyncSkipsOnSameMinutesAndText(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	d := &textDescriber{text: "fix parser"}
	mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 60)}, s, withDescriber(repo, d))

	rep := mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 60)}, s, withDescriber(repo, d))

	if rep.Skipped != 1 || rep.Updated != 0 {
		t.Fatalf("report = %+v, want one skip", rep)
	}
}

func TestSyncUpdatesOnTextChangeWithSameMinutes(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 60)}, s, withDescriber(repo, &textDescriber{text: "fix parser"}))

	rep := mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 60)}, s, withDescriber(repo, &textDescriber{text: "fix parser; add tests"}))

	if rep.Updated != 1 || rep.Skipped != 0 {
		t.Fatalf("report = %+v, want one update", rep)
	}
	_, text := onlyItem(t, s)
	if text != "fix parser; add tests "+Marker(Key("ABC-1", "2026-01-05", "Development")) {
		t.Errorf("text = %q", text)
	}
}

func TestSyncUpdatesOnMinuteChangeWithSameText(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	d := &textDescriber{text: "fix parser"}
	mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 60)}, s, withDescriber(repo, d))

	rep := mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 75)}, s, withDescriber(repo, d))

	if rep.Updated != 1 {
		t.Fatalf("report = %+v, want one update", rep)
	}
}

func TestSyncDryRunPlansTextChange(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 60)}, s, withDescriber(repo, &textDescriber{text: "a"}))
	o := withDescriber(repo, &textDescriber{text: "b"})
	o.DryRun = true

	rep := mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 60)}, s, o)

	if rep.Planned != 1 {
		t.Fatalf("report = %+v, want one planned update", rep)
	}
	if _, text := onlyItem(t, s); !strings.HasPrefix(text, "a ") {
		t.Errorf("dry run changed the text to %q", text)
	}
}

func TestSyncWithoutDescriberKeepsTheItemDescription(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 60)}, s, reconciling(repo))
	if _, text := onlyItem(t, s); !strings.HasPrefix(text, "ABC-1 work ") {
		t.Errorf("text = %q, want the item's own description", text)
	}
}

func TestMarkerStaysAtTheEndOfLongDescriptions(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	long := strings.Repeat("x", 5*200)
	mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 60)}, s, withDescriber(repo, &textDescriber{text: long}))
	key := Key("ABC-1", "2026-01-05", "Development")

	_, text := onlyItem(t, s)
	if !strings.HasSuffix(text, " "+Marker(key)) {
		t.Fatalf("marker is not at the end: %q", text[len(text)-80:])
	}
	// A lost ledger is recovered through the marker.
	id, found, err := s.FindByMarker(context.Background(), "ABC-1", Marker(key))
	if err != nil || !found || id == "" {
		t.Errorf("FindByMarker = %q, %v, %v", id, found, err)
	}
}

func TestUndoRestoresThePreviousText(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	key := Key("ABC-1", "2026-01-05", "Development")
	mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 60)}, s, withDescriber(repo, &textDescriber{text: "first"}))
	mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 75)}, s, withDescriber(repo, &textDescriber{text: "second"}))

	res, err := Undo(context.Background(), s, repo, opts(repo).Now)
	if err != nil || res.Restored != 1 {
		t.Fatalf("undo = %+v, err = %v", res, err)
	}

	_, text := onlyItem(t, s)
	if text != "first "+Marker(key) {
		t.Errorf("text after undo = %q, want the first text", text)
	}
	row := repo.rows[key]
	if row.Minutes != 60 || row.ContentHash != contentHash(key, 60, text) {
		t.Errorf("ledger row after undo = %+v", row)
	}
	// The restored state is stable for the same day.
	rep := mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 60)}, s, withDescriber(repo, &textDescriber{text: "first"}))
	if rep.Skipped != 1 {
		t.Errorf("sync after undo = %+v, want a skip", rep)
	}
}

func TestUndoOfEntryWithoutPreviousTextKeepsTheOldBehaviour(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	key := Key("ABC-1", "2026-01-05", "Development")
	mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 60)}, s, reconciling(repo))
	mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 75)}, s, reconciling(repo))
	// An entry written before previous_text existed has none.
	repo.runs[2].Entries[0].PreviousText = ""

	if _, err := Undo(context.Background(), s, repo, opts(repo).Now); err != nil {
		t.Fatal(err)
	}

	_, text := onlyItem(t, s)
	if text != "ABC-1 work "+Marker(key) || repo.rows[key].Minutes != 60 {
		t.Errorf("text = %q, row = %+v", text, repo.rows[key])
	}
}

func TestUndoRecreatesDeletedItemWithItsText(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	key := Key("ABC-1", "2026-01-05", "Development")
	mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 60)}, s, withDescriber(repo, &textDescriber{text: "kept text"}))
	mustSync(t, nil, s, withDescriber(repo, &textDescriber{text: "other"}))

	if _, err := Undo(context.Background(), s, repo, opts(repo).Now); err != nil {
		t.Fatal(err)
	}

	if _, text := onlyItem(t, s); text != "kept text "+Marker(key) {
		t.Errorf("text = %q, want the deleted item's text", text)
	}
}

// TestCommitSubjectsLandOnlyInSyncEntries is the privacy check: the text that
// a describer produced (standing in for commit subjects) may be kept in the
// sync history for undo and nowhere else in the database.
func TestCommitSubjectsLandOnlyInSyncEntries(t *testing.T) {
	const secret = "SECRET-SUBJECT-4711"
	path := filepath.Join(t.TempDir(), "events.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s := newFakeSink()
	o := withDescriber(st, &textDescriber{text: secret})
	mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 60)}, s, o)
	// A second text, so the previous text column is filled as well.
	o = withDescriber(st, &textDescriber{text: secret + " again"})
	mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 75)}, s, o)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	tables := tableNames(t, db)
	if len(tables) == 0 {
		t.Fatal("no tables found")
	}
	var inSyncEntries bool
	for _, table := range tables {
		hits := rowsMentioning(t, db, table, secret)
		switch {
		case table == "sync_entries":
			inSyncEntries = hits > 0
		case hits > 0:
			t.Errorf("commit subject text found in table %q (%d rows)", table, hits)
		}
	}
	if !inSyncEntries {
		t.Error("booked text is missing from sync_entries, undo could not restore it")
	}
}

func tableNames(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	return names
}

// rowsMentioning counts the rows of table in which any column contains needle.
func rowsMentioning(t *testing.T, db *sql.DB, table, needle string) int {
	t.Helper()
	cols, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatal(err)
	}
	var conds []string
	for cols.Next() {
		var c string
		if err := cols.Scan(&c); err != nil {
			t.Fatal(err)
		}
		conds = append(conds, `CAST("`+c+`" AS TEXT) LIKE '%' || ? || '%'`)
	}
	_ = cols.Close()
	args := make([]any, len(conds))
	for i := range args {
		args[i] = needle
	}
	var n int
	q := `SELECT COUNT(*) FROM "` + table + `" WHERE ` + strings.Join(conds, " OR ")
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
