package sessionizer

import (
	"fmt"
	"sort"
	"time"

	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/localday"
)

// RoundingConfig controls how Aggregate rounds and caps time.
type RoundingConfig struct {
	// Rounding is the booking granularity. Zero or negative means one minute.
	Rounding time.Duration
	// DailyCap flags days whose rounded total exceeds it. Zero disables it.
	DailyCap time.Duration
	// DefaultType is the work item type given to every DayItem.
	DefaultType string
}

// DefaultWorkItemType is the work item type used when none is configured.
const DefaultWorkItemType = "Development"

// DefaultRoundingConfig returns the default rounding and cap settings.
func DefaultRoundingConfig() RoundingConfig {
	return RoundingConfig{
		Rounding:    config.DefaultRounding,
		DailyCap:    config.DefaultDailyCap,
		DefaultType: DefaultWorkItemType,
	}
}

// RoundingConfigFrom builds a RoundingConfig from the user configuration.
func RoundingConfigFrom(c config.Config) RoundingConfig {
	rc := DefaultRoundingConfig()
	if c.Rounding > 0 {
		rc.Rounding = c.Rounding
	}
	if c.DailyCap > 0 {
		rc.DailyCap = c.DailyCap
	}
	if c.DailyCapDisabled {
		rc.DailyCap = 0
	}
	return rc
}

// DayItem is the total time of one ticket on one local day.
type DayItem struct {
	Date civil.Date
	// Ticket is empty for time that still needs to be assigned in review.
	Ticket string
	Type   string
	// Minutes is the rounded total to book. Items that round to zero are
	// omitted.
	Minutes int
	// Description is a short summary such as "ABC-1 — 3 blocks".
	Description string
	// RawMinutes is the unrounded total in whole minutes (nearest).
	RawMinutes int
	// OverCap is set on every item of a day whose rounded total exceeds
	// RoundingConfig.DailyCap. Time is never truncated silently.
	OverCap bool
	// Certainty splits Minutes by tier; it always adds up to Minutes. See
	// apportionCertainty for how rounding distributes it.
	Certainty Certainty
}

type dayKey struct {
	date   civil.Date
	ticket string
}

type dayTotal struct {
	duration time.Duration
	blocks   int
	// byBucket splits duration by certainty class.
	byBucket [bucketCount]time.Duration
}

// Aggregate splits blocks at the local day boundary in loc, sums the time per
// (local date, ticket), and rounds each day: the day total is rounded to the
// configured granularity and distributed over the tickets by largest
// remainder, so the rounded per-ticket totals add up to the rounded total of
// the assigned tickets; unassigned time is rounded separately (see roundDay).
// Boundaries come from localday, so 23 h and 25 h days and days
// without a local midnight work.
// The result is ordered by date, then ticket (empty ticket first).
func Aggregate(blocks []Block, cfg RoundingConfig, loc *time.Location) []DayItem {
	return AggregateReport(blocks, cfg, loc).Items
}

// AggregateReport is Aggregate plus, per day, what the rounding did to the
// time: see Rounded.
func AggregateReport(blocks []Block, cfg RoundingConfig, loc *time.Location) Rounded {
	if loc == nil {
		loc = time.UTC
	}
	step := cfg.Rounding
	if step <= 0 {
		step = time.Minute
	}

	totals := map[dayKey]*dayTotal{}
	for _, b := range blocks {
		for cur := b.Start; cur.Before(b.End); {
			date := civil.DateOf(cur.In(loc))
			// The next boundary is strictly after cur, so the loop ends.
			end := minTime(b.End, localday.Next(cur, loc))
			key := dayKey{date, b.Ticket}
			t := totals[key]
			if t == nil {
				t = &dayTotal{}
				totals[key] = t
			}
			t.duration += end.Sub(cur)
			t.byBucket[bucketOf(b)] += end.Sub(cur)
			t.blocks++
			cur = end
		}
	}

	byDay := map[civil.Date][]dayKey{}
	for key := range totals {
		byDay[key.date] = append(byDay[key.date], key)
	}
	dates := make([]civil.Date, 0, len(byDay))
	for d := range byDay {
		dates = append(dates, d)
	}
	sort.Slice(dates, func(i, j int) bool { return dates[i].Before(dates[j]) })

	var out Rounded
	for _, date := range dates {
		keys := byDay[date]
		sort.Slice(keys, func(i, j int) bool { return keys[i].ticket < keys[j].ticket })
		dayItems := roundDay(date, keys, totals, cfg, step)
		out.Items = append(out.Items, dayItems...)
		out.Days = append(out.Days, summarizeDay(date, keys, totals, dayItems))
	}
	return out
}

