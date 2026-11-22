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

func TestLedgerRowsForDateListsOnlyThatDay(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	run, err := s.BeginSyncRun(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []LedgerRow{
		{Issue: "B-2", Date: "2026-01-05", Type: "Development", WorkItemID: "B-2/1", Minutes: 15, ContentHash: "h", BookedAt: base},
		{Issue: "A-1", Date: "2026-01-05", Type: "Development", WorkItemID: "A-1/1", Minutes: 30, ContentHash: "h", BookedAt: base},
		{Issue: "A-1", Date: "2026-01-06", Type: "Development", WorkItemID: "A-1/2", Minutes: 45, ContentHash: "h", BookedAt: base},
	} {
		e := SyncEntry{Issue: r.Issue, Date: r.Date, Type: r.Type, Action: "created", WorkItemID: r.WorkItemID, NewMinutes: r.Minutes}
		if err := s.RecordBooking(ctx, run, r, e); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.LedgerRowsForDate(ctx, "2026-01-05")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Issue != "A-1" || rows[0].Minutes != 30 || rows[0].WorkItemID != "A-1/1" || !rows[0].BookedAt.Equal(base) || rows[1].Issue != "B-2" {
		t.Fatalf("rows = %+v", rows)
	}
	if rows, err := s.LedgerRowsForDate(ctx, "2026-01-07"); err != nil || len(rows) != 0 {
		t.Fatalf("empty day: rows=%+v err=%v", rows, err)
	}
}

func TestRecordDeletionRemovesRowAndKeepsUndoEntry(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	row := LedgerRow{Issue: "A-1", Date: "2026-01-05", Type: "Development", WorkItemID: "A-1/1", Minutes: 30, ContentHash: "h", BookedAt: base}
	first, err := s.BeginSyncRun(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	created := SyncEntry{Issue: "A-1", Date: "2026-01-05", Type: "Development", Action: "created", WorkItemID: "A-1/1", NewMinutes: 30}
	if err := s.RecordBooking(ctx, first, row, created); err != nil {
		t.Fatal(err)
	}
	second, err := s.BeginSyncRun(ctx, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	deleted := SyncEntry{Issue: "A-1", Date: "2026-01-05", Type: "Development", Action: "deleted", WorkItemID: "A-1/1", PreviousMinutes: 30, Text: "restored"}
	if err := s.RecordDeletion(ctx, second, row, deleted); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.LedgerGet(ctx, "A-1", "2026-01-05", "Development"); err != nil || ok {
		t.Fatalf("ledger row survived deletion: ok=%v err=%v", ok, err)
	}
	last, ok, err := s.LastSyncRun(ctx)
	if err != nil || !ok || last.ID != second || len(last.Entries) != 1 {
		t.Fatalf("last run = %+v ok=%v err=%v", last, ok, err)
	}
	if got := last.Entries[0]; got != deleted {
		t.Fatalf("entry = %+v, want %+v", got, deleted)
	}
}

func TestSyncEntryKeepsPreviousTextAndLastBookedTextFollowsTheHistory(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	const date, typ = "2026-01-05", "Development"
	row := func(minutes int) LedgerRow {
		return LedgerRow{Issue: "A-1", Date: date, Type: typ, WorkItemID: "A-1/1", Minutes: minutes, ContentHash: "h", BookedAt: base}
	}
	if _, ok, err := s.LastBookedText(ctx, "A-1", date, typ); err != nil || ok {
		t.Fatalf("empty history: ok=%v err=%v", ok, err)
	}
	first, _ := s.BeginSyncRun(ctx, base)
	if err := s.RecordBooking(ctx, first, row(30), SyncEntry{Issue: "A-1", Date: date, Type: typ, Action: "created", WorkItemID: "A-1/1", NewMinutes: 30, Text: "one"}); err != nil {
		t.Fatal(err)
	}
	second, _ := s.BeginSyncRun(ctx, base.Add(time.Hour))
	if err := s.RecordBooking(ctx, second, row(45), SyncEntry{Issue: "A-1", Date: date, Type: typ, Action: "updated", WorkItemID: "A-1/1", PreviousMinutes: 30, NewMinutes: 45, Text: "two", PreviousText: "one"}); err != nil {
		t.Fatal(err)
	}

	text, ok, err := s.LastBookedText(ctx, "A-1", date, typ)
	if err != nil || !ok || text != "two" {
		t.Fatalf("LastBookedText = %q ok=%v err=%v, want two", text, ok, err)
	}
	last, _, err := s.LastSyncRun(ctx)
	if err != nil || len(last.Entries) != 1 || last.Entries[0].PreviousText != "one" {
		t.Fatalf("entry = %+v err=%v", last.Entries, err)
	}
	restore := row(30)
	if err := s.RevertEntry(ctx, second, last.Entries[0], &restore); err != nil {
		t.Fatal(err)
	}
	if text, _, _ := s.LastBookedText(ctx, "A-1", date, typ); text != "one" {
		t.Errorf("LastBookedText after undo = %q, want one", text)
	}
}
