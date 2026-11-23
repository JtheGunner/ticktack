package retention

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
	"github.com/JtheGunner/ticktack/internal/store"
)

var (
	today = civil.Date{Year: 2026, Month: time.March, Day: 20}
	now   = time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)
)

func testConfig(retentionDays int) config.Config {
	return config.Config{
		Location: time.UTC, GapThreshold: 5 * time.Minute, MinBlockDuration: 10 * time.Minute,
		Rounding: 15 * time.Minute, DailyCap: 10 * time.Hour, RetentionDays: retentionDays,
	}
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "ticktack.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// seedDay stores one hour of presence from 09:00 on day. Each ticket owns an
// equal share of the hour, so two tickets give two items of 30 minutes.
func seedDay(t *testing.T, st *store.Store, day civil.Date, tickets ...string) {
	t.Helper()
	ctx := context.Background()
	start := time.Date(day.Year, day.Month, day.Day, 9, 0, 0, 0, time.UTC)
	share := 60 / len(tickets)
	for i, ticket := range tickets {
		e := event.Event{At: start.Add(time.Duration(i*share) * time.Minute), Source: "test", Kind: event.KindContext, Ticket: ticket}
		if err := st.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i <= 60; i++ {
		e := event.Event{At: start.Add(time.Duration(i) * time.Minute), Source: "test", Kind: event.KindPresence}
		if err := st.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
}

// confirm and book mark one item as reviewed and as booked.
func confirm(t *testing.T, st *store.Store, day civil.Date, issue string, minutes int) {
	t.Helper()
	c := store.Confirmation{Day: day.String(), Issue: issue, Type: sessionizer.DefaultWorkItemType, Minutes: minutes, ConfirmedAt: now}
	if err := st.Confirm(context.Background(), c); err != nil {
		t.Fatal(err)
	}
}

func book(t *testing.T, st *store.Store, day civil.Date, issue string, minutes int) {
	t.Helper()
	ctx := context.Background()
	runID, err := st.BeginSyncRun(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	row := store.LedgerRow{Issue: issue, Date: day.String(), Type: sessionizer.DefaultWorkItemType, WorkItemID: issue + "/1", Minutes: minutes, ContentHash: "h", BookedAt: now}
	entry := store.SyncEntry{Issue: issue, Date: day.String(), Type: row.Type, Action: "created", WorkItemID: row.WorkItemID, NewMinutes: minutes}
	if err := st.RecordBooking(ctx, runID, row, entry); err != nil {
		t.Fatal(err)
	}
}

func confirmAndBook(t *testing.T, st *store.Store, day civil.Date, issue string, minutes int) {
	t.Helper()
	confirm(t, st, day, issue, minutes)
	book(t, st, day, issue, minutes)
}

func eventsOf(t *testing.T, st *store.Store, day civil.Date) int {
	t.Helper()
	from := time.Date(day.Year, day.Month, day.Day, 0, 0, 0, 0, time.UTC)
	got, err := st.Events(context.Background(), from, from.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return len(got)
}

func TestExpiredIsMeasuredInLocalCalendarDays(t *testing.T) {
	tests := []struct {
		name      string
		day       civil.Date
		retention int
		want      bool
	}{
		{"off never expires", today.AddDays(-400), 0, false},
		{"today", today, 1, false},
		{"exactly the period old stays", today.AddDays(-30), 30, false},
		{"one day past the period goes", today.AddDays(-31), 30, true},
		{"one day with a short period", today.AddDays(-2), 1, true},
		{"yesterday with one day stays", today.AddDays(-1), 1, false},
		{"a future day never expires", today.AddDays(3), 1, false},
		{"across a month end", civil.Date{Year: 2026, Month: time.January, Day: 31}, 47, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Expired(tc.day, today, tc.retention); got != tc.want {
				t.Errorf("Expired(%s, %s, %d) = %v, want %v", tc.day, today, tc.retention, got, tc.want)
			}
		})
	}
}

func TestRunDeletesOnlyFullyBookedExpiredDays(t *testing.T) {
	old := today.AddDays(-40)
	tests := []struct {
		name      string
		retention int
		tickets   []string
		setup     func(t *testing.T, st *store.Store)
		wantGone  bool
	}{
		{"fully booked and expired", 30, nil, func(t *testing.T, st *store.Store) {
			confirmAndBook(t, st, old, "ABC-1", 60)
		}, true},
		{"two items both booked", 30, []string{"ABC-1", "ABC-2"}, func(t *testing.T, st *store.Store) {
			confirmAndBook(t, st, old, "ABC-1", 30)
			confirmAndBook(t, st, old, "ABC-2", 30)
		}, true},
		{"partially booked day", 30, []string{"ABC-1", "ABC-2"}, func(t *testing.T, st *store.Store) {
			confirmAndBook(t, st, old, "ABC-1", 30)
		}, false},
		{"partially confirmed day", 30, []string{"ABC-1", "ABC-2"}, func(t *testing.T, st *store.Store) {
			confirmAndBook(t, st, old, "ABC-1", 30)
			book(t, st, old, "ABC-2", 30)
		}, false},
		{"within the retention period", 60, nil, func(t *testing.T, st *store.Store) {
			confirmAndBook(t, st, old, "ABC-1", 60)
		}, false},
		{"not reviewed", 30, nil, func(t *testing.T, st *store.Store) {
			book(t, st, old, "ABC-1", 60)
		}, false},
		{"reviewed but not booked", 30, nil, func(t *testing.T, st *store.Store) {
			confirm(t, st, old, "ABC-1", 60)
		}, false},
		{"confirmed with other minutes", 30, nil, func(t *testing.T, st *store.Store) {
			confirm(t, st, old, "ABC-1", 45)
			book(t, st, old, "ABC-1", 45)
		}, false},
		{"booked with other minutes", 30, nil, func(t *testing.T, st *store.Store) {
			confirm(t, st, old, "ABC-1", 60)
			book(t, st, old, "ABC-1", 45)
		}, false},
		{"booking of time the day no longer has", 30, nil, func(t *testing.T, st *store.Store) {
			confirmAndBook(t, st, old, "ABC-1", 60)
			book(t, st, old, "ABC-9", 15)
		}, false},
		{"retention off", 0, nil, func(t *testing.T, st *store.Store) {
			confirmAndBook(t, st, old, "ABC-1", 60)
		}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openStore(t)
			tickets := tc.tickets
			if tickets == nil {
				tickets = []string{"ABC-1"}
			}
			seedDay(t, st, old, tickets...)
			tc.setup(t, st)
			before := eventsOf(t, st, old)

			report, err := Run(context.Background(), st, testConfig(tc.retention), now, false)
			if err != nil {
				t.Fatal(err)
			}
			after := eventsOf(t, st, old)
			if tc.wantGone {
				// Only the context event the next day starts from stays.
				if after != 1 {
					t.Errorf("%d events of the booked day remain, want only the context anchor", after)
				}
				if len(report.Days) == 0 || report.Events == 0 {
					t.Errorf("report = %+v, want cleaned days", report)
				}
				if _, found, _ := st.ArchivedDay(context.Background(), old.String()); !found {
					t.Error("the day is not recorded as archived")
				}
				return
			}
			if after != before || len(report.Days) != 0 {
				t.Errorf("events %d -> %d, report %+v: want the day untouched", before, after, report)
			}
			if _, found, _ := st.ArchivedDay(context.Background(), old.String()); found {
				t.Error("a day that must stay was archived")
			}
		})
	}
}

func TestRunKeepsOverridesLedgerAndOtherDays(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	old := today.AddDays(-40)
	recent := today.AddDays(-3)
	unbooked := today.AddDays(-35)
	seedDay(t, st, old, "ABC-1")
	seedDay(t, st, recent, "ABC-1")
	seedDay(t, st, unbooked, "ABC-1")
	confirmAndBook(t, st, old, "ABC-1", 60)
	confirmAndBook(t, st, recent, "ABC-1", 60)
	start := time.Date(old.Year, old.Month, old.Day, 9, 0, 0, 0, time.UTC)
	if err := st.AddOverride(ctx, event.Override{Start: start, End: start.Add(time.Hour), Ticket: "ABC-1"}); err != nil {
		t.Fatal(err)
	}
	unbookedBefore, recentBefore := eventsOf(t, st, unbooked), eventsOf(t, st, recent)

	report, err := Run(ctx, st, testConfig(30), now, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Days) != 1 || report.Days[0].Day != old {
		t.Fatalf("report = %+v, want only %s", report, old)
	}
	if got := eventsOf(t, st, unbooked); got != unbookedBefore {
		t.Errorf("unbooked day: %d events, want %d", got, unbookedBefore)
	}
	if got := eventsOf(t, st, recent); got != recentBefore {
		t.Errorf("recent day: %d events, want %d", got, recentBefore)
	}
	overrides, err := st.Overrides(ctx, start.Add(-time.Hour), start.Add(2*time.Hour))
	if err != nil || len(overrides) != 1 {
		t.Errorf("overrides = %d, %v, want 1", len(overrides), err)
	}
	rows, err := st.LedgerRowsForDate(ctx, old.String())
	if err != nil || len(rows) != 1 {
		t.Errorf("ledger rows of the cleaned day = %d, %v, want 1", len(rows), err)
	}
	if run, found, err := st.LastSyncRun(ctx); err != nil || !found || len(run.Entries) == 0 {
		t.Errorf("sync history lost: %+v, %v, %v", run, found, err)
	}
	if got := len(confirmationsOf(t, st, old)); got != 1 {
		t.Errorf("confirmations of the cleaned day = %d, want 1", got)
	}
}

func confirmationsOf(t *testing.T, st *store.Store, day civil.Date) []store.Confirmation {
	t.Helper()
	list, err := st.Confirmations(context.Background(), day.String())
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func TestRunDryRunDeletesNothingAndSecondRunIsIdempotent(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	old := today.AddDays(-40)
	seedDay(t, st, old, "ABC-1")
	confirmAndBook(t, st, old, "ABC-1", 60)
	before := eventsOf(t, st, old)

	dry, err := Run(ctx, st, testConfig(30), now, true)
	if err != nil {
		t.Fatal(err)
	}
	if !dry.DryRun || len(dry.Days) != 1 || dry.Events != before-1 {
		t.Errorf("dry run report = %+v, want 1 day and %d events", dry, before-1)
	}
	if got := eventsOf(t, st, old); got != before {
		t.Fatalf("dry run changed the events: %d -> %d", before, got)
	}
	if _, found, _ := st.ArchivedDay(ctx, old.String()); found {
		t.Fatal("dry run archived the day")
	}

	first, err := Run(ctx, st, testConfig(30), now, false)
	if err != nil {
		t.Fatal(err)
	}
	if first.Events != dry.Events {
		t.Errorf("deleted %d events, the dry run announced %d", first.Events, dry.Events)
	}
	second, err := Run(ctx, st, testConfig(30), now, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Days) != 0 || second.Events != 0 {
		t.Errorf("second run = %+v, want nothing to do", second)
	}
}

func TestRunDoesNotBringBackAnArchivedDayWhenItIsScannedAgain(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	old := today.AddDays(-40)
	seedDay(t, st, old, "ABC-1")
	confirmAndBook(t, st, old, "ABC-1", 60)
	if _, err := Run(ctx, st, testConfig(30), now, false); err != nil {
		t.Fatal(err)
	}
	remaining := eventsOf(t, st, old)
	at := time.Date(old.Year, old.Month, old.Day, 11, 0, 0, 0, time.UTC)
	if err := st.Append(ctx, event.Event{At: at, Source: "git", Kind: event.KindContext, Repo: "/r", Branch: "ABC-1", Key: "reflog-1"}); err != nil {
		t.Fatal(err)
	}
	if got := eventsOf(t, st, old); got != remaining {
		t.Errorf("an event came back into the archived day: %d -> %d", remaining, got)
	}
}

func TestRunKeepsTheContextTheFollowingDayStartsFrom(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	old := today.AddDays(-40)
	seedDay(t, st, old, "ABC-1")
	confirmAndBook(t, st, old, "ABC-1", 60)
	next := old.AddDays(1)
	nextStart := time.Date(next.Year, next.Month, next.Day, 0, 0, 0, 0, time.UTC)
	before, err := st.CarriedContext(ctx, nextStart, store.CarrySources{Manual: sessionizer.ManualSource, AppHint: sessionizer.TitleSource})
	if err != nil || len(before) == 0 {
		t.Fatalf("setup: carried context = %v, %v", before, err)
	}
	if _, err := Run(ctx, st, testConfig(30), now, false); err != nil {
		t.Fatal(err)
	}
	after, err := st.CarriedContext(ctx, nextStart, store.CarrySources{Manual: sessionizer.ManualSource, AppHint: sessionizer.TitleSource})
	if err != nil || len(after) != len(before) || after[0].Ticket != before[0].Ticket {
		t.Errorf("carried context changed: %v -> %v (%v)", before, after, err)
	}
}
