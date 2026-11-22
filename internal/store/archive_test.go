package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"
)

var (
	archiveCarry = CarrySources{Manual: "manual", AppHint: "title"}
	archiveFrom  = time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	archiveTo    = archiveFrom.Add(24 * time.Hour)
)

func presenceAt(at time.Time) event.Event {
	return event.Event{At: at, Source: "presence", Kind: event.KindPresence}
}

func archiveRequest(dryRun bool) ArchiveRequest {
	return ArchiveRequest{Day: "2026-01-05", From: archiveFrom, To: archiveTo, ArchivedAt: archiveTo.Add(48 * time.Hour), Carry: archiveCarry, DryRun: dryRun}
}

func countEventsBetween(t *testing.T, s *Store, from, to time.Time) int {
	t.Helper()
	got, err := s.Events(context.Background(), from, to)
	if err != nil {
		t.Fatal(err)
	}
	return len(got)
}

func TestArchiveDayDeletesOnlyThatDayAndKeepsEverythingElse(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	for _, e := range []event.Event{
		presenceAt(archiveFrom.Add(-time.Minute)), // the day before
		presenceAt(archiveFrom),                   // first instant of the day
		presenceAt(archiveFrom.Add(10 * time.Hour)),
		presenceAt(archiveTo.Add(-time.Nanosecond)), // last instant of the day
		presenceAt(archiveTo),                       // the day after
	} {
		if err := s.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddOverride(ctx, event.Override{Start: archiveFrom.Add(time.Hour), End: archiveFrom.Add(2 * time.Hour), Ticket: "ABC-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.root.Exec(`INSERT INTO ledger (issue, date, type, work_item_id, minutes, content_hash, booked_at)
		VALUES ('ABC-1','2026-01-05','Development','1-1',30,'h',1)`); err != nil {
		t.Fatal(err)
	}

	deleted, err := s.ArchiveDay(ctx, archiveRequest(false))
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 3 {
		t.Errorf("deleted = %d, want 3", deleted)
	}
	if n := countEventsBetween(t, s, archiveFrom, archiveTo); n != 0 {
		t.Errorf("%d events of the archived day remain", n)
	}
	if n := countEventsBetween(t, s, archiveFrom.Add(-time.Hour), archiveFrom); n != 1 {
		t.Errorf("the day before has %d events, want 1", n)
	}
	if n := countEventsBetween(t, s, archiveTo, archiveTo.Add(time.Hour)); n != 1 {
		t.Errorf("the day after has %d events, want 1", n)
	}
	if n := rowCount(t, s, "overrides"); n != 1 {
		t.Errorf("overrides = %d, want 1", n)
	}
	if n := rowCount(t, s, "ledger"); n != 1 {
		t.Errorf("ledger = %d, want 1", n)
	}
	got, found, err := s.ArchivedDay(ctx, "2026-01-05")
	if err != nil || !found || got.EventsDeleted != 3 {
		t.Errorf("ArchivedDay = %+v, %v, %v", got, found, err)
	}
	all, err := s.ArchivedDays(ctx)
	if err != nil || len(all) != 1 {
		t.Errorf("ArchivedDays = %+v, %v", all, err)
	}
}

func TestArchiveDayKeepsTheContextTheNextDaysStartFrom(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	branch := event.Event{At: archiveFrom.Add(9 * time.Hour), Source: "shell", Kind: event.KindContext, Repo: "/r", Branch: "ABC-1-x"}
	for _, e := range []event.Event{presenceAt(archiveFrom.Add(8 * time.Hour)), branch, presenceAt(archiveFrom.Add(10 * time.Hour))} {
		if err := s.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	deleted, err := s.ArchiveDay(ctx, archiveRequest(false))
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2 (the context anchor stays)", deleted)
	}
	carried, err := s.CarriedContext(ctx, archiveTo, archiveCarry)
	if err != nil {
		t.Fatal(err)
	}
	if len(carried) != 1 || carried[0].Branch != "ABC-1-x" {
		t.Errorf("carried context = %+v, want the branch event", carried)
	}
}

func TestArchiveDayDryRunChangesNothing(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	for _, e := range []event.Event{presenceAt(archiveFrom.Add(time.Hour)), presenceAt(archiveFrom.Add(2 * time.Hour))} {
		if err := s.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	deleted, err := s.ArchiveDay(ctx, archiveRequest(true))
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Errorf("dry run counted %d, want 2", deleted)
	}
	if n := countEventsBetween(t, s, archiveFrom, archiveTo); n != 2 {
		t.Errorf("dry run left %d events, want 2", n)
	}
	if _, found, _ := s.ArchivedDay(ctx, "2026-01-05"); found {
		t.Error("dry run archived the day")
	}
}

func TestArchiveDayRefusesAnArchivedDayAndBadRequests(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	if _, err := s.ArchiveDay(ctx, archiveRequest(false)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ArchiveDay(ctx, archiveRequest(false)); err == nil || !strings.Contains(err.Error(), "already archived") {
		t.Errorf("second archive: err = %v", err)
	}
	bad := archiveRequest(false)
	bad.To = bad.From
	if _, err := s.ArchiveDay(ctx, bad); err == nil {
		t.Error("an empty range was accepted")
	}
}

func TestArchiveDayRollsBackWhenTheRecordFails(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	if err := s.Append(ctx, presenceAt(archiveFrom.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	// A different day string that violates nothing, but a clashing primary
	// key on a second attempt with other bounds proves the delete is undone.
	if _, err := s.root.Exec(`INSERT INTO archived_days (day, start_at, end_at, archived_at, events_deleted) VALUES ('2026-01-05', 0, 1, 0, 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ArchiveDay(ctx, archiveRequest(false)); err == nil {
		t.Fatal("want an error for the clashing day")
	}
	if n := countEventsBetween(t, s, archiveFrom, archiveTo); n != 1 {
		t.Errorf("events = %d after a failed archive, want 1", n)
	}
}

func TestAppendIgnoresEventsOfAnArchivedDay(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	if _, err := s.ArchiveDay(ctx, archiveRequest(false)); err != nil {
		t.Fatal(err)
	}
	for _, e := range []event.Event{presenceAt(archiveFrom.Add(time.Hour)), {At: archiveFrom.Add(2 * time.Hour), Source: "git", Kind: event.KindContext, Repo: "/r", Key: "k1"}} {
		if err := s.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AppendBatch(ctx, []event.Event{presenceAt(archiveFrom.Add(3 * time.Hour))}); err != nil {
		t.Fatal(err)
	}
	if n := countEventsBetween(t, s, archiveFrom, archiveTo); n != 0 {
		t.Errorf("%d events were stored inside the archived day", n)
	}
	if err := s.Append(ctx, presenceAt(archiveTo)); err != nil {
		t.Fatal(err)
	}
	if n := countEventsBetween(t, s, archiveTo, archiveTo.Add(time.Hour)); n != 1 {
		t.Errorf("the day after the archived one has %d events, want 1", n)
	}
}

func TestEarliestEventAt(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	if _, found, err := s.EarliestEventAt(ctx); err != nil || found {
		t.Fatalf("empty store: found = %v, err = %v", found, err)
	}
	for _, off := range []time.Duration{2 * time.Hour, time.Hour} {
		if err := s.Append(ctx, presenceAt(archiveFrom.Add(off))); err != nil {
			t.Fatal(err)
		}
	}
	at, found, err := s.EarliestEventAt(ctx)
	if err != nil || !found || !at.Equal(archiveFrom.Add(time.Hour)) {
		t.Errorf("EarliestEventAt = %v, %v, %v", at, found, err)
	}
}
