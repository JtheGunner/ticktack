package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"
)

// A database written before the kind column existed keeps its overrides: rows
// that dropped time become discards, every other row a reassign.
func TestMigrationAddsOverrideKindToExistingRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_migrations (name TEXT PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"0001_init.sql", "0002_sync_history.sql", "0003_review.sql", "0004_learned_rules.sql",
		"0005_auto_mode.sql", "0006_default_tasks.sql", "0007_rule_changes.sql",
	} {
		body, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations (name, applied_at) VALUES (?, 0)`, name); err != nil {
			t.Fatal(err)
		}
	}
	start := base.UnixNano()
	for _, row := range []struct {
		ticket  string
		discard int
	}{{"ABC-1", 0}, {"", 1}} {
		if _, err := db.Exec(`INSERT INTO overrides (start, "end", ticket, discard) VALUES (?, ?, ?, ?)`,
			start, start+int64(time.Hour), row.ticket, row.discard); err != nil {
			t.Fatal(err)
		}
		start += int64(2 * time.Hour)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()
	got, err := s.Overrides(context.Background(), base, base.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Kind != event.OverrideReassign || got[0].Ticket != "ABC-1" || got[1].Kind != event.OverrideDiscard || !got[1].Discard {
		t.Fatalf("overrides = %+v, want a reassign and a discard", got)
	}
}

func TestOverrideKindsRoundTripAndDelete(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	add := []event.Override{
		{Start: base, End: base.Add(time.Hour), Ticket: "A-1", Kind: event.OverrideFill},
		{Start: base.Add(time.Hour), End: base.Add(2 * time.Hour), Kind: event.OverrideBreak},
		{Start: base.Add(2 * time.Hour), End: base.Add(3 * time.Hour), Kind: event.OverrideDiscard},
		{Start: base.Add(3 * time.Hour), End: base.Add(4 * time.Hour), Discard: true},
		{Start: base.Add(4 * time.Hour), End: base.Add(5 * time.Hour), Ticket: "B-2"},
	}
	var ids []int64
	for _, o := range add {
		id, err := s.AddOverrideID(ctx, o)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	want := []event.OverrideKind{event.OverrideFill, event.OverrideBreak, event.OverrideDiscard, event.OverrideDiscard, event.OverrideReassign}
	wantDrops := []bool{false, true, true, true, false}
	got, err := s.Overrides(ctx, base, base.Add(24*time.Hour))
	if err != nil || len(got) != len(want) {
		t.Fatalf("overrides = %+v err=%v", got, err)
	}
	for i := range want {
		if got[i].Kind != want[i] || got[i].Discard != wantDrops[i] || got[i].ID != ids[i] {
			t.Errorf("override %d = %+v, want kind %s drops %v id %d", i, got[i], want[i], wantDrops[i], ids[i])
		}
	}
	if err := s.DeleteOverride(ctx, ids[4]); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Overrides(ctx, base, base.Add(24*time.Hour)); len(got) != 4 {
		t.Errorf("%d overrides after delete, want 4", len(got))
	}
	if err := s.DeleteOverride(ctx, ids[4]); err == nil {
		t.Error("deleting a missing override must fail")
	}
}
