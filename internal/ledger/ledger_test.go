package ledger

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/sessionizer"
	"github.com/JtheGunner/ticktack/internal/sink"
	"github.com/JtheGunner/ticktack/internal/store"
)

type fakeItem struct {
	item sink.WorkItem
	// foreign marks an item authored by another user; FindByMarker must not
	// return it.
	foreign bool
}

// fakeSink is an in-memory TimeSink that records every call.
type fakeSink struct {
	items      map[string]fakeItem
	calls      []string
	next       int
	createErr  map[string]error // by issue
	updateErr  map[string]error
	deleteErr  map[string]error // by work item id
	createLand bool             // Create stores the item but still returns an error
	existsErr  error            // returned by every WorkItemExists call
	findErrs   []error          // consumed one per FindByMarker call; nil looks up normally
}

func newFakeSink() *fakeSink {
	return &fakeSink{items: map[string]fakeItem{}, createErr: map[string]error{}, updateErr: map[string]error{}, deleteErr: map[string]error{}}
}

func (f *fakeSink) Create(_ context.Context, w sink.WorkItem) (string, error) {
	f.calls = append(f.calls, "create:"+w.Issue)
	err := f.createErr[w.Issue]
	if err != nil && !f.createLand {
		return "", err
	}
	f.next++
	id := fmt.Sprintf("%s/%d", w.Issue, f.next)
	f.items[id] = fakeItem{item: w}
	if err != nil {
		return "", err
	}
	return id, nil
}

func (f *fakeSink) Update(_ context.Context, id string, w sink.WorkItem) error {
	f.calls = append(f.calls, "update:"+id)
	if err := f.updateErr[w.Issue]; err != nil {
		return err
	}
	if _, ok := f.items[id]; !ok {
		return sink.ErrNotFound
	}
	f.items[id] = fakeItem{item: w}
	return nil
}

func (f *fakeSink) Delete(_ context.Context, id string) error {
	f.calls = append(f.calls, "delete:"+id)
	if err := f.deleteErr[id]; err != nil {
		return err
	}
	delete(f.items, id)
	return nil
}

func (f *fakeSink) WorkItemExists(_ context.Context, issue, id string) (bool, error) {
	f.calls = append(f.calls, "exists:"+id)
	if f.existsErr != nil {
		return false, f.existsErr
	}
	fi, ok := f.items[id]
	return ok && fi.item.Issue == issue, nil
}

func (f *fakeSink) FindByMarker(_ context.Context, issue, marker string) (string, bool, error) {
	f.calls = append(f.calls, "find:"+issue)
	if len(f.findErrs) > 0 {
		err := f.findErrs[0]
		f.findErrs = f.findErrs[1:]
		if err != nil {
			return "", false, err
		}
	}
	ids := make([]string, 0, len(f.items))
	for id := range f.items {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if fi := f.items[id]; !fi.foreign && fi.item.Issue == issue && strings.Contains(fi.item.Text, marker) {
			return id, true, nil
		}
	}
	return "", false, nil
}

func (f *fakeSink) mutating() []string {
	var out []string
	for _, c := range f.calls {
		if !strings.HasPrefix(c, "find:") && !strings.HasPrefix(c, "exists:") {
			out = append(out, c)
		}
	}
	return out
}

// fakeRepo is an in-memory Repository.
type fakeRepo struct {
	rows    map[string]store.LedgerRow
	runs    map[int64]*store.SyncRun
	nextRun int64
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{rows: map[string]store.LedgerRow{}, runs: map[int64]*store.SyncRun{}}
}

func rowKey(issue, date, typ string) string { return Key(issue, date, typ) }

func (r *fakeRepo) LedgerGet(_ context.Context, issue, date, typ string) (store.LedgerRow, bool, error) {
	row, ok := r.rows[rowKey(issue, date, typ)]
	return row, ok, nil
}

func (r *fakeRepo) BeginSyncRun(_ context.Context, at time.Time) (int64, error) {
	r.nextRun++
	r.runs[r.nextRun] = &store.SyncRun{ID: r.nextRun, StartedAt: at}
	return r.nextRun, nil
}

