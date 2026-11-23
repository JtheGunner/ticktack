package sessionizer

import (
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/resolver"
)

func tieredBlock(start time.Time, d time.Duration, tier resolver.Tier, reason string) Block {
	return Block{Start: start, End: start.Add(d), Ticket: "AAA-1", Tier: tier, Reason: reason}
}

func TestAggregateCertaintyBreakdown(t *testing.T) {
	t0 := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)
	stale := StaleReason(3 * time.Hour)
	tests := []struct {
		name   string
		blocks []Block
		step   time.Duration
		want   Certainty
	}{
		{
			name: "sure and carried time are told apart",
			blocks: []Block{
				tieredBlock(t0, 40*time.Minute, resolver.TierRule, "rule"),
				tieredBlock(t0.Add(time.Hour), 50*time.Minute, resolver.TierSuggested, stale),
			},
			step: time.Minute,
			want: Certainty{Rule: 40, Suggested: 50, Carried: 50},
		},
		{
			name: "fresh suggestion is not carried",
			blocks: []Block{
				tieredBlock(t0, 10*time.Minute, resolver.TierExplicit, "manual"),
				tieredBlock(t0.Add(time.Hour), 20*time.Minute, resolver.TierLearned, "learned"),
				tieredBlock(t0.Add(2*time.Hour), 30*time.Minute, resolver.TierSuggested, "one candidate"),
				tieredBlock(t0.Add(3*time.Hour), 5*time.Minute, resolver.TierUnknown, ""),
			},
			step: time.Minute,
			want: Certainty{Explicit: 10, Learned: 20, Suggested: 30, Unknown: 5},
		},
		{
			name: "rounding gives the whole unit to the largest remainder",
			blocks: []Block{
				tieredBlock(t0, 7*time.Minute, resolver.TierRule, "rule"),
				tieredBlock(t0.Add(time.Hour), 8*time.Minute, resolver.TierSuggested, "x"),
			},
			step: 15 * time.Minute,
			// 15 min raw rounds to one unit; the suggested part has the larger remainder.
			want: Certainty{Suggested: 15},
		},
		{
			name: "rounding tie goes to the less certain bucket",
			blocks: []Block{
				tieredBlock(t0, 5*time.Minute, resolver.TierRule, "rule"),
				tieredBlock(t0.Add(time.Hour), 5*time.Minute, resolver.TierSuggested, "x"),
			},
			step: 15 * time.Minute,
			// 10 min raw rounds up to one unit.
			want: Certainty{Suggested: 15},
		},
		{
			name: "whole units stay with their own bucket",
			blocks: []Block{
				tieredBlock(t0, 32*time.Minute, resolver.TierRule, "rule"),
				tieredBlock(t0.Add(time.Hour), 14*time.Minute, resolver.TierSuggested, stale),
			},
			step: 15 * time.Minute,
			// 46 min raw rounds to 3 units: rule 2 whole, carried 0 whole, the
			// remaining unit goes to the carried part (remainder 14 > 2).
			want: Certainty{Rule: 30, Suggested: 15, Carried: 15},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items := Aggregate(tt.blocks, rounding(tt.step, 0), time.UTC)
			if len(items) != 1 {
				t.Fatalf("got %+v", items)
			}
			it := items[0]
			if it.Certainty != tt.want {
				t.Fatalf("certainty = %+v, want %+v", it.Certainty, tt.want)
			}
			if it.Certainty.Total() != it.Minutes {
				t.Fatalf("breakdown %d does not add up to minutes %d", it.Certainty.Total(), it.Minutes)
			}
		})
	}
}

func TestAggregateCertaintySplitsAtMidnight(t *testing.T) {
	start := time.Date(2026, 3, 2, 23, 0, 0, 0, time.UTC)
	items := Aggregate([]Block{
		tieredBlock(start, 30*time.Minute, resolver.TierRule, "rule"),
		tieredBlock(start.Add(30*time.Minute), 60*time.Minute, resolver.TierSuggested, StaleReason(time.Hour)),
	}, rounding(time.Minute, 0), time.UTC)
	if len(items) != 2 {
		t.Fatalf("got %+v", items)
	}
	if want := (Certainty{Rule: 30, Suggested: 30, Carried: 30}); items[0].Certainty != want {
		t.Fatalf("day 1 = %+v, want %+v", items[0].Certainty, want)
	}
	if want := (Certainty{Suggested: 30, Carried: 30}); items[1].Certainty != want {
		t.Fatalf("day 2 = %+v, want %+v", items[1].Certainty, want)
	}
}

func TestMergeItemsSumsCertainty(t *testing.T) {
	a := DayItem{Ticket: "A-1", Type: "Development", Minutes: 30, Certainty: Certainty{Rule: 30}}
	b := DayItem{Ticket: "A-1", Type: "Development", Minutes: 20, Certainty: Certainty{Suggested: 20, Carried: 20}}
	got := MergeItems([]DayItem{a, b})
	if len(got) != 1 || got[0].Certainty != (Certainty{Rule: 30, Suggested: 20, Carried: 20}) {
		t.Fatalf("got %+v", got)
	}
}
