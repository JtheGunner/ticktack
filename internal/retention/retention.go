// Package retention deletes the raw events of days that are fully booked and
// older than the configured retention period.
//
// Events are otherwise append-only, so the cleanup is deliberately cautious:
// a day is only cleaned when re-deriving it from its events, rules and
// overrides (the derivation review and sync use) gives items that are all
// confirmed and booked in the ledger with the same minutes. In any doubt the
// events stay. A cleaned day is recorded as archived and frozen: it is never
// derived again, so a day without events is never mistaken for a day without
// time.
package retention

import (
	"context"
	"errors"
	"fmt"
	"time"

	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/ledger"
	"github.com/JtheGunner/ticktack/internal/localday"
	"github.com/JtheGunner/ticktack/internal/review"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
	"github.com/JtheGunner/ticktack/internal/store"
)

// Store is what the cleanup needs from the persistence layer.
type Store interface {
	review.Store
	LedgerRowsForDate(ctx context.Context, date string) ([]store.LedgerRow, error)
	ArchivedDays(ctx context.Context) ([]store.ArchivedDay, error)
	EarliestEventAt(ctx context.Context) (time.Time, bool, error)
	ArchiveDay(ctx context.Context, req store.ArchiveRequest) (int, error)
}

// Expired reports whether day is older than retentionDays days before today.
// It is pure date arithmetic on local calendar days, so DST never shifts it.
// A retention of zero or less is off and nothing expires.
func Expired(day, today civil.Date, retentionDays int) bool {
	return retentionDays > 0 && today.DaysSince(day) > retentionDays
}

// DayReport is one cleaned (or, in a dry run, cleanable) day.
type DayReport struct {
	Day    civil.Date
	Events int
}

// Report is the outcome of one cleanup run.
type Report struct {
	// Days lists the days that were cleaned, oldest first.
	Days []DayReport
	// Events is the number of deleted events over all days.
	Events int
	// DryRun is set when nothing was deleted.
	DryRun bool
}

// Run archives every expired, fully booked day that still has events and
// deletes its events; with dryRun it only counts. It does nothing while
// cfg.RetentionDays is zero. now is passed in; the function reads no clock.
// Each day is archived in its own transaction, so an interrupted run leaves
// every day either untouched or cleaned and recorded.
func Run(ctx context.Context, st Store, cfg config.Config, now time.Time, dryRun bool) (Report, error) {
	report := Report{DryRun: dryRun}
	if cfg.RetentionDays <= 0 {
		return report, nil
	}
	loc := cfg.Location
	if loc == nil {
		loc = time.Local
	}
	today := civil.DateOf(now.In(loc))
	earliest, found, err := st.EarliestEventAt(ctx)
	if err != nil || !found {
		return report, err
	}
	archived, err := st.ArchivedDays(ctx)
	if err != nil {
		return report, err
	}
	isArchived := make(map[string]bool, len(archived))
	for _, a := range archived {
		isArchived[a.Day] = true
	}
	for day := civil.DateOf(earliest.In(loc)); Expired(day, today, cfg.RetentionDays); day = day.AddDays(1) {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		if isArchived[day.String()] {
			continue
		}
		from, to := localday.Bounds(day, loc)
		events, err := st.Events(ctx, from, to)
		if err != nil {
			return report, err
		}
		if len(events) == 0 {
			continue
		}
		booked, err := FullyBooked(ctx, st, cfg, day)
		if err != nil {
			return report, err
		}
		if !booked {
			continue
		}
		deleted, err := st.ArchiveDay(ctx, store.ArchiveRequest{
			Day: day.String(), From: from, To: to, ArchivedAt: now,
			Carry:  store.CarrySources{Manual: sessionizer.ManualSource, AppHint: sessionizer.TitleSource},
			DryRun: dryRun,
		})
		if err != nil {
			return report, fmt.Errorf("clean %s: %w", day, err)
		}
		report.Days = append(report.Days, DayReport{Day: day, Events: deleted})
		report.Events += deleted
	}
	return report, nil
}

// FullyBooked reports whether everything the day derives to is booked: the
// day has at least one item, every item has a ticket and is confirmed at its
// current minutes, the day is not blocked by an unacknowledged cap, and the
// ledger holds exactly one row per item with the same minutes and no row
// besides them. It re-derives the day through review.LoadDay and
// review.ConfirmedItems, the loaders review and sync use. It answers false
// whenever it cannot be sure.
func FullyBooked(ctx context.Context, st Store, cfg config.Config, day civil.Date) (bool, error) {
	d, err := review.LoadDay(ctx, st, cfg, day)
	if err != nil {
		return false, err
	}
	if len(d.Items) == 0 {
		return false, nil
	}
	confirmed, err := review.ConfirmedItems(ctx, st, cfg, day)
	var blocked *review.CapBlockedError
	if errors.As(err, &blocked) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if len(confirmed) != len(d.Items) {
		return false, nil
	}
	tasks, err := st.DefaultTasks(ctx)
	if err != nil {
		return false, err
	}
	want := make(map[string]int, len(d.Items))
	for _, it := range d.Items {
		key, ok := ledger.ItemKey(it, tasks)
		if !ok {
			return false, nil
		}
		if _, clash := want[key]; clash {
			return false, nil
		}
		want[key] = it.Minutes
	}
	rows, err := st.LedgerRowsForDate(ctx, day.String())
	if err != nil {
		return false, err
	}
	if len(rows) != len(want) {
		return false, nil
	}
	for _, row := range rows {
		minutes, ok := want[ledger.Key(row.Issue, row.Date, row.Type)]
		if !ok || minutes != row.Minutes {
			return false, nil
		}
	}
	return true, nil
}