func (r *fakeRepo) RecordBooking(_ context.Context, runID int64, row store.LedgerRow, e store.SyncEntry) error {
	r.rows[rowKey(row.Issue, row.Date, row.Type)] = row
	r.runs[runID].Entries = append(r.runs[runID].Entries, e)
	return nil
}

func (r *fakeRepo) LedgerRowsForDate(_ context.Context, date string) ([]store.LedgerRow, error) {
	var out []store.LedgerRow
	for _, row := range r.rows {
		if row.Date == date {
			out = append(out, row)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return rowKey(out[i].Issue, out[i].Date, out[i].Type) < rowKey(out[j].Issue, out[j].Date, out[j].Type)
	})
	return out, nil
}

func (r *fakeRepo) RecordDeletion(_ context.Context, runID int64, row store.LedgerRow, e store.SyncEntry) error {
	delete(r.rows, rowKey(row.Issue, row.Date, row.Type))
	r.runs[runID].Entries = append(r.runs[runID].Entries, e)
	return nil
}

func (r *fakeRepo) LastSyncRun(context.Context) (store.SyncRun, bool, error) {
	var best int64
	for id, run := range r.runs {
		if len(run.Entries) > 0 && id > best {
			best = id
		}
	}
	if best == 0 {
		return store.SyncRun{}, false, nil
	}
	return *r.runs[best], true, nil
}

func (r *fakeRepo) LastBookedText(_ context.Context, issue, date, typ string) (string, bool, error) {
	var bestRun int64
	var text string
	for id, run := range r.runs {
		for _, e := range run.Entries {
			if e.Issue != issue || e.Date != date || e.Type != typ || id < bestRun {
				continue
			}
			if e.Action == entryActionCreated || e.Action == entryActionUpdated || e.Action == entryActionRecovered {
				bestRun, text = id, e.Text
			}
		}
	}
	return text, bestRun != 0, nil
}

func (r *fakeRepo) RevertEntry(_ context.Context, runID int64, e store.SyncEntry, restore *store.LedgerRow) error {
	k := rowKey(e.Issue, e.Date, e.Type)
	if restore != nil {
		r.rows[k] = *restore
	} else {
		delete(r.rows, k)
	}
	run := r.runs[runID]
	kept := run.Entries[:0:0]
	for _, x := range run.Entries {
		if rowKey(x.Issue, x.Date, x.Type) != k {
			kept = append(kept, x)
		}
	}
	run.Entries = kept
	if len(kept) == 0 {
		delete(r.runs, runID)
	}
	return nil
}

var day = civil.Date{Year: 2026, Month: 1, Day: 5}

func dayItem(ticket string, minutes int) sessionizer.DayItem {
	return sessionizer.DayItem{Date: day, Ticket: ticket, Type: "Development", Minutes: minutes, Description: ticket + " work"}
}

func opts(repo Repository) SyncOptions {
	return SyncOptions{Repo: repo, Now: func() time.Time { return time.Date(2026, 1, 5, 18, 0, 0, 0, time.UTC) }}
}

func mustSync(t *testing.T, items []sessionizer.DayItem, s sink.TimeSink, o SyncOptions) Report {
	t.Helper()
	rep, err := Sync(context.Background(), items, s, o)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	return rep
}

func TestSyncTwiceCreatesOnce(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	items := []sessionizer.DayItem{dayItem("ABC-1", 30)}
	first := mustSync(t, items, s, opts(repo))
	second := mustSync(t, items, s, opts(repo))
	if first.Created != 1 || second.Skipped != 1 || second.Created != 0 {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	if len(s.items) != 1 {
		t.Fatalf("sink has %d items, want 1", len(s.items))
	}
	for _, it := range s.items {
		if !strings.HasSuffix(it.item.Text, "[ticktack:ABC-1:2026-01-05:Development]") {
			t.Fatalf("text lacks marker: %q", it.item.Text)
		}
	}
}

func TestSyncUpdatesWhenTotalGrows(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 30)}, s, opts(repo))
	rep := mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 45)}, s, opts(repo))
	if rep.Updated != 1 || len(s.items) != 1 {
		t.Fatalf("report=%+v items=%d", rep, len(s.items))
	}
	for _, it := range s.items {
		if it.item.Minutes != 45 {
			t.Fatalf("minutes = %d, want 45", it.item.Minutes)
		}
	}
	if row := repo.rows[Key("ABC-1", "2026-01-05", "Development")]; row.Minutes != 45 {
		t.Fatalf("ledger minutes = %d", row.Minutes)
	}
}

