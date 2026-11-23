package retention

import (
	"context"
	"testing"
	"time"

	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/localday"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
	"github.com/JtheGunner/ticktack/internal/store"
)

// A day above the daily cap is cleaned only when the cap was acknowledged for
// exactly the day total; in any other case its events stay.
func TestRunKeepsADayBlockedByTheDailyCap(t *testing.T) {
	old := today.AddDays(-40)
	tests := []struct {
		name     string
		ack      int // minutes acknowledged; zero: no acknowledgement
		wantGone bool
	}{
		{"cap not acknowledged", 0, false},
		{"cap acknowledged for another total", 45, false},
		{"cap acknowledged for the day total", 60, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openStore(t)
			ctx := context.Background()
			seedDay(t, st, old, "ABC-1")
			confirmAndBook(t, st, old, "ABC-1", 60)
			if tc.ack > 0 {
				ack := store.CapAcknowledgement{Day: old.String(), TotalMinutes: tc.ack, AcknowledgedAt: now}
				if err := st.AcknowledgeCap(ctx, ack); err != nil {
					t.Fatal(err)
				}
			}
			cfg := testConfig(30)
			cfg.DailyCap = 30 * time.Minute
			before := eventsOf(t, st, old)

			if _, err := Run(ctx, st, cfg, now, false); err != nil {
				t.Fatal(err)
			}
			after := eventsOf(t, st, old)
			if tc.wantGone && after != 1 {
				t.Errorf("%d events remain, want only the context anchor", after)
			}
			if !tc.wantGone && after != before {
				t.Errorf("events %d -> %d, want the day untouched", before, after)
			}
			if _, found, _ := st.ArchivedDay(ctx, old.String()); found != tc.wantGone {
				t.Errorf("archived = %v, want %v", found, tc.wantGone)
			}
		})
	}
}

// seedLocalDay stores one hour of presence from 09:00 local time on day plus
// a presence event at 23:30 and one at 00:30 of each neighbouring day, so the
// local day bounds are exercised on both sides.
func seedLocalDay(t *testing.T, st *store.Store, day civil.Date, loc *time.Location, ticket string) {
	t.Helper()
	ctx := context.Background()
	at := func(d civil.Date, h, m int) time.Time { return time.Date(d.Year, d.Month, d.Day, h, m, 0, 0, loc) }
	events := []event.Event{{At: at(day, 9, 0), Source: "test", Kind: event.KindContext, Ticket: ticket}}
	for i := 0; i <= 60; i++ {
		events = append(events, event.Event{At: at(day, 9, i), Source: "test", Kind: event.KindPresence})
	}
	events = append(events,
		event.Event{At: at(day.AddDays(-1), 23, 30), Source: "test", Kind: event.KindPresence},
		event.Event{At: at(day.AddDays(1), 0, 30), Source: "test", Kind: event.KindPresence},
	)
	for _, e := range events {
		if err := st.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
}

func localEventCount(t *testing.T, st *store.Store, day civil.Date, loc *time.Location) int {
	t.Helper()
	from, to := localday.Bounds(day, loc)
	got, err := st.Events(context.Background(), from, to)
	if err != nil {
		t.Fatal(err)
	}
	return len(got)
}

// Days of 23 and 25 hours are measured in calendar days: a day is cleaned one
// calendar day after the retention period, never earlier, and only its own
// local events are deleted.
func TestRunAcrossDaylightSavingChanges(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Zurich")
	if err != nil {
		t.Skipf("time zone unavailable: %v", err)
	}
	tests := []struct {
		name string
		day  civil.Date
	}{
		{"spring forward, 23 hour day", civil.Date{Year: 2026, Month: time.March, Day: 29}},
		{"fall back, 25 hour day", civil.Date{Year: 2026, Month: time.October, Day: 25}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig(30)
			cfg.Location = loc
			noon := func(d civil.Date) time.Time { return time.Date(d.Year, d.Month, d.Day, 12, 0, 0, 0, loc) }

			st := openStore(t)
			ctx := context.Background()
			seedLocalDay(t, st, tc.day, loc, "ABC-1")
			confirm(t, st, tc.day, "ABC-1", 60)
			book(t, st, tc.day, "ABC-1", 60)
			before := localEventCount(t, st, tc.day, loc)
			prevBefore := localEventCount(t, st, tc.day.AddDays(-1), loc)
			nextBefore := localEventCount(t, st, tc.day.AddDays(1), loc)

			// Exactly 30 calendar days later the day is still inside the period.
			report, err := Run(ctx, st, cfg, noon(tc.day.AddDays(30)), false)
			if err != nil {
				t.Fatal(err)
			}
			if len(report.Days) != 0 || localEventCount(t, st, tc.day, loc) != before {
				t.Fatalf("day cleaned too early: report %+v", report)
			}
			// Late evening of the 30th day is still the 30th calendar day.
			late := time.Date(tc.day.AddDays(30).Year, tc.day.AddDays(30).Month, tc.day.AddDays(30).Day, 23, 59, 0, 0, loc)
			if report, err = Run(ctx, st, cfg, late, false); err != nil || len(report.Days) != 0 {
				t.Fatalf("day cleaned too early at 23:59: report %+v, %v", report, err)
			}

			// One calendar day later it is expired.
			report, err = Run(ctx, st, cfg, noon(tc.day.AddDays(31)), false)
			if err != nil {
				t.Fatal(err)
			}
			if len(report.Days) != 1 || report.Days[0].Day != tc.day {
				t.Fatalf("report = %+v, want only %s", report, tc.day)
			}
			if got := localEventCount(t, st, tc.day, loc); got != 1 {
				t.Errorf("%d events of the day remain, want only the context anchor", got)
			}
			if got := localEventCount(t, st, tc.day.AddDays(-1), loc); got != prevBefore {
				t.Errorf("previous day: %d events, want %d", got, prevBefore)
			}
			if got := localEventCount(t, st, tc.day.AddDays(1), loc); got != nextBefore {
				t.Errorf("next day: %d events, want %d", got, nextBefore)
			}
		})
	}
}

// The cleanup keeps the day when it cannot tell: a ledger row with other
// minutes in a DST zone is no different from any other mismatch.
func TestRunKeepsAMismatchedDayInADaylightSavingZone(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Zurich")
	if err != nil {
		t.Skipf("time zone unavailable: %v", err)
	}
	day := civil.Date{Year: 2026, Month: time.March, Day: 29}
	cfg := testConfig(30)
	cfg.Location = loc
	st := openStore(t)
	seedLocalDay(t, st, day, loc, "ABC-1")
	c := store.Confirmation{Day: day.String(), Issue: "ABC-1", Type: sessionizer.DefaultWorkItemType, Minutes: 60, ConfirmedAt: now}
	if err := st.Confirm(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	book(t, st, day, "ABC-1", 45)
	before := localEventCount(t, st, day, loc)

	report, err := Run(context.Background(), st, cfg, time.Date(2026, 6, 1, 12, 0, 0, 0, loc), false)
	if err != nil || len(report.Days) != 0 || localEventCount(t, st, day, loc) != before {
		t.Fatalf("report %+v, err %v: want the day untouched", report, err)
	}
}
