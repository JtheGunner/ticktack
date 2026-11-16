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
// A day counts when its latest review record has no correction and at least
// one confirmed item. Days without a review record are skipped (weekends,
// holidays); days reviewed without confirming anything neither count nor
// break the streak. The first day whose latest record has corrections ends
// the streak. Days booked by auto mode have no review record and so never
// count.
func TrackRecord(ctx context.Context, log ReviewLog, now time.Time) (int, error) {
	records, err := log.ReviewRecords(ctx)
	if err != nil {
		return 0, err
	}
	today := civil.DateOf(now).String()
	latest := map[string]store.ReviewRecord{}
	for _, r := range records {
		if r.Day > today {
			continue
		}
		if cur, ok := latest[r.Day]; !ok || !r.FinishedAt.Before(cur.FinishedAt) {
			latest[r.Day] = r
		}
	}
	days := make([]string, 0, len(latest))
	for d := range latest {
		days = append(days, d)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(days)))
	streak := 0
	for _, d := range days {
		r := latest[d]
		if r.Corrections > 0 {
			break
		}
		if r.Confirmed > 0 {
			streak++
		}
	}
	return streak, nil
}