func TestSyncSkipsUnchanged(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	items := []sessionizer.DayItem{dayItem("ABC-1", 30)}
	mustSync(t, items, s, opts(repo))
	s.calls = nil
	rep := mustSync(t, items, s, opts(repo))
	if rep.Skipped != 1 || len(s.mutating()) != 0 {
		t.Fatalf("report=%+v calls=%v", rep, s.calls)
	}
}

func TestSyncRecoversExistingItemByMarker(t *testing.T) {
	s := newFakeSink()
	mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 30)}, s, opts(newFakeRepo()))
	lostDB := newFakeRepo()
	rep := mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 45)}, s, opts(lostDB))
	if rep.Recovered != 1 || rep.Created != 0 || len(s.items) != 1 {
		t.Fatalf("report=%+v items=%d", rep, len(s.items))
	}
	row := lostDB.rows[Key("ABC-1", "2026-01-05", "Development")]
	if row.WorkItemID == "" || row.Minutes != 45 {
		t.Fatalf("ledger row = %+v", row)
	}
}

func TestMarkerDoesNotMatchLongerKey(t *testing.T) {
	s := newFakeSink()
	s.items["ABC-1/1"] = fakeItem{item: sink.WorkItem{Issue: "ABC-1", Text: "x [ticktack:ABC-1:2026-01-05:Development2]"}}
	rep := mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 30)}, s, opts(newFakeRepo()))
	if rep.Created != 1 {
		t.Fatalf("report=%+v", rep)
	}
}

func TestSyncDryRunDoesNotMutate(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	mustSync(t, []sessionizer.DayItem{dayItem("ABC-2", 30)}, s, opts(repo))
	repo = newFakeRepo() // empty ledger forces the recover path
	s.calls = nil
	o := opts(repo)
	o.DryRun = true
	items := []sessionizer.DayItem{dayItem("ABC-1", 30), dayItem("ABC-2", 60)}
	rep := mustSync(t, items, s, o)
	if got := s.mutating(); len(got) != 0 {
		t.Fatalf("mutating sink calls in dry run: %v", got)
	}
	if len(repo.rows) != 0 || len(repo.runs) != 0 {
		t.Fatalf("dry run wrote ledger state: %+v %+v", repo.rows, repo.runs)
	}
	if rep.Planned != 2 || rep.Items[0].Action != ActionWouldCreate || rep.Items[1].Action != ActionWouldRecover {
		t.Fatalf("report = %+v", rep)
	}
}

func TestSyncContinuesAfterSingleItemFailure(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	s.createErr["ABC-2"] = sink.ErrNotFound
	items := []sessionizer.DayItem{dayItem("ABC-1", 30), dayItem("ABC-2", 30), dayItem("ABC-3", 30)}
	rep, err := Sync(context.Background(), items, s, opts(repo))
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if rep.Created != 2 || rep.Failed != 1 {
		t.Fatalf("report = %+v", rep)
	}
	if rep.Items[1].Action != ActionFailed || !strings.Contains(rep.Items[1].Error, "issue not found") {
		t.Fatalf("failure not listed: %+v", rep.Items[1])
	}
	if _, ok := repo.rows[Key("ABC-2", "2026-01-05", "Development")]; ok {
		t.Fatal("ledger row written for failed item")
	}
}

func TestSyncStopsOnUnauthorized(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	s.createErr["ABC-2"] = sink.ErrUnauthorized
	items := []sessionizer.DayItem{dayItem("ABC-1", 30), dayItem("ABC-2", 30), dayItem("ABC-3", 30)}
	rep, err := Sync(context.Background(), items, s, opts(repo))
	if !errors.Is(err, sink.ErrUnauthorized) {
		t.Fatalf("err = %v", err)
	}
	if rep.Created != 1 || rep.Failed != 1 || rep.NotSent != 1 {
		t.Fatalf("report = %+v", rep)
	}
	if len(repo.rows) != 1 {
		t.Fatalf("ledger has %d rows, want only the booked one", len(repo.rows))
	}
	if len(s.items) != 1 {
		t.Fatalf("sink has %d items", len(s.items))
	}
}

