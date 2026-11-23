package store

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"sort"
	"testing"
)

func TestHoldsRoundTrip(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	for _, d := range []string{"2026-01-05", "2026-01-03", "2026-01-05"} {
		if err := s.HoldDay(ctx, d, base); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.HeldDays(ctx)
	if err != nil || len(got) != 2 || got[0] != "2026-01-03" || got[1] != "2026-01-05" {
		t.Fatalf("held = %v, err %v", got, err)
	}
	if err := s.ReleaseHold(ctx, "2026-01-03"); err != nil {
		t.Fatal(err)
	}
	if err := s.ReleaseHold(ctx, "2026-02-01"); err != nil {
		t.Fatalf("releasing a day without a hold: %v", err)
	}
	if got, _ := s.HeldDays(ctx); len(got) != 1 || got[0] != "2026-01-05" {
		t.Fatalf("held after release = %v", got)
	}
}

// A database that predates the holds table keeps its rows and gets an empty
// table when it is opened.
func TestMigrationAddsHoldsToAnExistingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_migrations (name TEXT PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if e.Name() < "0011" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
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
	if _, err := db.Exec(`INSERT INTO confirmations (day, issue, type, minutes, confirmed_at) VALUES ('2026-01-05', 'ABC-1', 'Development', 60, 0)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()
	if days, err := s.ConfirmedDays(ctx); err != nil || len(days) != 1 {
		t.Fatalf("existing rows lost: %v %v", days, err)
	}
	if held, err := s.HeldDays(ctx); err != nil || len(held) != 0 {
		t.Fatalf("held = %v, err %v", held, err)
	}
	if err := s.HoldDay(ctx, "2026-01-05", base); err != nil {
		t.Fatal(err)
	}
}
