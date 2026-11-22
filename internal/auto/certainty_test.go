package auto

import (
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/resolver"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
)

// TestSelectBooksOnlyWholeItems pins the rule: an item is booked when all of
// its minutes rest on explicit or rule tiers, otherwise the whole item stays
// for review. Nothing is booked in part, and rounding never lets a small
// uncertain share slip through even when the breakdown shows it as zero.
func TestSelectBooksOnlyWholeItems(t *testing.T) {
	t0 := time.Date(2026, 1, 5, 8, 0, 0, 0, time.UTC)
	span := func(ticket string, from, length time.Duration, tier resolver.Tier) sessionizer.Block {
		start := t0.Add(from)
		return sessionizer.Block{Start: start, End: start.Add(length), Ticket: ticket, Tier: tier}
	}
	tests := []struct {
		name     string
		blocks   []sessionizer.Block
		wantBook bool
	}{
		{"all sure", []sessionizer.Block{
			span("A-1", 0, 20*time.Minute, resolver.TierExplicit),
			span("A-1", time.Hour, 25*time.Minute, resolver.TierRule),
		}, true},
		{"mostly sure, some carried", []sessionizer.Block{
			span("A-1", 0, 40*time.Minute, resolver.TierRule),
			span("A-1", time.Hour, 50*time.Minute, resolver.TierSuggested),
		}, false},
		{"uncertain share rounds to zero", []sessionizer.Block{
			span("A-1", 0, 30*time.Minute, resolver.TierRule),
			span("A-1", time.Hour, 2*time.Minute, resolver.TierLearned),
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc := sessionizer.RoundingConfig{Rounding: 15 * time.Minute, DefaultType: "Development"}
			items := sessionizer.Aggregate(tt.blocks, rc, time.UTC)
			sel := Select(items, LowestTiers(tt.blocks), 0)
			if booked := len(sel.Book) == 1; booked != tt.wantBook {
				t.Fatalf("booked = %v, want %v (items %+v, skipped %+v)", booked, tt.wantBook, items, sel.Skipped)
			}
			if tt.wantBook && sel.Book[0].Item.Minutes != items[0].Minutes {
				t.Fatalf("booked %d minutes, want the whole item %d", sel.Book[0].Item.Minutes, items[0].Minutes)
			}
		})
	}
}