func TestSyncRecoversAfterAmbiguousCreateFailure(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	s.createErr["ABC-1"] = errors.New("youtrack: server error 502")
	s.createLand = true
	rep := mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 30)}, s, opts(repo))
	if rep.Recovered != 1 || rep.Failed != 0 || len(s.items) != 1 {
		t.Fatalf("report=%+v items=%d", rep, len(s.items))
	}
	if len(repo.rows) != 1 {
		t.Fatalf("ledger rows = %d", len(repo.rows))
	}
	s.calls = nil
	res, err := Undo(context.Background(), s, repo, opts(repo).Now)
	if err != nil || res.Forgotten != 1 || res.Deleted != 0 {
		t.Fatalf("undo = %+v err=%v", res, err)
	}
	for _, c := range s.calls {
		if strings.HasPrefix(c, "delete:") {
			t.Fatalf("undo deleted a recovered item: %v", s.calls)
		}
	}
	if len(s.items) != 1 || len(repo.rows) != 0 {
		t.Fatalf("items=%d rows=%d", len(s.items), len(repo.rows))
	}
}

func TestSyncReportsUnclearCreateAndNextSyncFindsTheItem(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	s.createErr["ABC-1"] = fmt.Errorf("POST /api/issues/ABC-1/timeTracking/workItems: %w: status 504", sink.ErrOutcomeUnclear)
	s.createLand = true
	// The lookup before the create works, the one right after it fails too.
	s.findErrs = []error{nil, errors.New("youtrack: unexpected status 504")}
	items := []sessionizer.DayItem{dayItem("ABC-1", 30)}

	rep := mustSync(t, items, s, opts(repo))
	if rep.Failed != 1 || rep.Created != 0 || len(repo.rows) != 0 {
		t.Fatalf("report=%+v rows=%d", rep, len(repo.rows))
	}
	if got := rep.Items[0]; got.Action != ActionFailed || !strings.Contains(got.Error, "outcome unclear") {
		t.Fatalf("item = %+v, want a failure reported as unclear outcome", got)
	}

	s.createErr = map[string]error{}
	rep = mustSync(t, items, s, opts(repo))
	if rep.Recovered != 1 || rep.Created != 0 || rep.Failed != 0 {
		t.Fatalf("second report = %+v", rep)
	}
	if len(s.items) != 1 || len(repo.rows) != 1 {
		t.Fatalf("items=%d rows=%d, want one work item adopted by the ledger", len(s.items), len(repo.rows))
	}
}

func TestSyncIgnoresMarkerMatchOfAnotherAuthor(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	s.items["ABC-1/colleague"] = fakeItem{
		item:    sink.WorkItem{Issue: "ABC-1", Minutes: 99, Text: "theirs " + Marker(Key("ABC-1", "2026-01-05", "Development"))},
		foreign: true,
	}
	rep := mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 30)}, s, opts(repo))
	if rep.Created != 1 || rep.Recovered != 0 {
		t.Fatalf("report = %+v", rep)
	}
	if got := s.items["ABC-1/colleague"].item.Minutes; got != 99 {
		t.Fatalf("colleague item was modified: %d", got)
	}
	if len(s.items) != 2 {
		t.Fatalf("sink items = %d, want colleague item plus own", len(s.items))
	}
}

func TestSyncDoesNotLookUpAfterRateLimitedCreate(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	s.createErr["ABC-1"] = sink.ErrRateLimited
	rep, err := Sync(context.Background(), []sessionizer.DayItem{dayItem("ABC-1", 30), dayItem("ABC-2", 30)}, s, opts(repo))
	if !errors.Is(err, sink.ErrRateLimited) || rep.NotSent != 1 {
		t.Fatalf("report=%+v err=%v", rep, err)
	}
	finds := 0
	for _, c := range s.calls {
		if c == "find:ABC-1" {
			finds++
		}
	}
	if finds != 1 {
		t.Fatalf("FindByMarker called %d times for ABC-1, want 1 (before create only): %v", finds, s.calls)
	}
}

