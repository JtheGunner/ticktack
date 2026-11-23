package review

import (
	"testing"
	"time"

	"cloud.google.com/go/civil"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("time zone %s unavailable: %v", name, err)
	}
	return loc
}

// TestTrackRecordCountsOnlyTimelyReviews pins the review-day rule: a day
// counts only when its items were confirmed on that calendar day or the next
// one, in the configured time zone.
func TestTrackRecordCountsOnlyTimelyReviews(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	auckland := mustLoc(t, "Pacific/Auckland")
	tests := []struct {
		name      string
		loc       *time.Location
		day       civil.Date
		sessionAt time.Time // instant of the review session
		corrected bool
		want      int
	}{
		{"same day", time.UTC, civil.Date{Year: 2026, Month: 1, Day: 5}, time.Date(2026, 1, 5, 19, 0, 0, 0, time.UTC), false, 1},
		{"next day 23:59 counts", time.UTC, civil.Date{Year: 2026, Month: 1, Day: 5}, time.Date(2026, 1, 6, 23, 59, 0, 0, time.UTC), false, 1},
		{"day+2 00:00 does not count", time.UTC, civil.Date{Year: 2026, Month: 1, Day: 5}, time.Date(2026, 1, 7, 0, 0, 0, 0, time.UTC), false, 0},
		{"week-old catch-up does not count", time.UTC, civil.Date{Year: 2026, Month: 1, Day: 5}, time.Date(2026, 1, 12, 9, 0, 0, 0, time.UTC), false, 0},
		// 2026-01-07 00:00 in Auckland (UTC+13) is still 2026-01-06 11:00 UTC
		// of the previous UTC day: judged in Auckland it is day+2.
		{"time zone decides: day+2 in Auckland", auckland, civil.Date{Year: 2026, Month: 1, Day: 5}, time.Date(2026, 1, 6, 11, 0, 0, 0, time.UTC), false, 0},
		{"time zone decides: day+1 23:59 in Auckland", auckland, civil.Date{Year: 2026, Month: 1, Day: 5}, time.Date(2026, 1, 6, 10, 59, 0, 0, time.UTC), false, 1},
		// DST starts 2026-03-08 in New York (23 hour day). The next day ends
		// at 03-09 23:59 EDT = 03-10 03:59 UTC.
		{"DST day reviewed next evening counts", ny, civil.Date{Year: 2026, Month: 3, Day: 8}, time.Date(2026, 3, 10, 3, 59, 0, 0, time.UTC), false, 1},
		{"DST day reviewed at day+2 midnight does not count", ny, civil.Date{Year: 2026, Month: 3, Day: 8}, time.Date(2026, 3, 10, 4, 0, 0, 0, time.UTC), false, 0},
		// DST ends 2026-11-01 (25 hour day): 11-01 00:00 EDT = 04:00 UTC.
		{"fall-back day: next day 23:59 counts", ny, civil.Date{Year: 2026, Month: 11, Day: 1}, time.Date(2026, 11, 3, 4, 59, 0, 0, time.UTC), false, 1},
		{"fall-back day: day+2 00:00 does not count", ny, civil.Date{Year: 2026, Month: 11, Day: 1}, time.Date(2026, 11, 3, 5, 0, 0, 0, time.UTC), false, 0},
		{"late corrected review still breaks the streak", time.UTC, civil.Date{Year: 2026, Month: 1, Day: 5}, time.Date(2026, 1, 12, 9, 0, 0, 0, time.UTC), true, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newGate(t)
			g.cfg.Location = tt.loc
			// An older timely clean day, so a broken streak is visible.
			g.reviewClean(civil.Date{Year: 2025, Month: 12, Day: 1})
			g.work(tt.day, 9, 60, "ABC-1")
			acts := []func(*Session) error{confirmAll}
			if tt.corrected {
				acts = []func(*Session) error{reassign(1, "XYZ-9"), confirmAll}
			}
			g.review(tt.day, tt.sessionAt, acts...)
			want := tt.want
			if !tt.corrected {
				want++ // the older timely day
			}
			if n := g.streak(time.Date(2026, 12, 31, 12, 0, 0, 0, tt.loc)); n != want {
				t.Errorf("streak = %d, want %d", n, want)
			}
		})
	}
}
