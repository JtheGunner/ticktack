package localday

import (
	"testing"
	"time"

	"cloud.google.com/go/civil"
)

func mustLoad(t *testing.T, zone string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(zone)
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	return loc
}

func utc(year int, month time.Month, day, hour int) time.Time {
	return time.Date(year, month, day, hour, 0, 0, 0, time.UTC)
}

func TestBounds(t *testing.T) {
	tests := []struct {
		name     string
		zone     string
		date     civil.Date
		wantFrom time.Time
		wantTo   time.Time
	}{
		{"utc", "UTC", civil.Date{Year: 2026, Month: 3, Day: 2}, utc(2026, 3, 2, 0), utc(2026, 3, 3, 0)},
		{"month end", "UTC", civil.Date{Year: 2026, Month: 12, Day: 31}, utc(2026, 12, 31, 0), utc(2027, 1, 1, 0)},
		{"zurich 23 h day", "Europe/Zurich", civil.Date{Year: 2026, Month: 3, Day: 29}, utc(2026, 3, 28, 23), utc(2026, 3, 29, 22)},
		{"zurich 25 h day", "Europe/Zurich", civil.Date{Year: 2026, Month: 10, Day: 25}, utc(2026, 10, 24, 22), utc(2026, 10, 25, 23)},
		// Clock jumps 00:00 -> 01:00: the day starts at 01:00.
		{"santiago no midnight", "America/Santiago", civil.Date{Year: 2026, Month: 9, Day: 6}, utc(2026, 9, 6, 4), utc(2026, 9, 7, 3)},
		{"santiago day before", "America/Santiago", civil.Date{Year: 2026, Month: 9, Day: 5}, utc(2026, 9, 5, 4), utc(2026, 9, 6, 4)},
		{"havana no midnight", "America/Havana", civil.Date{Year: 2026, Month: 3, Day: 8}, utc(2026, 3, 8, 5), utc(2026, 3, 9, 4)},
		{"azores no midnight", "Atlantic/Azores", civil.Date{Year: 2026, Month: 3, Day: 29}, utc(2026, 3, 29, 1), utc(2026, 3, 30, 0)},
		// Clock falls back 24:00 -> 23:00: the day before has 25 hours.
		{"santiago 25 h day", "America/Santiago", civil.Date{Year: 2026, Month: 4, Day: 4}, utc(2026, 4, 4, 3), utc(2026, 4, 5, 4)},
		// Clock falls back 01:00 -> 00:00: midnight happens twice, the day
		// starts at the first one.
		{"havana two midnights", "America/Havana", civil.Date{Year: 2025, Month: 11, Day: 2}, utc(2025, 11, 2, 4), utc(2025, 11, 3, 5)},
		{"azores two midnights", "Atlantic/Azores", civil.Date{Year: 2026, Month: 10, Day: 25}, utc(2026, 10, 25, 0), utc(2026, 10, 26, 1)},
		// Samoa skipped 2011-12-30: the day is empty.
		{"apia skipped day", "Pacific/Apia", civil.Date{Year: 2011, Month: 12, Day: 30}, utc(2011, 12, 30, 10), utc(2011, 12, 30, 10)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			from, to := Bounds(tc.date, mustLoad(t, tc.zone))
			if !from.Equal(tc.wantFrom) || !to.Equal(tc.wantTo) {
				t.Fatalf("got [%s, %s), want [%s, %s)", from.UTC(), to.UTC(), tc.wantFrom, tc.wantTo)
			}
		})
	}
}

func TestStartOfNilLocationIsUTC(t *testing.T) {
	got := StartOf(civil.Date{Year: 2026, Month: 3, Day: 2}, nil)
	if !got.Equal(utc(2026, 3, 2, 0)) {
		t.Fatalf("got %s", got)
	}
}

func TestNext(t *testing.T) {
	tests := []struct {
		name string
		zone string
		at   time.Time
		want time.Time
	}{
		{"mid day", "UTC", utc(2026, 3, 2, 15), utc(2026, 3, 3, 0)},
		{"on the boundary", "UTC", utc(2026, 3, 2, 0), utc(2026, 3, 3, 0)},
		{"last nanosecond", "UTC", utc(2026, 3, 3, 0).Add(-time.Nanosecond), utc(2026, 3, 3, 0)},
		{"before missing midnight", "America/Santiago", utc(2026, 9, 6, 3), utc(2026, 9, 6, 4)},
		{"at missing midnight", "America/Santiago", utc(2026, 9, 6, 4), utc(2026, 9, 7, 3)},
		{"before repeated hour", "America/Santiago", utc(2026, 4, 5, 2), utc(2026, 4, 5, 4)},
		{"in repeated hour", "America/Santiago", utc(2026, 4, 5, 3), utc(2026, 4, 5, 4)},
		{"first of two midnights", "America/Havana", utc(2025, 11, 2, 4), utc(2025, 11, 3, 5)},
		{"second of two midnights", "America/Havana", utc(2025, 11, 2, 5), utc(2025, 11, 3, 5)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Next(tc.at, mustLoad(t, tc.zone))
			if !got.Equal(tc.want) {
				t.Fatalf("got %s, want %s", got.UTC(), tc.want)
			}
		})
	}
}

// TestNextAdvancesAndMatchesStartOf walks whole years hour by hour: the
// boundary after an instant is strictly later, at most 25 hours away, and is
// the start of the following local date.
func TestNextAdvancesAndMatchesStartOf(t *testing.T) {
	zones := []string{"UTC", "Europe/Zurich", "America/Santiago", "America/Havana", "Atlantic/Azores", "Australia/Lord_Howe", "Asia/Kathmandu"}
	for _, zone := range zones {
		t.Run(zone, func(t *testing.T) {
			loc := mustLoad(t, zone)
			end := utc(2027, 1, 2, 0)
			for at := utc(2024, 12, 31, 0).Add(17 * time.Minute); at.Before(end); at = at.Add(time.Hour) {
				next := Next(at, loc)
				if !next.After(at) {
					t.Fatalf("Next(%s) = %s is not after it", at, next.UTC())
				}
				if next.Sub(at) > 25*time.Hour {
					t.Fatalf("Next(%s) = %s is more than a day away", at, next.UTC())
				}
				date := civil.DateOf(at.In(loc))
				if want := StartOf(date.AddDays(1), loc); !next.Equal(want) {
					t.Fatalf("Next(%s) = %s, StartOf(%s) = %s", at, next.UTC(), date.AddDays(1), want.UTC())
				}
				if got := civil.DateOf(next.In(loc)); got != date.AddDays(1) {
					t.Fatalf("Next(%s) = %s falls on %s", at, next.UTC(), got)
				}
				if got := civil.DateOf(next.Add(-time.Nanosecond).In(loc)); got != date {
					t.Fatalf("instant before Next(%s) falls on %s", at, got)
				}
			}
		})
	}
}