func TestSyncSkipsItemsWithoutTicket(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	rep := mustSync(t, []sessionizer.DayItem{dayItem("", 30)}, s, opts(repo))
	if rep.Skipped != 1 || rep.Items[0].Reason != "no ticket" || len(s.calls) != 0 {
		t.Fatalf("report=%+v calls=%v", rep, s.calls)
	}
}

func TestUndoRevertsLastSyncOnly(t *testing.T) {
	ctx := context.Background()
	s, repo := newFakeSink(), newFakeRepo()
	o := opts(repo)
	mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 30)}, s, o)
	mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 45), dayItem("ABC-2", 60)}, s, o)

	res, err := Undo(ctx, s, repo, o.Now)
	if err != nil || !res.Found || res.Deleted != 1 || res.Restored != 1 {
		t.Fatalf("undo = %+v err=%v", res, err)
	}
	if len(s.items) != 1 {
		t.Fatalf("sink items = %d, want only ABC-1", len(s.items))
	}
	for _, it := range s.items {
		if it.item.Issue != "ABC-1" || it.item.Minutes != 30 {
			t.Fatalf("remaining item = %+v", it.item)
		}
	}
	if row := repo.rows[Key("ABC-1", "2026-01-05", "Development")]; row.Minutes != 30 {
		t.Fatalf("restored ledger minutes = %d", row.Minutes)
	}
	if _, ok := repo.rows[Key("ABC-2", "2026-01-05", "Development")]; ok {
		t.Fatal("ledger row of created item survived undo")
	}

	// A second undo reverts the run before it.
	res, err = Undo(ctx, s, repo, o.Now)
	if err != nil || res.Deleted != 1 || len(s.items) != 0 || len(repo.rows) != 0 {
		t.Fatalf("second undo = %+v err=%v items=%d rows=%d", res, err, len(s.items), len(repo.rows))
	}
	res, err = Undo(ctx, s, repo, o.Now)
	if err != nil || res.Found {
		t.Fatalf("third undo = %+v err=%v", res, err)
	}
}

// reconciling returns options that reconcile the test day.
func reconciling(repo Repository) SyncOptions {
	o := opts(repo)
	o.Reconcile = []civil.Date{day}
	return o
}

func sinkMinutes(s *fakeSink) map[string]int {
	out := map[string]int{}
	for _, it := range s.items {
		out[it.item.Issue] += it.item.Minutes
	}
	return out
}

func TestSyncReconcilesBookedRowsOfTheDay(t *testing.T) {
	tests := []struct {
		name        string
		second      []sessionizer.DayItem
		wantDeleted int
		wantUpdated int
		wantSink    map[string]int
	}{
		{"reassigned to another ticket", []sessionizer.DayItem{dayItem("ABC-2", 60)}, 1, 0, map[string]int{"ABC-2": 60}},
		{"discarded", nil, 1, 0, map[string]int{}},
		// An item that rounds to zero is omitted by the sessionizer.
		{"rounded to zero next to a remaining item", []sessionizer.DayItem{dayItem("ABC-3", 15)}, 1, 0, map[string]int{"ABC-3": 15}},
		{"partly reassigned", []sessionizer.DayItem{dayItem("ABC-1", 30), dayItem("ABC-2", 30)}, 0, 1, map[string]int{"ABC-1": 30, "ABC-2": 30}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, repo := newFakeSink(), newFakeRepo()
			mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 60)}, s, reconciling(repo))
			rep := mustSync(t, tt.second, s, reconciling(repo))
			if rep.Deleted != tt.wantDeleted || rep.Updated != tt.wantUpdated || rep.Failed != 0 {
				t.Fatalf("report = %+v", rep)
			}
			got := sinkMinutes(s)
			if len(got) != len(tt.wantSink) {
				t.Fatalf("sink = %v, want %v", got, tt.wantSink)
			}
			for issue, minutes := range tt.wantSink {
				if got[issue] != minutes {
					t.Fatalf("sink = %v, want %v", got, tt.wantSink)
				}
			}
			if len(repo.rows) != len(tt.wantSink) {
				t.Fatalf("ledger rows = %+v, want one per booked item", repo.rows)
			}
		})
	}
}

