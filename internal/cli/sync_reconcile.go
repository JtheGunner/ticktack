package cli

import (
	"context"

	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/ledger"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
	"github.com/JtheGunner/ticktack/internal/store"
)

// reconciliation says which booked rows of a day stay: the keys of every item
// the day still derives, confirmed or not. A booked row outside that set
// belongs to time that was reassigned, discarded or rounded away, and is
// stale. stale reports whether the day has such a row.
func reconciliation(ctx context.Context, st *store.Store, dayItems []sessionizer.DayItem, day civil.Date) (keep []string, stale bool, err error) {
	tasks, err := st.DefaultTasks(ctx)
	if err != nil {
		return nil, false, err
	}
	keep = keptKeys(dayItems, tasks)
	rows, err := st.LedgerRowsForDate(ctx, day.String())
	if err != nil {
		return nil, false, err
	}
	kept := make(map[string]bool, len(keep))
	for _, key := range keep {
		kept[key] = true
	}
	for _, row := range rows {
		if !kept[ledger.Key(row.Issue, row.Date, row.Type)] {
			return keep, true, nil
		}
	}
	return keep, false, nil
}

// keptKeys returns the ledger key of every attributed item. Time on a standard
// task is booked on the real issue, so its placeholder is replaced by the
// known task key of the project.
func keptKeys(items []sessionizer.DayItem, defaultTasks map[string]string) []string {
	keys := make([]string, 0, len(items))
	for _, it := range items {
		if key, ok := ledger.ItemKey(it, defaultTasks); ok {
			keys = append(keys, key)
		}
	}
	return keys
}
