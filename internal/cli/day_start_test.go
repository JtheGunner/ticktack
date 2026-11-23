package cli

import (
	"context"
	"reflect"
	"testing"
	"time"

	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/review"
)

func TestDayStart(t *testing.T) {
	santiago, err := time.LoadLocation("America/Santiago")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	// The clock jumps from 00:00 to 01:00 at this instant.
	gapStart := time.Date(2026, 9, 6, 4, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		day  string
		now  time.Time
		loc  *time.Location
		want time.Time
	}{
		{"default is today", "", time.Date(2026, 3, 2, 15, 30, 0, 0, time.UTC), time.UTC, time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)},
		{"explicit day", "2026-03-01", time.Date(2026, 3, 2, 15, 30, 0, 0, time.UTC), time.UTC, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)},
		{"today without midnight", "", gapStart.Add(5 * time.Hour), santiago, gapStart},
		{"explicit day without midnight", "2026-09-06", gapStart.Add(-48 * time.Hour), santiago, gapStart},
		{"day after the one without midnight", "2026-09-07", gapStart, santiago, gapStart.Add(23 * time.Hour)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := dayStart(tc.day, tc.now, tc.loc)
			if err != nil {
				t.Fatal(err)
			}
			if !got.Equal(tc.want) {
				t.Fatalf("got %s, want %s", got.UTC(), tc.want)
			}
			if tc.day != "" && got.Format(dayLayout) != tc.day {
				t.Errorf("start is labelled %s, want %s", got.Format(dayLayout), tc.day)
			}
		})
	}
}

func TestDayStartRejectsMalformedDay(t *testing.T) {
	for _, day := range []string{"06.09.2026", "2026-13-01", "today"} {
		if _, err := dayStart(day, dailyNow, time.UTC); err == nil {
			t.Errorf("dayStart(%q) returned no error", day)
		}
	}
}

// TestDeriveDayAndLoadDayShareWindowWithoutLocalMidnight seeds presence
// across a missing midnight: today and review must cut it at the same instant.
func TestDeriveDayAndLoadDayShareWindowWithoutLocalMidnight(t *testing.T) {
	santiago, err := time.LoadLocation("America/Santiago")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	deps, st, _ := newDailyEnv(t, "")
	ctx := context.Background()
	gapStart := time.Date(2026, 9, 6, 4, 0, 0, 0, time.UTC)
	mustAppend(t, st, shellContext(gapStart.Add(-time.Hour), "/work/api", "ABC-7-fix"))
	mustAppend(t, st, morningPresence(gapStart.Add(-30*time.Minute), 90)...)

	cfg := loadTestConfig(t, deps)
	cfg.Location = santiago
	start, err := dayStart("2026-09-06", dailyNow, santiago)
	if err != nil {
		t.Fatal(err)
	}
	derived, err := deriveDay(ctx, nopCloseStore{st}, cfg, start)
	if err != nil {
		t.Fatal(err)
	}
	d, err := review.LoadDay(ctx, st, cfg, civil.Date{Year: 2026, Month: 9, Day: 6})
	if err != nil {
		t.Fatal(err)
	}
	if len(derived.Blocks) != 1 || len(d.Blocks) != 1 || !reflect.DeepEqual(derived.Blocks[0], d.Blocks[0]) {
		t.Fatalf("today blocks %+v, review blocks %+v", derived.Blocks, d.Blocks)
	}
	if b := derived.Blocks[0]; !b.Start.Equal(gapStart) || !b.End.Equal(gapStart.Add(time.Hour)) {
		t.Errorf("block = %s..%s, want it cut at the day start %s", b.Start.UTC(), b.End.UTC(), gapStart)
	}
}
