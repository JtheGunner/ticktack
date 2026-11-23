package review

import (
	"context"
	"testing"

	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/sessionizer"
	"github.com/JtheGunner/ticktack/internal/store"
)

// bookDay records one ledger row for day, as a sync would.
func (g *gate) bookDay(day civil.Date, issue string, minutes int) {
	g.t.Helper()
	ctx := context.Background()
	at := g.at(day, 20)
	runID, err := g.st.BeginSyncRun(ctx, at)
	if err != nil {
		g.t.Fatal(err)
	}
	row := store.LedgerRow{Issue: issue, Date: day.String(), Type: sessionizer.DefaultWorkItemType, WorkItemID: issue + "/1", Minutes: minutes, ContentHash: "h", BookedAt: at}
	entry := store.SyncEntry{Issue: issue, Date: day.String(), Type: row.Type, Action: "created", WorkItemID: row.WorkItemID, NewMinutes: minutes}
	if err := g.st.RecordBooking(ctx, runID, row, entry); err != nil {
		g.t.Fatal(err)
	}
}

// archive deletes the events of day the way the retention cleanup does.
func (g *gate) archive(day civil.Date) {
	g.t.Helper()
	from := g.at(day, 0)
	req := store.ArchiveRequest{Day: day.String(), From: from, To: from.AddDate(0, 0, 1), ArchivedAt: g.at(testDay.AddDays(60), 12)}
	if _, err := g.st.ArchiveDay(context.Background(), req); err != nil {
		g.t.Fatal(err)
	}
}

// reviewCleanArchived reviews, books and archives an hour for ABC-1 on day.
func (g *gate) reviewCleanArchived(day civil.Date) {
	g.t.Helper()
	g.reviewClean(day)
	g.bookDay(day, "ABC-1", 60)
	g.archive(day)
}

// An archived day cannot be re-derived, so it is judged from its confirmations
// and ledger rows: it counts when it was clean, and it never ends the streak
// on its own.
func TestTrackRecordArchivedDays(t *testing.T) {
	tests := []struct {
		name  string
		build func(g *gate)
		want  int
	}{
		{"archived clean booked day counts", func(g *gate) {
			g.reviewClean(dayBefore(0))
			g.reviewCleanArchived(dayBefore(1))
			g.reviewClean(dayBefore(2))
		}, 3},
		{"archived day among live days does not break the streak", func(g *gate) {
			g.reviewClean(dayBefore(0))
			g.reviewCleanArchived(dayBefore(1))
		}, 2},
		{"archived day without a ledger row is skipped", func(g *gate) {
			g.reviewClean(dayBefore(0))
			g.reviewClean(dayBefore(1))
			g.archive(dayBefore(1))
			g.reviewClean(dayBefore(2))
		}, 2},
		{"archived day booked with other minutes is skipped", func(g *gate) {
			g.reviewClean(dayBefore(0))
			g.reviewClean(dayBefore(1))
			g.bookDay(dayBefore(1), "ABC-1", 45)
			g.archive(dayBefore(1))
			g.reviewClean(dayBefore(2))
		}, 2},
		{"archived day with an extra ledger row is skipped", func(g *gate) {
			g.reviewClean(dayBefore(0))
			g.reviewClean(dayBefore(1))
			g.bookDay(dayBefore(1), "ABC-1", 60)
			g.bookDay(dayBefore(1), "ABC-9", 15)
			g.archive(dayBefore(1))
		}, 1},
		{"archived day confirmed too late is skipped", func(g *gate) {
			g.reviewClean(dayBefore(0))
			g.work(dayBefore(1), 9, 60, "ABC-1")
			g.review(dayBefore(1), g.at(dayBefore(-1), 18), confirmAll)
			g.bookDay(dayBefore(1), "ABC-1", 60)
			g.archive(dayBefore(1))
		}, 1},
		{"archived day with a correction ends the streak", func(g *gate) {
			g.reviewClean(dayBefore(0))
			g.work(dayBefore(1), 9, 60, "ABC-1")
			g.review(dayBefore(1), g.at(dayBefore(1), 18), reassign(1, "XYZ-9"), confirmAll)
			g.bookDay(dayBefore(1), "XYZ-9", 60)
			g.archive(dayBefore(1))
			g.reviewClean(dayBefore(2))
		}, 1},
		{"an archived day older than a correction is not reached", func(g *gate) {
			g.reviewCleanArchived(dayBefore(3))
			g.ruleChange(g.at(dayBefore(2), 10), store.RuleEdited)
			g.reviewClean(dayBefore(1))
		}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newGate(t)
			tt.build(g)
			if got := g.streak(fixedNow); got != tt.want {
				t.Fatalf("streak = %d, want %d", got, tt.want)
			}
		})
	}
}

// Archiving a day changes nothing about the streak: the same history gives
// the same number before and after the cleanup.
func TestTrackRecordIsUnchangedByArchiving(t *testing.T) {
	g := newGate(t)
	for n := 0; n < 4; n++ {
		g.reviewClean(dayBefore(n))
		g.bookDay(dayBefore(n), "ABC-1", 60)
	}
	before := g.streak(fixedNow)
	g.archive(dayBefore(3))
	g.archive(dayBefore(2))
	if after := g.streak(fixedNow); after != before || after != 4 {
		t.Fatalf("streak before archiving %d, after %d, want 4 both times", before, after)
	}
}
