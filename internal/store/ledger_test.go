package store

import (
	"context"
	"testing"
	"time"
)

func TestSyncHistoryRevertsLastRunOnly(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	row := func(issue string, minutes int) LedgerRow {
		return LedgerRow{Issue: issue, Date: "2026-01-05", Type: "Development", WorkItemID: issue + "/1", Minutes: minutes, ContentHash: "h", BookedAt: base}
	}
	entry := func(issue, action string, prev, minutes int) SyncEntry {
		return SyncEntry{Issue: issue, Date: "2026-01-05", Type: "Development", Action: action, WorkItemID: issue + "/1", PreviousMinutes: prev, NewMinutes: minutes}
	}
	if _, ok, err := s.LastSyncRun(ctx); err != nil || ok {
		t.Fatalf("empty store: ok=%v err=%v", ok, err)
	}
	first, err := s.BeginSyncRun(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordBooking(ctx, first, row("A-1", 30), entry("A-1", "created", 0, 30)); err != nil {
		t.Fatal(err)
	}
	second, err := s.BeginSyncRun(ctx, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordBooking(ctx, second, row("A-1", 45), entry("A-1", "updated", 30, 45)); err != nil {
		t.Fatal(err)
	}
	last, ok, err := s.LastSyncRun(ctx)
	if err != nil || !ok || last.ID != second || len(last.Entries) != 1 || last.Entries[0].PreviousMinutes != 30 {
		t.Fatalf("last run = %+v ok=%v err=%v", last, ok, err)
	}
	restore := row("A-1", 30)
	if err := s.RevertEntry(ctx, second, last.Entries[0], &restore); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.LedgerGet(ctx, "A-1", "2026-01-05", "Development")
	if err != nil || !ok || got.Minutes != 30 {
		t.Fatalf("ledger after revert = %+v ok=%v err=%v", got, ok, err)
	}
	last, ok, err = s.LastSyncRun(ctx)
	if err != nil || !ok || last.ID != first {
		t.Fatalf("second revert target = %+v ok=%v err=%v", last, ok, err)
	}
	if err := s.RevertEntry(ctx, first, last.Entries[0], nil); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.LedgerGet(ctx, "A-1", "2026-01-05", "Development"); ok {
		t.Fatal("ledger row survived revert of created entry")
	}
	if _, ok, _ := s.LastSyncRun(ctx); ok {
		t.Fatal("run survived after all entries were reverted")
	}
}