func TestSyncReportsDeletedRowWithItsMinutes(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 60)}, s, reconciling(repo))
	rep := mustSync(t, []sessionizer.DayItem{dayItem("ABC-2", 60)}, s, reconciling(repo))
	last := rep.Items[len(rep.Items)-1]
	if last.Key != "ABC-1:2026-01-05:Development" || last.Action != ActionDeleted || last.Minutes != 60 {
		t.Fatalf("deleted entry = %+v", last)
	}
}

func TestSyncKeepsRowsOfOtherDaysAndKeptKeys(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	other := dayItem("OLD-1", 30)
	other.Date = civil.Date{Year: 2026, Month: 1, Day: 4}
	mustSync(t, []sessionizer.DayItem{other, dayItem("ABC-1", 60), dayItem("ABC-2", 15)}, s, opts(repo))
	o := reconciling(repo)
	o.Keep = []string{Key("ABC-2", "2026-01-05", "Development")}
	rep := mustSync(t, nil, s, o)
	if rep.Deleted != 1 {
		t.Fatalf("report = %+v", rep)
	}
	got := sinkMinutes(s)
	if got["OLD-1"] != 30 || got["ABC-2"] != 15 || len(got) != 2 {
		t.Fatalf("sink = %v, want OLD-1 and ABC-2 untouched", got)
	}
}

func TestSyncDoesNotReconcileRowsOfHeldProjects(t *testing.T) {
	tests := []struct {
		name string
		hold []string
		// wantKept lists the issues whose booking survives a run without items.
		wantKept []string
	}{
		{"nothing held", nil, nil},
		{"one project held", []string{"GEN"}, []string{"GEN-5"}},
		{"held project in another case", []string{"gen"}, []string{"GEN-5"}},
		{"a longer project key is not a match", []string{"GE"}, nil},
		{"a project key with a dash", []string{"GEN-X"}, []string{"GEN-X-2"}},
		{"several projects held", []string{"GEN", "GEN-X"}, []string{"GEN-5", "GEN-X-2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, repo := newFakeSink(), newFakeRepo()
			mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 60), dayItem("GEN-5", 30), dayItem("GEN-X-2", 15)}, s, opts(repo))
			o := reconciling(repo)
			o.HoldProjects = tt.hold
			rep := mustSync(t, nil, s, o)

			if want := 3 - len(tt.wantKept); rep.Deleted != want || rep.Failed != 0 {
				t.Fatalf("report = %+v, want %d deleted", rep, want)
			}
			got := sinkMinutes(s)
			if len(got) != len(tt.wantKept) || len(repo.rows) != len(tt.wantKept) {
				t.Fatalf("sink = %v, ledger rows = %+v, want only %v", got, repo.rows, tt.wantKept)
			}
			for _, issue := range tt.wantKept {
				if _, ok := repo.rows[Key(issue, "2026-01-05", "Development")]; !ok || got[issue] == 0 {
					t.Errorf("%s was reconciled away: sink %v, ledger rows %+v", issue, got, repo.rows)
				}
			}
		})
	}
}

func TestSyncDryRunPlansNoDeletionForHeldProjects(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 60), dayItem("GEN-5", 30)}, s, opts(repo))
	o := reconciling(repo)
	o.HoldProjects = []string{"GEN"}
	o.DryRun = true
	rep := mustSync(t, nil, s, o)
	if rep.Planned != 1 || len(rep.Items) != 1 || rep.Items[0].Key != Key("ABC-1", "2026-01-05", "Development") {
		t.Fatalf("report = %+v, want only ABC-1 planned for deletion", rep)
	}
}

func TestSyncWithoutReconcileDeletesNothing(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 60)}, s, opts(repo))
	rep := mustSync(t, []sessionizer.DayItem{dayItem("ABC-2", 60)}, s, opts(repo))
	if rep.Deleted != 0 || len(s.items) != 2 {
		t.Fatalf("report=%+v items=%d", rep, len(s.items))
	}
}