// roundDay rounds one day with largest-remainder apportionment in units of step.
//
// "Unassigned" means time without a ticket (an empty Ticket, which is what
// resolver tier unknown produces). It is rounded on its own, as a group of
// one, and never competes with the tickets for remainder units: otherwise a
// confirmed item could change, and lose its confirmation, just because the
// unassigned time changed. The tickets are apportioned among themselves to the
// rounded total of their own time.
func roundDay(date civil.Date, keys []dayKey, totals map[dayKey]*dayTotal, cfg RoundingConfig, step time.Duration) []DayItem {
	var assignedKeys, unassignedKeys []dayKey
	for _, k := range keys {
		if k.ticket == "" {
			unassignedKeys = append(unassignedKeys, k)
		} else {
			assignedKeys = append(assignedKeys, k)
		}
	}
	unitsOf := map[dayKey]int64{}
	for _, group := range [][]dayKey{assignedKeys, unassignedKeys} {
		for k, u := range apportion(group, totals, step) {
			unitsOf[k] = u
		}
	}
	units := make([]int64, len(keys))
	for i, k := range keys {
		units[i] = unitsOf[k]
	}

	var roundedTotal time.Duration
	for _, u := range units {
		roundedTotal += time.Duration(u) * step
	}
	overCap := cfg.DailyCap > 0 && roundedTotal > cfg.DailyCap

	var items []DayItem
	for i, k := range keys {
		if units[i] == 0 {
			continue
		}
		t := totals[k]
		items = append(items, DayItem{
			Date:        date,
			Ticket:      k.ticket,
			Type:        cfg.DefaultType,
			Minutes:     int(time.Duration(units[i]) * step / time.Minute),
			Description: describe(k.ticket, t.blocks),
			RawMinutes:  int((t.duration + 30*time.Second) / time.Minute),
			OverCap:     overCap,
			Certainty:   apportionCertainty(t.byBucket, units[i], step),
		})
	}
	return items
}

// apportion distributes the group's total, rounded to the nearest unit with
// halves up, over its keys by largest remainder.
func apportion(keys []dayKey, totals map[dayKey]*dayTotal, step time.Duration) map[dayKey]int64 {
	var sum time.Duration
	for _, k := range keys {
		sum += totals[k].duration
	}
	groupUnits := int64((sum + step/2) / step)

	units := make([]int64, len(keys))
	var assigned int64
	for i, k := range keys {
		units[i] = int64(totals[k].duration / step)
		assigned += units[i]
	}
	order := make([]int, len(keys))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		ra, rb := totals[keys[order[a]]].duration%step, totals[keys[order[b]]].duration%step
		return ra > rb
	})
	for i := 0; assigned < groupUnits && len(order) > 0; i++ {
		units[order[i%len(order)]]++
		assigned++
	}
	out := make(map[dayKey]int64, len(keys))
	for i, k := range keys {
		out[k] = units[i]
	}
	return out
}

func describe(ticket string, blocks int) string {
	if ticket == "" {
		ticket = "unassigned"
	}
	noun := "blocks"
	if blocks == 1 {
		noun = "block"
	}
	return fmt.Sprintf("%s — %d %s", ticket, blocks, noun)
}
