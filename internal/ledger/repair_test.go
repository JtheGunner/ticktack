package ledger

import (
	"context"
	"errors"
	"testing"

	"github.com/JtheGunner/ticktack/internal/sessionizer"
	"github.com/JtheGunner/ticktack/internal/sink"
	"github.com/JtheGunner/ticktack/internal/youtrack"
)

const repairKey = "ABC-1:2026-01-05:Development"

// bookedThenDeleted books ABC-1 with 30 minutes and then removes the work item
// behind the ledger's back, as a user deleting it in the booking target does.
// It returns the id the ledger still points to.
func bookedThenDeleted(t *testing.T, s *fakeSink, repo *fakeRepo) string {
	t.Helper()
	mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 30)}, s, opts(repo))
	id := repo.rows[repairKey].WorkItemID
	if _, ok := s.items[id]; !ok {
		t.Fatalf("setup: no work item %q in the sink", id)
	}
	delete(s.items, id)
	s.calls = nil
	return id
}

// adoptable stores a work item that carries the marker of ABC-1 under an id
// the ledger does not know.
func adoptable(s *fakeSink, foreign bool) string {
	const id = "ABC-1/77"
	s.items[id] = fakeItem{foreign: foreign, item: sink.WorkItem{
		Issue: "ABC-1", Date: day, Minutes: 5, Type: "Development", Text: "old " + Marker(repairKey),
	}}
	return id
}

func TestSyncRepairsWorkItemDeletedInSink(t *testing.T) {
	tests := []struct {
		name    string
		minutes int
		// adopt adds an item with the marker under another id before the sync.
		adopt        bool
		foreign      bool
		wantMutating []string
		wantEntry    string
	}{
		{name: "unchanged minutes: created again", minutes: 30,
			wantMutating: []string{"create:ABC-1"}, wantEntry: entryActionCreated},
		{name: "changed minutes: created again", minutes: 45,
			wantMutating: []string{"update:ABC-1/1", "create:ABC-1"}, wantEntry: entryActionCreated},
		{name: "unchanged minutes: marker item adopted", minutes: 30, adopt: true,
			wantMutating: []string{"update:ABC-1/77"}, wantEntry: entryActionRecovered},
		{name: "changed minutes: marker item adopted", minutes: 45, adopt: true,
			wantMutating: []string{"update:ABC-1/1", "update:ABC-1/77"}, wantEntry: entryActionRecovered},
		{name: "marker item of another author is not adopted", minutes: 30, adopt: true, foreign: true,
			wantMutating: []string{"create:ABC-1"}, wantEntry: entryActionCreated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, repo := newFakeSink(), newFakeRepo()
			staleID := bookedThenDeleted(t, s, repo)
			if tt.adopt {
				adoptable(s, tt.foreign)
			}

			rep := mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", tt.minutes)}, s, opts(repo))

			if rep.Repaired != 1 || rep.Skipped+rep.Failed+rep.Created+rep.Updated+rep.Recovered != 0 {
				t.Fatalf("report = %+v", rep)
			}
			if got := rep.Items[0]; got.Action != ActionRepaired || got.Reason != RepairedReason || got.Minutes != tt.minutes {
				t.Fatalf("item = %+v", got)
			}
			if got := s.mutating(); !equalStrings(got, tt.wantMutating) {
				t.Fatalf("mutating calls = %v, want %v", got, tt.wantMutating)
			}
			row := repo.rows[repairKey]
			if row.WorkItemID == staleID || row.Minutes != tt.minutes {
				t.Fatalf("ledger row not replaced: %+v", row)
			}
			booked, ok := s.items[row.WorkItemID]
			if !ok || booked.foreign || booked.item.Minutes != tt.minutes {
				t.Fatalf("ledger points to %q, sink has %+v (found=%v)", row.WorkItemID, booked, ok)
			}
			entries := repo.runs[repo.nextRun].Entries
			if len(entries) != 1 || entries[0].Action != tt.wantEntry || entries[0].WorkItemID != row.WorkItemID {
				t.Fatalf("run entries = %+v", entries)
			}

			// The repaired row is stable: the next sync has nothing to do.
			s.calls = nil
			rep = mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", tt.minutes)}, s, opts(repo))
			if rep.Skipped != 1 || rep.Repaired != 0 || len(s.mutating()) != 0 {
				t.Fatalf("second sync: report=%+v calls=%v", rep, s.calls)
			}
		})
	}
}

func TestSyncChecksEachSkippedRowOnce(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	items := []sessionizer.DayItem{dayItem("ABC-1", 30), dayItem("ABC-2", 60)}
	mustSync(t, items, s, opts(repo))
	s.calls = nil

	rep := mustSync(t, items, s, opts(repo))

	if rep.Skipped != 2 || rep.Repaired != 0 {
		t.Fatalf("report = %+v", rep)
	}
	if want := []string{"find:ABC-1", "find:ABC-2"}; !equalStrings(s.calls, want) {
		t.Fatalf("calls = %v, want %v", s.calls, want)
	}
}

func TestSyncDryRunPlansRepairWithoutMutating(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	staleID := bookedThenDeleted(t, s, repo)
	o := opts(repo)
	o.DryRun = true

	rep := mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 30)}, s, o)

	if rep.Planned != 1 || rep.Repaired != 0 || rep.Items[0].Action != ActionWouldRepair || rep.Items[0].Reason != RepairedReason {
		t.Fatalf("report = %+v", rep)
	}
	if len(s.mutating()) != 0 || len(s.items) != 0 || repo.rows[repairKey].WorkItemID != staleID {
		t.Fatalf("dry run mutated: calls=%v items=%d row=%+v", s.calls, len(s.items), repo.rows[repairKey])
	}
}

