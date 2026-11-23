package review

import (
	"context"
	"fmt"
	"sort"
	"time"

	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/store"
)

// TrackRecordStore is the read side the trust gate needs: the review log, the
// manual rule changes, and what it takes to re-derive a day and compare it
// with its confirmations. *store.Store implements it.
type TrackRecordStore interface {
	Reader
	Confirmations(ctx context.Context, day string) ([]store.Confirmation, error)
	ReviewRecords(ctx context.Context) ([]store.ReviewRecord, error)
	RuleChanges(ctx context.Context) ([]store.RuleChange, error)
}

// TrackRecord returns the current streak of the trust gate: the number of
// most recent consecutive reviewed days that were confirmed without a
// correction, walking back from the day of now. Auto mode unlocks when the
// streak reaches the configured number of days.
//
// A day counts when all of this holds:
//   - It has at least one assigned item, and every assigned item is confirmed
//     with exactly the minutes the day derives to now. An item whose minutes
//     changed after its confirmation, or that appeared later, is unconfirmed.
//   - Every one of those confirmations was made on that calendar day or the
//     next one. ticktack review --day may still confirm older days for
//     booking, but such catch-up reviews do not add to the streak.
//   - Open unassigned time does not matter: it is never booked.
//
// A correction ends the streak at its day, whatever else happened that day
// and however late it was made. A correction is:
//   - Time that already had a ticket is reassigned, discarded, marked as a
//     break or split in a review (counted in the review log).
//   - A learned rule is added, edited or removed with ticktack map. It counts
//     on the calendar day of the change, reviewed or not: whoever has to
//     change a rule has seen a wrong assignment.
//
// These are deliberately no corrections:
//   - Giving unassigned time a ticket, splitting or discarding it, and
//     remembering a rule for it: the tool claimed nothing wrong there.
//   - ticktack switch: it is information given in advance, not a fix.
//
// Days that neither count nor hold a correction are skipped without ending
// the streak: days without a review (weekends, holidays), partly confirmed
// days and days booked by auto mode, which leave no confirmation.
//
// All calendar days are judged in the configured time zone. With upTo above
// zero the walk stops as soon as the streak reaches it, so a long history is
// not re-derived just to pass the gate; zero counts the whole streak.
func TrackRecord(ctx context.Context, st TrackRecordStore, cfg config.Config, now time.Time, upTo int) (int, error) {
	loc := location(cfg)
	today := civil.DateOf(now.In(loc)).String()

	records, err := st.ReviewRecords(ctx)
	if err != nil {
		return 0, err
	}
	changes, err := st.RuleChanges(ctx)
	if err != nil {
		return 0, err
	}
	// corrected maps every candidate day to whether it holds a correction.
	corrected := map[string]bool{}
	for _, r := range records {
		if r.Day <= today {
			corrected[r.Day] = corrected[r.Day] || r.Corrections > 0
		}
	}
	for _, c := range changes {
		if day := civil.DateOf(c.ChangedAt.In(loc)).String(); day <= today {
			corrected[day] = true
		}
	}
	days := make([]string, 0, len(corrected))
	for d := range corrected {
		days = append(days, d)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(days)))

	streak := 0
	for _, d := range days {
		if corrected[d] {
			break
		}
		day, err := civil.ParseDate(d)
		if err != nil {
			return 0, fmt.Errorf("review log day %q: %w", d, err)
		}
		confirmed, err := fullyConfirmed(ctx, st, cfg, day)
		if err != nil {
			return 0, err
		}
		if !confirmed {
			continue
		}
		streak++
		if upTo > 0 && streak >= upTo {
			break
		}
	}
	return streak, nil
}

// fullyConfirmed reports whether day has assigned items and each of them is
// confirmed at its current minutes by a timely confirmation.
func fullyConfirmed(ctx context.Context, st TrackRecordStore, cfg config.Config, day civil.Date) (bool, error) {
	d, err := LoadDay(ctx, st, cfg, day)
	if err != nil {
		return false, err
	}
	list, err := st.Confirmations(ctx, day.String())
	if err != nil {
		return false, err
	}
	confirmations := make(map[itemKey]store.Confirmation, len(list))
	for _, c := range list {
		confirmations[itemKey{c.Issue, c.Type}] = c
	}
	loc := location(cfg)
	assigned := 0
	for _, it := range d.Items {
		if it.Ticket == "" {
			continue
		}
		assigned++
		c, ok := confirmations[itemKey{it.Ticket, itemType(it)}]
		if !ok || c.Minutes != it.Minutes || !timelyConfirmation(c.ConfirmedAt, day, loc) {
			return false, nil
		}
	}
	return assigned > 0, nil
}

// timelyConfirmation reports whether the confirmation was made on day or the
// day after, judged by calendar dates in loc so DST never shifts the cut-off.
func timelyConfirmation(confirmedAt time.Time, day civil.Date, loc *time.Location) bool {
	made := civil.DateOf(confirmedAt.In(loc))
	return made == day || made == day.AddDays(1)
}
