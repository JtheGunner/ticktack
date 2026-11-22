// Package localday computes the boundaries of local calendar days.
//
// A local day does not always start at 00:00 and does not always last 24
// hours: a zone that moves its clock forward at midnight has no 00:00 on that
// day, and time.Date does not promise which side of such a gap it returns.
// The functions here locate boundaries by walking the zone's transitions
// instead, so the result is the first instant of the day in every zone. They
// are pure: no I/O and no clock reads.
package localday

import (
	"time"

	"cloud.google.com/go/civil"
)

// StartOf returns the first instant whose local date in loc is date, as a
// time in loc. When the zone skips the date entirely it returns the first
// instant after it. A nil loc means UTC.
func StartOf(date civil.Date, loc *time.Location) time.Time {
	loc = orUTC(loc)
	// Noon of the previous day is a safe place to start walking from:
	// transitions happen around midnight, not around noon.
	anchor := time.Date(date.Year, date.Month, date.Day-1, 12, 0, 0, 0, loc)
	for !civil.DateOf(anchor.In(loc)).Before(date) {
		anchor = anchor.Add(-24 * time.Hour)
	}
	return firstInstantOf(date, anchor, loc).In(loc)
}

// Next returns the first day boundary in loc after t, as a time in loc. The
// result is always strictly after t. A nil loc means UTC.
func Next(t time.Time, loc *time.Location) time.Time {
	loc = orUTC(loc)
	return firstInstantOf(civil.DateOf(t.In(loc)).AddDays(1), t, loc).In(loc)
}

// Bounds returns the half-open interval [from, to) that the local date covers
// in loc. A nil loc means UTC.
func Bounds(date civil.Date, loc *time.Location) (from, to time.Time) {
	return StartOf(date, loc), StartOf(date.AddDays(1), loc)
}

// firstInstantOf walks forward from cur, whose local date must be before
// date, to the first instant whose local date is date or later.
//
// Within one zone period the offset is constant, so local midnight is a fixed
// distance away. When a transition comes first, the walk continues from the
// transition with the new offset. Every round moves past one transition, and
// a zone has finitely many of them, so the walk ends.
func firstInstantOf(date civil.Date, cur time.Time, loc *time.Location) time.Time {
	midnight := time.Date(date.Year, date.Month, date.Day, 0, 0, 0, 0, time.UTC)
	for {
		local := cur.In(loc)
		if !civil.DateOf(local).Before(date) {
			// The transition just crossed skipped local midnight.
			return cur
		}
		wall := time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), local.Minute(), local.Second(), local.Nanosecond(), time.UTC)
		candidate := cur.Add(midnight.Sub(wall))
		_, periodEnd := local.ZoneBounds()
		if periodEnd.IsZero() || candidate.Before(periodEnd) {
			return candidate
		}
		cur = periodEnd
	}
}

func orUTC(loc *time.Location) *time.Location {
	if loc == nil {
		return time.UTC
	}
	return loc
}