// findFailingSink fails every marker lookup.
type findFailingSink struct {
	*fakeSink
	err error
}

func (f findFailingSink) FindByMarker(context.Context, string, string) (string, bool, error) {
	return "", false, f.err
}

func TestSyncReportsFailedLookupOfBookedRow(t *testing.T) {
	tests := []struct {
		name     string
		minutes  int
		findErr  error
		wantStop bool
	}{
		{name: "unchanged: lookup fails", minutes: 30, findErr: errors.New("HTTP 500")},
		{name: "changed: lookup fails", minutes: 45, findErr: errors.New("HTTP 500")},
		{name: "changed: issue is gone", minutes: 45, findErr: youtrack.ErrIssueNotFound},
		{name: "unchanged: rate limited stops the run", minutes: 30, findErr: youtrack.ErrRateLimited, wantStop: true},
		{name: "changed: unauthorized stops the run", minutes: 45, findErr: youtrack.ErrUnauthorized, wantStop: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inner, repo := newFakeSink(), newFakeRepo()
			staleID := bookedThenDeleted(t, inner, repo)
			s := findFailingSink{fakeSink: inner, err: tt.findErr}

			rep, err := Sync(context.Background(), []sessionizer.DayItem{dayItem("ABC-1", tt.minutes)}, s, opts(repo))

			if stopped := err != nil; stopped != tt.wantStop {
				t.Fatalf("Sync error = %v, want stop=%v", err, tt.wantStop)
			}
			if rep.Failed != 1 || rep.Repaired != 0 || rep.Items[0].Error == "" {
				t.Fatalf("report = %+v", rep)
			}
			if len(inner.items) != 0 || repo.rows[repairKey].WorkItemID != staleID {
				t.Fatalf("failed lookup changed state: items=%d row=%+v", len(inner.items), repo.rows[repairKey])
			}
		})
	}
}

func TestSyncReportsFailedRepairAndRetries(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	staleID := bookedThenDeleted(t, s, repo)
	s.createErr["ABC-1"] = errors.New("HTTP 500")

	rep := mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 30)}, s, opts(repo))
	if rep.Failed != 1 || rep.Repaired != 0 || repo.rows[repairKey].WorkItemID != staleID {
		t.Fatalf("report=%+v row=%+v", rep, repo.rows[repairKey])
	}

	delete(s.createErr, "ABC-1")
	rep = mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 30)}, s, opts(repo))
	if rep.Repaired != 1 || len(s.items) != 1 {
		t.Fatalf("retry: report=%+v items=%d", rep, len(s.items))
	}
}

func TestSyncKeepsUpdateFailureWhenItemStillListed(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 30)}, s, opts(repo))
	s.updateErr["ABC-1"] = youtrack.ErrIssueNotFound

	rep := mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 45)}, s, opts(repo))

	if rep.Failed != 1 || rep.Repaired != 0 || len(s.items) != 1 || repo.rows[repairKey].Minutes != 30 {
		t.Fatalf("report=%+v items=%d row=%+v", rep, len(s.items), repo.rows[repairKey])
	}
}

func TestUndoOfRepairRemovesTheNewItem(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	bookedThenDeleted(t, s, repo)
	o := opts(repo)
	mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 30)}, s, o)

	res, err := Undo(context.Background(), s, repo, o.Now)

	if err != nil || res.Deleted != 1 || len(s.items) != 0 || len(repo.rows) != 0 {
		t.Fatalf("undo = %+v err=%v items=%d rows=%d", res, err, len(s.items), len(repo.rows))
	}
}

func TestUndoTreatsMissingUpdatedItemAsDone(t *testing.T) {
	ctx := context.Background()
	s, repo := newFakeSink(), newFakeRepo()
	o := opts(repo)
	mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 30), dayItem("ABC-2", 60)}, s, o)
	mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 45), dayItem("ABC-2", 90)}, s, o)
	delete(s.items, repo.rows[repairKey].WorkItemID)

	res, err := Undo(ctx, s, repo, o.Now)

	if err != nil || !res.Found || res.Restored != 1 || res.Forgotten != 1 {
		t.Fatalf("undo = %+v err=%v", res, err)
	}
	if _, ok := repo.rows[repairKey]; ok {
		t.Fatal("ledger row of the missing item survived undo")
	}
	if row := repo.rows[Key("ABC-2", "2026-01-05", "Development")]; row.Minutes != 60 {
		t.Fatalf("ABC-2 not restored: %+v", row)
	}
	if len(s.items) != 1 {
		t.Fatalf("undo booked the missing item again: %d items", len(s.items))
	}

	// The run is released: the next undo reverts the run before it.
	run, found, err := repo.LastSyncRun(ctx)
	if err != nil || !found || run.ID != 1 {
		t.Fatalf("last run = %+v found=%v err=%v, want run 1", run, found, err)
	}
	res, err = Undo(ctx, s, repo, o.Now)
	if err != nil || res.Deleted != 2 || len(s.items) != 0 || len(repo.rows) != 0 {
		t.Fatalf("second undo = %+v err=%v items=%d rows=%d", res, err, len(s.items), len(repo.rows))
	}
}

func TestUndoStillFailsOnOtherUpdateErrors(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	o := opts(repo)
	mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 30)}, s, o)
	mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 45)}, s, o)
	s.updateErr["ABC-1"] = errors.New("HTTP 500")

	res, err := Undo(context.Background(), s, repo, o.Now)

	if err == nil || res.Restored != 0 || res.Forgotten != 0 {
		t.Fatalf("undo = %+v err=%v", res, err)
	}
	if run, found, _ := repo.LastSyncRun(context.Background()); !found || run.ID != 2 || len(run.Entries) != 1 {
		t.Fatalf("failed entry was cleared: run=%+v found=%v", run, found)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