func TestSyncDryRunPlansDeletionWithoutMutating(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 60)}, s, reconciling(repo))
	runs := len(repo.runs)
	s.calls = nil
	o := reconciling(repo)
	o.DryRun = true
	rep := mustSync(t, nil, s, o)
	if got := s.mutating(); len(got) != 0 {
		t.Fatalf("mutating sink calls in dry run: %v", got)
	}
	if len(repo.rows) != 1 || len(repo.runs) != runs {
		t.Fatalf("dry run wrote ledger state: %+v %+v", repo.rows, repo.runs)
	}
	if rep.Planned != 1 || rep.Deleted != 0 || len(rep.Items) != 1 || rep.Items[0].Action != ActionWouldDelete {
		t.Fatalf("report = %+v", rep)
	}
}

func TestSyncContinuesAfterFailedDeletion(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 60), dayItem("ABC-2", 30), dayItem("ABC-3", 15)}, s, reconciling(repo))
	failing := repo.rows[Key("ABC-2", "2026-01-05", "Development")].WorkItemID
	s.deleteErr[failing] = errors.New("youtrack: server error 500")
	rep := mustSync(t, nil, s, reconciling(repo))
	if rep.Deleted != 2 || rep.Failed != 1 {
		t.Fatalf("report = %+v", rep)
	}
	if rep.Items[1].Action != ActionFailed || !strings.Contains(rep.Items[1].Error, "server error 500") {
		t.Fatalf("failure not listed: %+v", rep.Items[1])
	}
	if len(repo.rows) != 1 || len(s.items) != 1 {
		t.Fatalf("rows=%+v items=%d, want only the failed one left", repo.rows, len(s.items))
	}
}

func TestSyncTreatsAlreadyMissingWorkItemAsDeleted(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 60)}, s, reconciling(repo))
	s.deleteErr[repo.rows[Key("ABC-1", "2026-01-05", "Development")].WorkItemID] = sink.ErrNotFound
	rep := mustSync(t, nil, s, reconciling(repo))
	if rep.Deleted != 1 || rep.Failed != 0 || len(repo.rows) != 0 {
		t.Fatalf("report=%+v rows=%+v", rep, repo.rows)
	}
}

func TestSyncDoesNotReconcileAfterStopError(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 60)}, s, reconciling(repo))
	s.createErr["ABC-2"] = sink.ErrUnauthorized
	s.calls = nil
	_, err := Sync(context.Background(), []sessionizer.DayItem{dayItem("ABC-2", 60)}, s, reconciling(repo))
	if !errors.Is(err, sink.ErrUnauthorized) {
		t.Fatalf("err = %v", err)
	}
	if len(repo.rows) != 1 || len(s.items) != 1 {
		t.Fatalf("a stopped run reconciled: rows=%+v calls=%v", repo.rows, s.calls)
	}
}

func TestSyncStopsReconcilingOnStopError(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 60), dayItem("ABC-2", 30)}, s, reconciling(repo))
	s.deleteErr[repo.rows[Key("ABC-1", "2026-01-05", "Development")].WorkItemID] = sink.ErrRateLimited
	rep, err := Sync(context.Background(), nil, s, reconciling(repo))
	if !errors.Is(err, sink.ErrRateLimited) {
		t.Fatalf("err = %v", err)
	}
	if rep.Failed != 1 || rep.NotSent != 1 || len(repo.rows) != 2 {
		t.Fatalf("report=%+v rows=%+v", rep, repo.rows)
	}
}

func TestUndoRecreatesDeletedItems(t *testing.T) {
	ctx := context.Background()
	s, repo := newFakeSink(), newFakeRepo()
	mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 60)}, s, reconciling(repo))
	mustSync(t, []sessionizer.DayItem{dayItem("ABC-2", 60)}, s, reconciling(repo))

	res, err := Undo(ctx, s, repo, opts(repo).Now)
	if err != nil || res.Deleted != 1 || res.Recreated != 1 {
		t.Fatalf("undo = %+v err=%v", res, err)
	}
	got := sinkMinutes(s)
	if len(got) != 1 || got["ABC-1"] != 60 {
		t.Fatalf("sink = %v, want ABC-1 back at 60", got)
	}
	key := Key("ABC-1", "2026-01-05", "Development")
	row, ok := repo.rows[key]
	if !ok || row.Minutes != 60 || len(repo.rows) != 1 {
		t.Fatalf("ledger rows = %+v", repo.rows)
	}
	item, ok := s.items[row.WorkItemID]
	if !ok || !strings.HasSuffix(item.item.Text, Marker(key)) || item.item.Date != day || item.item.Type != "Development" {
		t.Fatalf("ledger row %+v does not point at the re-created item %+v", row, item)
	}
	// The restored state is stable: the next sync of the original day changes nothing.
	rep := mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 60)}, s, reconciling(repo))
	if rep.Skipped != 1 || rep.Deleted != 0 || rep.Created != 0 {
		t.Fatalf("sync after undo = %+v", rep)
	}
}

