// Package auto decides what auto mode may book. It is pure: the trust gate,
// the selection of safe items and the digest are computed from values passed
// in, with no I/O and no clock reads. Booking itself goes through the same
// ledger as reviewed sync.
package auto

import (
	"fmt"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/resolver"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
)

// Reasons an item was not booked. They are stable text stored in digests.
const (
	ReasonUnassigned = "unassigned time needs a ticket"
	ReasonOverCap    = "day total exceeds the daily cap: a capped day needs a human"
)

// LockReason says why auto mode is locked.
type LockReason int

// Lock reasons.
const (
	// Unlocked means auto mode may book.
	Unlocked LockReason = iota
	// LockedDisabled means [auto] enabled is false.
	LockedDisabled
	// LockedTrackRecord means the clean-day streak is below the threshold.
	LockedTrackRecord
)

// RequiredDays returns the configured number of clean reviewed days. A value
// below one (an unset configuration) falls back to the default, so a missing
// value can never unlock auto mode early.
func RequiredDays(cfg config.Auto) int {
	if cfg.TrustedDays < 1 {
		return config.DefaultAutoTrustedDays
	}
	return cfg.TrustedDays
}

// Gate decides whether auto mode may book: it must be enabled and the streak
// of consecutive clean reviewed days must reach the required days.
func Gate(cfg config.Auto, streak int) LockReason {
	switch {
	case !cfg.Enabled:
		return LockedDisabled
	case streak < RequiredDays(cfg):
		return LockedTrackRecord
	}
	return Unlocked
}

// Entry is one item together with the tier it is booked or skipped at.
type Entry struct {
	Item sessionizer.DayItem
	// Tier is the lowest tier over the item's blocks.
	Tier resolver.Tier
	// Reason explains skipped entries; empty for items to book.
	Reason string
}

// Selection is the outcome of Select for one day.
type Selection struct {
	// Book holds the items auto mode may book.
	Book []Entry
	// Skipped holds every other item with the reason.
	Skipped []Entry
	// CapExceeded is set when the day total is above the daily cap; then
	// nothing is booked.
	CapExceeded  bool
	TotalMinutes int
	CapMinutes   int
}

// Items returns the items to book.
func (s Selection) Items() []sessionizer.DayItem {
	out := make([]sessionizer.DayItem, 0, len(s.Book))
	for _, e := range s.Book {
		out = append(out, e.Item)
	}
	return out
}

// TierOf returns the lowest tier over all blocks of a ticket.
type TierOf func(ticket string) (resolver.Tier, bool)

// rank orders tiers from most to least trustworthy.
func rank(t resolver.Tier) int {
	switch t {
	case resolver.TierExplicit:
		return 0
	case resolver.TierRule:
		return 1
	case resolver.TierLearned:
		return 2
	case resolver.TierSuggested:
		return 3
	}
	return 4
}

// LowestTiers builds a TierOf from the blocks of a day. Manually switched or
// overridden blocks already carry the explicit tier.
func LowestTiers(blocks []sessionizer.Block) TierOf {
	lowest := map[string]resolver.Tier{}
	for _, b := range blocks {
		if cur, ok := lowest[b.Ticket]; !ok || rank(b.Tier) > rank(cur) {
			lowest[b.Ticket] = b.Tier
		}
	}
	return func(ticket string) (resolver.Tier, bool) {
		t, ok := lowest[ticket]
		return t, ok
	}
}

// Select picks the items auto mode may book. An item is booked only when it
// has a ticket and every one of its blocks is explicit or rule; a single
// lower block skips the whole item. When the day total (including unassigned
// time) exceeds capMinutes, nothing is booked. A capMinutes of zero disables
// the cap.
func Select(items []sessionizer.DayItem, tierOf TierOf, capMinutes int) Selection {
	sel := Selection{CapMinutes: capMinutes}
	for _, it := range items {
		sel.TotalMinutes += it.Minutes
	}
	sel.CapExceeded = capMinutes > 0 && sel.TotalMinutes > capMinutes
	for _, it := range items {
		tier := resolver.TierUnknown
		if t, ok := tierOf(it.Ticket); ok {
			tier = t
		}
		e := Entry{Item: it, Tier: tier}
		switch {
		case it.Ticket == "":
			e.Reason = ReasonUnassigned
		case sel.CapExceeded:
			e.Reason = ReasonOverCap
		case tier != resolver.TierExplicit && tier != resolver.TierRule:
			e.Reason = fmt.Sprintf("tier %s is below rule: needs review", tier)
		}
		if e.Reason != "" {
			sel.Skipped = append(sel.Skipped, e)
			continue
		}
		sel.Book = append(sel.Book, e)
	}
	return sel
}
