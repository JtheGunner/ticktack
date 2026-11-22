package store

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"
)

func openTemp(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ticktack.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func TestOpenCreatesDatabaseWithOwnerOnlyMode(t *testing.T) {
	_, path := openTemp(t)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("new database mode = %v, want 0600", got)
	}
}

func TestOpenNarrowsExistingDatabaseMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ticktack.db")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("existing database mode = %v, want 0600", got)
	}
}

var base = time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)

func ev(offset time.Duration) event.Event {
	return event.Event{At: base.Add(offset), Source: "git", Kind: event.KindContext, Repo: "r", Branch: "b", Ticket: "TICKT-1", App: "term", IdleSeconds: 3}
}

func TestAppendIsOrderedByTime(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	for _, off := range []time.Duration{3 * time.Minute, time.Minute, 2 * time.Minute} {
		if err := s.Append(ctx, ev(off)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Events(ctx, base, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d events", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].At.Before(got[i-1].At) {
			t.Fatalf("events not ordered: %v", got)
		}
	}
	want := ev(time.Minute)
	want.ID = got[0].ID
	if got[0].ID == 0 || !got[0].At.Equal(want.At) {
		t.Fatalf("unexpected first event: %+v", got[0])
	}
	got[0].At, want.At = time.Time{}, time.Time{}
	if !reflect.DeepEqual(got[0], want) {
		t.Fatalf("round trip mismatch: %+v vs %+v", got[0], want)
	}
}

func TestEventsRangeIsHalfOpen(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	for _, off := range []time.Duration{0, time.Minute, 2 * time.Minute} {
		if err := s.Append(ctx, ev(off)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Events(ctx, base, base.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got[0].At.Equal(base) || !got[1].At.Equal(base.Add(time.Minute)) {
		t.Fatalf("want [from,to), got %+v", got)
	}
}

func TestAppendIgnoresDuplicateKey(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	keyed := ev(0)
	keyed.Key = "abc"
	for i := 0; i < 2; i++ {
		if err := s.Append(ctx, keyed); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	// Empty keys are never deduplicated.
	for i := 0; i < 2; i++ {
		if err := s.Append(ctx, ev(time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Events(ctx, base, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 events, got %d", len(got))
	}
}

func TestEventsAreImmutable(t *testing.T) {
	typ := reflect.TypeOf(&Store{})
	allowed := map[string]bool{
		"Append": true, "Events": true, "AddOverride": true, "Overrides": true,
		"AppendBatch": true,
		"Purge":       true, "Close": true, "InTx": true, "CheckWritable": true,
		// CarriedContext only reads the events table.
		"CarriedContext": true,
		// Ledger and sync history methods never touch the events table.
		"LedgerGet": true, "BeginSyncRun": true, "RecordBooking": true,
		"LastSyncRun": true, "RevertEntry": true,
		"LedgerRowsForDate": true, "RecordDeletion": true,
		// Overrides are corrections in their own table; a review session takes
		// its own last one back.
		"AddOverrideID": true, "DeleteOverride": true,
		// Review state (confirmations and cap acknowledgements) lives in its
		// own tables and never touches events.
		"Confirm": true, "Confirmations": true,
		"AcknowledgeCap": true, "CapAcknowledgementFor": true,
		// Learned rules are visible, editable rows in their own table; the
		// map command edits them. They never touch events.
		"AddLearnedRule": true, "LearnedRules": true,
		"UpdateLearnedRule": true, "DeleteLearnedRule": true,
		// The review log (track record for auto mode) and the digests of auto
		// runs live in their own tables and never touch events.
		"SaveReviewRecord": true, "ReviewRecords": true,
		"AddDigest": true, "LastDigest": true,
		"RecordRuleChange": true, "RuleChanges": true,
		// The project cache, standard task keys and consent live in their own
		// tables; renaming a confirmation only edits review state.
		"ReplaceProjects": true, "Projects": true,
		"DefaultTasks": true, "SetDefaultTask": true,
		"ConsentedProjects": true, "SetConsent": true,
		"RenameConfirmationIssue": true,
		// ArchiveDay is the retention cleanup: the one deliberate event
		// deletion besides Purge. It is scoped to a single local day, keeps the
		// carried-context anchors and records the day in archived_days in the
		// same transaction. The other two only read.
		"ArchiveDay": true, "ArchivedDay": true, "ArchivedDays": true,
		"EarliestEventAt": true,
	}
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		if !allowed[name] {
			t.Errorf("unexpected exported method %q: events must stay immutable", name)
		}
	}
}

func TestMigrationsAreIdempotent(t *testing.T) {
	s, path := openTemp(t)
	ctx := context.Background()
	if err := s.Append(ctx, ev(0)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = s2.Close() }()
	got, err := s2.Events(ctx, base, base.Add(time.Hour))
	if err != nil || len(got) != 1 {
		t.Fatalf("data lost after reopen: %v %v", got, err)
	}
	var applied int
	if err := s2.root.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&applied); err != nil {
		t.Fatal(err)
	}
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	if applied != len(entries) {
		t.Fatalf("applied %d migrations, have %d files", applied, len(entries))
	}
}

func TestPurgeRemovesEventsOverridesAndLedger(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	if err := s.Append(ctx, ev(0)); err != nil {
		t.Fatal(err)
	}
	if err := s.AddOverride(ctx, event.Override{Start: base, End: base.Add(time.Hour), Ticket: "TICKT-2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.root.Exec(`INSERT INTO ledger (issue, date, type, work_item_id, minutes, content_hash, booked_at)
		VALUES ('TICKT-1','2026-01-05','Development','1-1',30,'h',1)`); err != nil {
		t.Fatal(err)
	}
	runID, err := s.BeginSyncRun(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordBooking(ctx, runID,
		LedgerRow{Issue: "TICKT-1", Date: "2026-01-06", Type: "Development", WorkItemID: "1-2", Minutes: 15, ContentHash: "h", BookedAt: base},
		SyncEntry{Issue: "TICKT-1", Date: "2026-01-06", Type: "Development", Action: "created", WorkItemID: "1-2", NewMinutes: 15}); err != nil {
		t.Fatal(err)
	}
	if err := s.Confirm(ctx, Confirmation{Day: "2026-01-06", Issue: "TICKT-1", Type: "Development", Minutes: 15, ConfirmedAt: base}); err != nil {
		t.Fatal(err)
	}
	if err := s.AcknowledgeCap(ctx, CapAcknowledgement{Day: "2026-01-06", TotalMinutes: 700, AcknowledgedAt: base}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddLearnedRule(ctx, LearnedRule{Repo: "/work/app", Ticket: "TICKT-1", CreatedAt: base}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveReviewRecord(ctx, ReviewRecord{Day: "2026-01-06", SessionStart: base, FinishedAt: base, Confirmed: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddDigest(ctx, Digest{Day: "2026-01-06", CreatedAt: base, Text: "digest"}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordRuleChange(ctx, RuleChange{ChangedAt: base, Action: RuleAdded}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceProjects(ctx, []Project{{Key: "TICKT", Name: "ticktack"}}, base); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDefaultTask(ctx, "TICKT", "TICKT-9"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetConsent(ctx, "TICKT", base); err != nil {
		t.Fatal(err)
	}

	archiveStart := base.AddDate(0, 0, -5)
	if _, err := s.ArchiveDay(ctx, ArchiveRequest{Day: "2026-01-01", From: archiveStart, To: archiveStart.Add(24 * time.Hour), ArchivedAt: base}); err != nil {
		t.Fatal(err)
	}

	// The tables come from the schema itself, so a table added by a later
	// migration fails here until it is seeded above and emptied by Purge.
	tables := userTables(t, s)
	if len(tables) == 0 {
		t.Fatal("the schema lists no data tables")
	}
	for _, table := range tables {
		if n := rowCount(t, s, table); n == 0 {
			t.Errorf("%s has no rows before purge: seed it in this test", table)
		}
	}
	migrationsBefore := rowCount(t, s, migrationsTable)

	if err := s.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		if n := rowCount(t, s, table); n != 0 {
			t.Errorf("%s has %d rows after purge", table, n)
		}
	}
	if got := rowCount(t, s, migrationsTable); got != migrationsBefore || got == 0 {
		t.Errorf("%s has %d rows after purge, want %d", migrationsTable, got, migrationsBefore)
	}
}

// A purged database opens again and accepts new data without re-running the
// migrations.
func TestPurgedDatabaseReopens(t *testing.T) {
	s, path := openTemp(t)
	ctx := context.Background()
	if err := s.Append(ctx, ev(0)); err != nil {
		t.Fatal(err)
	}
	if err := s.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen after purge: %v", err)
	}
	defer func() { _ = reopened.Close() }()
	if err := reopened.Append(ctx, ev(0)); err != nil {
		t.Fatalf("append after purge: %v", err)
	}
	got, err := reopened.Events(ctx, base, base.Add(time.Hour))
	if err != nil || len(got) != 1 {
		t.Fatalf("events after purge and reopen = %v, err %v", got, err)
	}
}

// userTables lists every table of the schema that holds user data: all but
// SQLite's own tables and the migration bookkeeping.
func userTables(t *testing.T, s *Store) []string {
	t.Helper()
	rows, err := s.root.Query(`SELECT name FROM sqlite_master WHERE type = 'table'`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		if name == migrationsTable || strings.HasPrefix(name, "sqlite_") {
			continue
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return tables
}

func rowCount(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	if err := s.root.QueryRow(`SELECT COUNT(*) FROM "` + table + `"`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestOverridesRangeAndRoundTrip(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	in := event.Override{Start: base, End: base.Add(time.Hour), Ticket: "TICKT-2"}
	disc := event.Override{Start: base.Add(5 * time.Hour), End: base.Add(6 * time.Hour), Discard: true}
	for _, o := range []event.Override{in, disc} {
		if err := s.AddOverride(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Overrides(ctx, base, base.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Ticket != "TICKT-2" || !got[0].Start.Equal(in.Start) || !got[0].End.Equal(in.End) || got[0].Discard {
		t.Fatalf("got %+v", got)
	}
	got, err = s.Overrides(ctx, base, base.Add(24*time.Hour))
	if err != nil || len(got) != 2 || !got[1].Discard {
		t.Fatalf("got %+v err %v", got, err)
	}
}

func TestNoWindowTitleColumn(t *testing.T) {
	s, _ := openTemp(t)
	rows, err := s.root.Query("SELECT name FROM pragma_table_info('events')")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	n := 0
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		n++
		l := strings.ToLower(name)
		if strings.Contains(l, "title") || strings.Contains(l, "text") {
			t.Errorf("forbidden column %q on events", name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("events table has no columns")
	}
}

func TestAppendStoresGapEvent(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	gap := event.Event{At: base, Source: "presence", Kind: event.KindGap, IdleSeconds: 3600}
	if err := s.Append(ctx, gap); err != nil {
		t.Fatal(err)
	}
	got, err := s.Events(ctx, base, base.Add(time.Hour))
	if err != nil || len(got) != 1 {
		t.Fatalf("got %+v err %v", got, err)
	}
	if got[0].Kind != event.KindGap || got[0].IdleSeconds != 3600 {
		t.Fatalf("unexpected gap event %+v", got[0])
	}
}
