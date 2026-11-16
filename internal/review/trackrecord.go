package review

import (
	"context"
	"sort"
	"time"

	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/store"
)

// ReviewLog is the read side of the review log. *store.Store implements it.
type ReviewLog interface {
	ReviewRecords(ctx context.Context) ([]store.ReviewRecord, error)
}

// TrackRecord returns the current streak: the number of most recent
// consecutive reviewed days without a correction, walking back from the most
// recent reviewed day up to and including the day of now.
//
// A day is clean when none of its review sessions made a correction, and it
// counts when its sessions together confirmed at least one item. A single
// session with a correction ends the streak, whatever sessions follow it that
// day. Days without a review record are skipped (weekends, holidays); days
// whose sessions confirmed nothing and corrected nothing neither count nor
// break the streak. Days booked by auto mode have no review record and so
// never count.
//
// Review-day rule: a session confirms a day for the streak only when it took
// place on that calendar day or the next one, in the time zone of now
// (the configured one). ticktack review --day may still review older days
// and their confirmations stay valid for booking, but such catch-up reviews
// do not add to the streak. Corrections always count, however late: a
// correction ends the streak either way.
func TrackRecord(ctx context.Context, log ReviewLog, now time.Time) (int, error) {
	records, err := log.ReviewRecords(ctx)
	if err != nil {
		return 0, err
	}
	type dayTotals struct{ corrections, confirmed int }
	loc := now.Location()
	today := civil.DateOf(now).String()
	byDay := map[string]*dayTotals{}
	for _, r := range records {
		if r.Day > today {
			continue
		}
		t := byDay[r.Day]
		if t == nil {
			t = &dayTotals{}
			byDay[r.Day] = t
		}
		t.corrections += r.Corrections
		if timelyReview(r, loc) {
			t.confirmed += r.Confirmed
		}
	}
	days := make([]string, 0, len(byDay))
	for d := range byDay {
		days = append(days, d)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(days)))
	streak := 0
	for _, d := range days {
		t := byDay[d]
		if t.corrections > 0 {
			break
		}
		if t.confirmed > 0 {
			streak++
		}
	}
	return streak, nil
}

// timelyReview reports whether the session started on the record's day or the
// day after, judged by calendar dates in loc so DST never shifts the cut-off.
func timelyReview(r store.ReviewRecord, loc *time.Location) bool {
	day, err := civil.ParseDate(r.Day)
	if err != nil {
		return false
	}
	started := civil.DateOf(r.SessionStart.In(loc))
	return started == day || started == day.AddDays(1)
}
