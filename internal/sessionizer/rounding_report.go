package sessionizer

import (
	"time"

	"cloud.google.com/go/civil"
)

// Rounded is the result of AggregateReport: the bookable items and, per day,
// how the rounding changed the time.
type Rounded struct {
	Items []DayItem
	// Days has one entry per local day that has time, ordered by date.
	Days []DaySummary
}

// Dropped is time of one ticket on one day that rounded to zero units and so
// is not in the bookable items.
type Dropped struct {
	Date civil.Date
	// Ticket is empty for unassigned time.
	Ticket string
	// Raw is the exact unrounded time.
	Raw time.Duration
}

// DaySummary reconciles the exact day total with the bookable items. The
// difference is never absorbed silently: Raw - Booked is what rounding added
// (negative) or removed (positive) on the day, and Dropped names the tickets
// whose time vanished completely.
type DaySummary struct {
	Date civil.Date
	// Raw is the exact tracked time of the day, over all tickets including
	// unassigned time.
	Raw time.Duration
	// Booked is the sum of the day's items (their rounded minutes).
	Booked time.Duration
	// Dropped lists the tickets that round to zero, in ticket order.
	Dropped []Dropped
}

// Difference is the exact tracked time minus the booked time: positive when
// rounding removed time, negative when it added time.
func (d DaySummary) Difference() time.Duration { return d.Raw - d.Booked }

// summarizeDay builds the summary of one day from its keys and the items
// roundDay produced for them. A key without an item rounded to zero.
func summarizeDay(date civil.Date, keys []dayKey, totals map[dayKey]*dayTotal, items []DayItem) DaySummary {
	s := DaySummary{Date: date}
	itemised := make(map[string]bool, len(items))
	for _, it := range items {
		itemised[it.Ticket] = true
		s.Booked += time.Duration(it.Minutes) * time.Minute
	}
	for _, k := range keys {
		raw := totals[k].duration
		s.Raw += raw
		if !itemised[k.ticket] {
			s.Dropped = append(s.Dropped, Dropped{Date: date, Ticket: k.ticket, Raw: raw})
		}
	}
	return s
}