func TestUndoReusesItemAlreadyRecreated(t *testing.T) {
	ctx := context.Background()
	s, repo := newFakeSink(), newFakeRepo()
	mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 60)}, s, reconciling(repo))
	mustSync(t, nil, s, reconciling(repo))
	// An earlier undo attempt created the item but failed before clearing the entry.
	key := Key("ABC-1", "2026-01-05", "Development")
	s.items["ABC-1/earlier"] = fakeItem{item: sink.WorkItem{Issue: "ABC-1", Date: day, Minutes: 60, Type: "Development", Text: "x " + Marker(key)}}

	res, err := Undo(ctx, s, repo, opts(repo).Now)
	if err != nil || res.Recreated != 1 || len(s.items) != 1 {
		t.Fatalf("undo = %+v err=%v items=%d", res, err, len(s.items))
	}
	if repo.rows[key].WorkItemID != "ABC-1/earlier" {
		t.Fatalf("ledger row = %+v", repo.rows[key])
	}
}

func TestSyncSendsAheadItemsFirstAndHoldsTheRestWhenTheyFail(t *testing.T) {
	boom := errors.New("boom")
	ahead := func(it sessionizer.DayItem) bool { return it.Ticket == "GEN-5" }
	tests := []struct {
		name string
		// failCreate fails the creation of the ahead item.
		failCreate  bool
		wantMinutes map[string]int
		wantFailed  int
		wantNotSent int
		wantDeleted int
	}{
		{"ahead item booked: the reductions and the reconciliation follow", false, map[string]int{"ABC-1": 30, "GEN-5": 60}, 0, 0, 1},
		{"ahead item fails: nothing is reduced or deleted", true, map[string]int{"ABC-1": 60, "ABC-2": 60}, 1, 1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, repo := newFakeSink(), newFakeRepo()
			mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 60), dayItem("ABC-2", 60)}, s, opts(repo))
			if tt.failCreate {
				s.createErr["GEN-5"] = boom
			}
			o := reconciling(repo)
			o.Ahead = ahead
			// ABC-1 is reduced, ABC-2 moves away entirely, GEN-5 is new.
			rep := mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 30), dayItem("GEN-5", 60)}, s, o)

			if rep.Failed != tt.wantFailed || rep.NotSent != tt.wantNotSent || rep.Deleted != tt.wantDeleted {
				t.Fatalf("report = %+v, want failed %d, not sent %d, deleted %d", rep, tt.wantFailed, tt.wantNotSent, tt.wantDeleted)
			}
			got := sinkMinutes(s)
			if len(got) != len(tt.wantMinutes) {
				t.Fatalf("sink = %v, want %v", got, tt.wantMinutes)
			}
			for issue, minutes := range tt.wantMinutes {
				if got[issue] != minutes {
					t.Errorf("%s = %d minutes, want %d (sink %v)", issue, got[issue], minutes, got)
				}
			}
		})
	}
}

func TestSyncAheadItemsGoBeforeTheOthers(t *testing.T) {
	s, repo := newFakeSink(), newFakeRepo()
	o := opts(repo)
	o.Ahead = func(it sessionizer.DayItem) bool { return it.Ticket == "ZZZ-1" }
	mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 30), dayItem("ZZZ-1", 30)}, s, o)
	if len(s.calls) == 0 || !strings.HasPrefix(s.mutating()[0], "create:ZZZ-1") {
		t.Fatalf("calls = %v, want the ahead item created first", s.mutating())
	}
}
