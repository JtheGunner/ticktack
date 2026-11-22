package review

import (
	"context"
	"testing"
	"time"

	"cloud.google.com/go/civil"
)

// TestLoadDayWithoutLocalMidnight covers a zone whose clock jumps from 00:00
// to 01:00: the day starts at 01:00, has 23 hours, and presence across the
// missing midnight is split at that instant.
func TestLoadDayWithoutLocalMidnight(t *testing.T) {
	loc, err := time.LoadLocation("America/Santiago")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	cfg := testConfig()
	cfg.Location = loc
	gapStart := time.Date(2026, 9, 6, 4, 0, 0, 0, time.UTC)
	st := openStore(t)
	seed(t, st, gapStart.Add(-30*time.Minute), 90, "ABC-1")

	tests := []struct {
		name        string
		day         civil.Date
		start, end  time.Time
		wantMinutes int
	}{
		{"day before", civil.Date{Year: 2026, Month: 9, Day: 5}, gapStart.Add(-24 * time.Hour), gapStart, 30},
		{"day without midnight", civil.Date{Year: 2026, Month: 9, Day: 6}, gapStart, gapStart.Add(23 * time.Hour), 60},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d, err := LoadDay(context.Background(), st, cfg, tc.day)
			if err != nil {
				t.Fatal(err)
			}
			if !d.Start.Equal(tc.start) || !d.End.Equal(tc.end) {
				t.Errorf("day = %s..%s, want %s..%s", d.Start.UTC(), d.End.UTC(), tc.start, tc.end)
			}
			if d.TotalMinutes != tc.wantMinutes {
				t.Errorf("total = %d min, want %d; items %+v", d.TotalMinutes, tc.wantMinutes, d.Items)
			}
		})
	}
}
