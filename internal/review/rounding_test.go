package review

import (
	"context"
	"testing"
	"time"
)

func TestLoadDayReconcilesBookedItemsWithDayTotal(t *testing.T) {
	st := openStore(t)
	cfg := testConfig()
	cfg.MinBlockDuration = time.Minute
	seed(t, st, dayStart, 40, "ABC-1")
	seed(t, st, dayStart.Add(2*time.Hour), 6, "ABC-2")

	d, err := LoadDay(context.Background(), st, cfg, testDay)
	if err != nil {
		t.Fatal(err)
	}

	booked := 0
	for _, it := range d.Items {
		booked += it.Minutes
	}
	if booked != d.TotalMinutes || time.Duration(booked)*time.Minute != d.Rounding.Booked {
		t.Fatalf("items sum %d, TotalMinutes %d, Rounding.Booked %v", booked, d.TotalMinutes, d.Rounding.Booked)
	}
	if len(d.Rounding.Dropped) != 1 || d.Rounding.Dropped[0].Ticket != "ABC-2" {
		t.Fatalf("dropped = %+v, want ABC-2", d.Rounding.Dropped)
	}
	if d.Rounding.Raw-d.Rounding.Booked != d.Rounding.Difference() || d.Rounding.Difference() <= 0 {
		t.Fatalf("difference not explicit: %+v", d.Rounding)
	}
}
