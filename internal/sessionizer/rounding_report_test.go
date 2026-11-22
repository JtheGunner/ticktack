package sessionizer

import (
	"slices"
	"testing"
	"time"
)

func minutesByTicket(items []DayItem) map[string]int {
	out := map[string]int{}
	for _, it := range items {
		out[it.Ticket] = it.Minutes
	}
	return out
}

// A booked item must not change because only the unassigned time changed:
// unassigned time does not compete for the remainder units.
func TestAggregateAssignedItemsIgnoreUnassignedTime(t *testing.T) {
	step := rounding(15*time.Minute, 0)
	tests := []struct {
		name       string
		unassigned time.Duration
		wantUnass  int
	}{
		{"no unassigned time", 0, 0},
		{"small unassigned time", 10 * time.Minute, 15},
		{"larger unassigned time", 14 * time.Minute, 15},
		{"below half a step", 5 * time.Minute, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocks := []Block{blockAt(base, 38*time.Minute, "AAA-1")}
			if tt.unassigned > 0 {
				blocks = append(blocks, blockAt(base.Add(2*time.Hour), tt.unassigned, ""))
			}
			got := minutesByTicket(Aggregate(blocks, step, time.UTC))
			if got["AAA-1"] != 45 {
				t.Fatalf("AAA-1 = %d min, want 45 regardless of unassigned time: %v", got["AAA-1"], got)
			}
			if got[""] != tt.wantUnass {
				t.Fatalf("unassigned = %d min, want %d: %v", got[""], tt.wantUnass, got)
			}
		})
	}
}

func TestAggregateReportListsDroppedItems(t *testing.T) {
	blocks := []Block{
		blockAt(base, 40*time.Minute, "BBB-2"),
		blockAt(base.Add(time.Hour), 6*time.Minute, "AAA-1"),
	}
	r := AggregateReport(blocks, rounding(15*time.Minute, 0), time.UTC)

	if len(r.Items) != 1 || r.Items[0].Ticket != "BBB-2" || r.Items[0].Minutes != 45 {
		t.Fatalf("items = %+v", r.Items)
	}
	if len(r.Days) != 1 {
		t.Fatalf("days = %+v", r.Days)
	}
	d := r.Days[0]
	if len(d.Dropped) != 1 || d.Dropped[0].Ticket != "AAA-1" || d.Dropped[0].Raw != 6*time.Minute {
		t.Fatalf("dropped = %+v", d.Dropped)
	}
	// The day total reconciles with the bookable items through an explicit
	// difference.
	if d.Raw != 46*time.Minute || d.Booked != 45*time.Minute || d.Difference() != time.Minute {
		t.Fatalf("raw %v booked %v difference %v", d.Raw, d.Booked, d.Difference())
	}
	var sum time.Duration
	for _, it := range r.Items {
		sum += time.Duration(it.Minutes) * time.Minute
	}
	if sum != d.Booked {
		t.Fatalf("booked %v != sum of items %v", d.Booked, sum)
	}
}

func TestAggregateReportDifferenceCases(t *testing.T) {
	tests := []struct {
		name        string
		blocks      []Block
		wantDiff    time.Duration
		wantDropped []string
	}{
		{"exact multiple, nothing lost", []Block{blockAt(base, 30*time.Minute, "AAA-1")}, 0, nil},
		{"rounded up is a negative difference", []Block{blockAt(base, 10*time.Minute, "AAA-1")}, -5 * time.Minute, nil},
		{
			"several tickets plus unassigned, one of each rounds to zero",
			[]Block{
				blockAt(base, 40*time.Minute, "AAA-1"),
				blockAt(base.Add(time.Hour), 6*time.Minute, "BBB-2"),
				blockAt(base.Add(2*time.Hour), 30*time.Minute, "CCC-3"),
				blockAt(base.Add(3*time.Hour), 6*time.Minute, ""),
			},
			7 * time.Minute, []string{"", "BBB-2"},
		},
		{"unassigned time rounds on its own", []Block{blockAt(base, 40*time.Minute, "AAA-1"), blockAt(base.Add(time.Hour), 6*time.Minute, "")}, time.Minute, []string{""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := AggregateReport(tt.blocks, rounding(15*time.Minute, 0), time.UTC)
			d := r.Days[0]
			if d.Difference() != tt.wantDiff {
				t.Fatalf("difference = %v, want %v (%+v)", d.Difference(), tt.wantDiff, d)
			}
			var got []string
			for _, dr := range d.Dropped {
				got = append(got, dr.Ticket)
			}
			if !slices.Equal(got, tt.wantDropped) {
				t.Fatalf("dropped = %v, want %v", got, tt.wantDropped)
			}
		})
	}
}
