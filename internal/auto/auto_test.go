package auto

import (
	"testing"

	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/ledger"
	"github.com/JtheGunner/ticktack/internal/resolver"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
)

var day = civil.Date{Year: 2026, Month: 1, Day: 5}

func item(ticket string, minutes int) sessionizer.DayItem {
	return sessionizer.DayItem{Date: day, Ticket: ticket, Type: "Development", Minutes: minutes, Description: ticket + " - 1 block"}
}

func block(ticket string, tier resolver.Tier) sessionizer.Block {
	return sessionizer.Block{Ticket: ticket, Tier: tier}
}

func TestAutoModeSkipsLowTierBlocks(t *testing.T) {
	blocks := []sessionizer.Block{
		block("EXP-1", resolver.TierExplicit),
		block("RUL-1", resolver.TierRule),
		block("LRN-1", resolver.TierLearned),
		block("SUG-1", resolver.TierSuggested),
		block("UNK-1", resolver.TierUnknown),
		// MIX-1 has one good block and one learned block: the whole item is skipped.
		block("MIX-1", resolver.TierExplicit),
		block("MIX-1", resolver.TierLearned),
		block("MIX-1", resolver.TierRule),
	}
	items := []sessionizer.DayItem{
		item("", 15), item("EXP-1", 30), item("RUL-1", 30), item("LRN-1", 30),
		item("SUG-1", 30), item("UNK-1", 30), item("MIX-1", 45),
	}
	sel := Select(items, LowestTiers(blocks), 600)
	var booked, skipped []string
	for _, e := range sel.Book {
		booked = append(booked, e.Item.Ticket)
	}
	for _, e := range sel.Skipped {
		skipped = append(skipped, e.Item.Ticket)
		if e.Reason == "" {
			t.Errorf("skipped %q without reason", e.Item.Ticket)
		}
	}
	if len(booked) != 2 || booked[0] != "EXP-1" || booked[1] != "RUL-1" {
		t.Fatalf("booked = %v", booked)
	}
	if len(skipped) != 5 {
		t.Fatalf("skipped = %v", skipped)
	}
	if sel.Skipped[len(sel.Skipped)-1].Tier != resolver.TierLearned {
		t.Errorf("MIX-1 must report its lowest tier, got %v", sel.Skipped[len(sel.Skipped)-1].Tier)
	}
}

func TestAutoModeSkipsItemWithoutBlocks(t *testing.T) {
	sel := Select([]sessionizer.DayItem{item("ABC-1", 30)}, LowestTiers(nil), 600)
	if len(sel.Book) != 0 || len(sel.Skipped) != 1 {
		t.Fatalf("selection = %+v", sel)
	}
}

func TestAutoModeRespectsDailyCap(t *testing.T) {
	blocks := []sessionizer.Block{block("EXP-1", resolver.TierExplicit), block("RUL-1", resolver.TierRule)}
	items := []sessionizer.DayItem{item("EXP-1", 300), item("RUL-1", 301)}
	sel := Select(items, LowestTiers(blocks), 600)
	if !sel.CapExceeded || len(sel.Book) != 0 || len(sel.Skipped) != 2 {
		t.Fatalf("over the cap nothing may be booked: %+v", sel)
	}
	if sel.Skipped[0].Reason != ReasonOverCap {
		t.Errorf("reason = %q", sel.Skipped[0].Reason)
	}
	// Exactly at the cap is within it; unassigned time counts toward the total.
	sel = Select([]sessionizer.DayItem{item("EXP-1", 300), item("RUL-1", 300)}, LowestTiers(blocks), 600)
	if sel.CapExceeded || len(sel.Book) != 2 {
		t.Fatalf("at the cap: %+v", sel)
	}
	sel = Select([]sessionizer.DayItem{item("EXP-1", 300), item("", 301)}, LowestTiers(blocks), 600)
	if !sel.CapExceeded || len(sel.Book) != 0 {
		t.Fatalf("unassigned time must count: %+v", sel)
	}
}

func TestGate(t *testing.T) {
	on := config.Auto{Enabled: true, TrustedDays: 10}
	tests := []struct {
		name   string
		cfg    config.Auto
		streak int
		want   LockReason
	}{
		{"N-1 days locked", on, 9, LockedTrackRecord},
		{"N days unlocks", on, 10, Unlocked},
		{"more than N", on, 25, Unlocked},
		{"disabled stays locked with perfect record", config.Auto{TrustedDays: 10}, 100, LockedDisabled},
		{"unset threshold falls back to default", config.Auto{Enabled: true}, 9, LockedTrackRecord},
		{"custom threshold", config.Auto{Enabled: true, TrustedDays: 3}, 3, Unlocked},
	}
	for _, tt := range tests {
		if got := Gate(tt.cfg, tt.streak); got != tt.want {
			t.Errorf("%s: Gate = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestBuildDigestListsBookedAndSkippedWithTiers(t *testing.T) {
	blocks := []sessionizer.Block{block("EXP-1", resolver.TierExplicit), block("RUL-1", resolver.TierRule), block("LRN-1", resolver.TierLearned)}
	items := []sessionizer.DayItem{item("EXP-1", 30), item("RUL-1", 45), item("LRN-1", 15)}
	sel := Select(items, LowestTiers(blocks), 600)
	report := ledger.Report{
		Items: []ledger.ItemResult{
			{Key: "EXP-1:2026-01-05:Development", Action: ledger.ActionCreated, Minutes: 30},
			{Key: "RUL-1:2026-01-05:Development", Action: ledger.ActionUpdated, Minutes: 45},
		},
		Created: 1, Updated: 1,
	}
	d := BuildDigest(day, sel, report, false)
	if len(d.Entries) != 3 || d.BookedMinutes != 75 || d.Skipped != 1 || !d.Changed() {
		t.Fatalf("digest = %+v", d)
	}
	first := d.Entries[0]
	if first.Ticket != "EXP-1" || first.Tier != resolver.TierExplicit || first.Status != "created" || first.Minutes != 30 || first.Date != day {
		t.Errorf("first entry = %+v", first)
	}
	last := d.Entries[2]
	if last.Ticket != "LRN-1" || last.Status != "skipped" || last.Reason == "" || last.Tier != resolver.TierLearned {
		t.Errorf("skipped entry = %+v", last)
	}
}
