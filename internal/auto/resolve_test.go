package auto

import (
	"testing"
	"time"

	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/resolver"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
)

func TestSelectionResolveMergesItemsThatShareARewrittenTicket(t *testing.T) {
	day := civil.Date{Year: 2026, Month: time.March, Day: 2}
	entry := func(ticket string, minutes int) Entry {
		return Entry{Item: sessionizer.DayItem{Date: day, Ticket: ticket, Type: "Development", Minutes: minutes}, Tier: resolver.TierRule}
	}
	sel := Selection{Book: []Entry{entry("A-7", 30), entry("A:default", 45)}}

	got := sel.Resolve(map[string]string{"A:default": "A-7"}, nil)

	if len(got.Book) != 1 || got.Book[0].Item.Ticket != "A-7" || got.Book[0].Item.Minutes != 75 {
		t.Fatalf("Book = %+v, want one A-7 entry of 75 minutes", got.Book)
	}
}

func TestSelectionResolveRewritesAndSkips(t *testing.T) {
	day := civil.Date{Year: 2026, Month: time.March, Day: 2}
	entry := func(ticket string) Entry {
		return Entry{Item: sessionizer.DayItem{Date: day, Ticket: ticket, Type: "Development", Minutes: 30}, Tier: resolver.TierRule}
	}
	sel := Selection{
		Book:         []Entry{entry("A:default"), entry("B:default"), entry("ABC-1")},
		Skipped:      []Entry{{Item: sessionizer.DayItem{Date: day, Minutes: 15}, Reason: ReasonUnassigned}},
		TotalMinutes: 105,
	}

	got := sel.Resolve(map[string]string{"A:default": "A-1"}, []string{"B:default"})

	if len(got.Book) != 2 || got.Book[0].Item.Ticket != "A-1" || got.Book[1].Item.Ticket != "ABC-1" {
		t.Fatalf("Book = %+v, want A-1 and ABC-1", got.Book)
	}
	if got.Book[0].Tier != resolver.TierRule {
		t.Errorf("rewriting lost the tier: %+v", got.Book[0])
	}
	if len(got.Skipped) != 2 || got.Skipped[1].Item.Ticket != "B:default" || got.Skipped[1].Reason != ReasonStandardTask {
		t.Fatalf("Skipped = %+v, want the unassigned item then B:default with the standard task reason", got.Skipped)
	}
	if got.TotalMinutes != 105 {
		t.Errorf("TotalMinutes = %d, want it unchanged", got.TotalMinutes)
	}
	if len(sel.Book) != 3 {
		t.Errorf("Resolve mutated its receiver: %+v", sel.Book)
	}
}
