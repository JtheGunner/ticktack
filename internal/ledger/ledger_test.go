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
	"github.com/JtheGunner/ticktack/internal/youtrack"
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
	createLand bool // Create stores the item but still returns an error
}

func newFakeSink() *fakeSink {
	return &fakeSink{items: map[string]fakeItem{}, createErr: map[string]error{}, updateErr: map[string]error{}}
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
		return youtrack.ErrIssueNotFound
	}
	f.items[id] = fakeItem{item: w}
	return nil
}

func (f *fakeSink) Delete(_ context.Context, id string) error {
	f.calls = append(f.calls, "delete:"+id)
	delete(f.items, id)
	return nil
}

func (f *fakeSink) FindByMarker(_ context.Context, issue, marker string) (string, bool, error) {
	f.calls = append(f.calls, "find:"+issue)
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
		if !strings.HasPrefix(c, "find:") {
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
	if rep.Skipped != 1 || len(s.calls) != 0 {
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
	s.createErr["ABC-2"] = youtrack.ErrIssueNotFound
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
	s.createErr["ABC-2"] = youtrack.ErrUnauthorized
	items := []sessionizer.DayItem{dayItem("ABC-1", 30), dayItem("ABC-2", 30), dayItem("ABC-3", 30)}
	rep, err := Sync(context.Background(), items, s, opts(repo))
	if !errors.Is(err, youtrack.ErrUnauthorized) {
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
	s.createErr["ABC-1"] = youtrack.ErrRateLimited
	rep, err := Sync(context.Background(), []sessionizer.DayItem{dayItem("ABC-1", 30), dayItem("ABC-2", 30)}, s, opts(repo))
	if !errors.Is(err, youtrack.ErrRateLimited) || rep.NotSent != 1 {
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
