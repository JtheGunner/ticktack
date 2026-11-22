package ledger

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/sessionizer"
	"github.com/JtheGunner/ticktack/internal/sink"
	"github.com/JtheGunner/ticktack/internal/store"
)

// cancelSink cancels the run's context right after the first successful
// mutating call, which is when a signal does the most harm: the work item
// exists in the sink but its ledger write is still to come.
type cancelSink struct {
	*fakeSink
	cancel context.CancelFunc
}

func (c *cancelSink) Create(ctx context.Context, w sink.WorkItem) (string, error) {
	id, err := c.fakeSink.Create(ctx, w)
	c.cancel()
	return id, err
}

func (c *cancelSink) Delete(ctx context.Context, id string) error {
	err := c.fakeSink.Delete(ctx, id)
	c.cancel()
	return err
}

// liveCtxRepo refuses every write made with a cancelled context, like the
// SQLite driver does.
type liveCtxRepo struct{ *fakeRepo }

func (r liveCtxRepo) BeginSyncRun(ctx context.Context, at time.Time) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return r.fakeRepo.BeginSyncRun(ctx, at)
}

func (r liveCtxRepo) RecordBooking(ctx context.Context, runID int64, row store.LedgerRow, e store.SyncEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.fakeRepo.RecordBooking(ctx, runID, row, e)
}

func (r liveCtxRepo) RecordDeletion(ctx context.Context, runID int64, row store.LedgerRow, e store.SyncEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.fakeRepo.RecordDeletion(ctx, runID, row, e)
}

func (r liveCtxRepo) RevertEntry(ctx context.Context, runID int64, e store.SyncEntry, restore *store.LedgerRow) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.fakeRepo.RevertEntry(ctx, runID, e, restore)
}

func TestSyncCancelledMidRunFinishesTheItemInFlight(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, repo := &cancelSink{fakeSink: newFakeSink(), cancel: cancel}, newFakeRepo()
	items := []sessionizer.DayItem{dayItem("ABC-1", 30), dayItem("ABC-2", 30), dayItem("ABC-3", 30)}

	rep, err := Sync(ctx, items, s, opts(liveCtxRepo{repo}))

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if rep.Created != 1 || rep.NotSent != 2 || rep.Failed != 0 {
		t.Fatalf("report = %+v", rep)
	}
	if len(s.items) != 1 || len(repo.rows) != 1 {
		t.Fatalf("sink has %d items, ledger %d rows; want 1 each", len(s.items), len(repo.rows))
	}
	if _, ok := repo.rows[Key("ABC-1", "2026-01-05", "Development")]; !ok {
		t.Fatal("the item sent before the signal is not in the ledger")
	}
	if got := rep.Items[1]; got.Key != Key("ABC-2", "2026-01-05", "Development") || got.Action != ActionNotSent {
		t.Fatalf("second item = %+v", got)
	}
}

func TestSyncCancelledMidReconcileFinishesTheDeletionInFlight(t *testing.T) {
	base, repo := newFakeSink(), newFakeRepo()
	mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 30), dayItem("ABC-2", 30)}, base, opts(repo))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &cancelSink{fakeSink: base, cancel: cancel}

	rep, err := Sync(ctx, nil, s, reconciling(liveCtxRepo{repo}))

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if rep.Deleted != 1 || rep.NotSent != 1 {
		t.Fatalf("report = %+v", rep)
	}
	if len(base.items) != 1 || len(repo.rows) != 1 {
		t.Fatalf("sink has %d items, ledger %d rows; want 1 each", len(base.items), len(repo.rows))
	}
}

func TestUndoCancelledMidRunFinishesTheEntryInFlight(t *testing.T) {
	base, repo := newFakeSink(), newFakeRepo()
	o := opts(repo)
	mustSync(t, []sessionizer.DayItem{dayItem("ABC-1", 30), dayItem("ABC-2", 30)}, base, o)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &cancelSink{fakeSink: base, cancel: cancel}

	res, err := Undo(ctx, s, liveCtxRepo{repo}, o.Now)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if res.Deleted != 1 {
		t.Fatalf("result = %+v", res)
	}
	// Sink and ledger agree: one item deleted and its row gone, one untouched.
	if len(base.items) != 1 || len(repo.rows) != 1 {
		t.Fatalf("sink has %d items, ledger %d rows; want 1 each", len(base.items), len(repo.rows))
	}
}
