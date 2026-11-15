package activitywatch

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"
)

// Source is the event source name of every imported event.
const Source = "activitywatch"

const (
	afkBucketPrefix    = "aw-watcher-afk_"
	windowBucketPrefix = "aw-watcher-window_"

	statusNotAFK = "not-afk"

	// DefaultInterval is the presence sample cadence; it matches the
	// sessionizer's sample interval.
	DefaultInterval = 30 * time.Second

	pageLimit = 5000
	maxPages  = 50
)

// ErrNoBuckets means the server has no AFK or window bucket for the host.
var ErrNoBuckets = errors.New("no aw-watcher-afk or aw-watcher-window bucket found")

// EventSource is the part of Client the importer needs.
type EventSource interface {
	Buckets(ctx context.Context) ([]Bucket, error)
	Events(ctx context.Context, bucketID string, start, end time.Time, limit int) ([]RawEvent, error)
}

// Importer turns ActivityWatch buckets into ticktack events. It is a
// collector: it only reads and returns events; the caller stores them.
type Importer struct {
	Source EventSource
	// Now is the injected clock; open intervals are cut off at Now.
	Now func() time.Time
	// Hostname restricts the import to buckets of this host; empty accepts all.
	Hostname string
	// Interval is the presence sample cadence; zero means DefaultInterval.
	Interval time.Duration
}

// Collect returns the events for everything ActivityWatch recorded since the
// given time, ordered by time. A failing bucket does not abort the others:
// its error is joined into the returned error while the events of the
// healthy buckets are still returned.
//
// Mapping:
//   - Inside every not-afk interval of an AFK bucket, one presence event with
//     idle_seconds 0 per Interval, on a grid of absolute multiples of the
//     interval. Nothing is emitted inside afk intervals.
//   - Between two not-afk intervals separated by afk time, a gap marker
//     stamped at the resume sample, so the sessionizer never bridges the
//     afk time.
//   - Every window event with an app becomes a context event that carries the
//     app name only.
func (i *Importer) Collect(ctx context.Context, since time.Time) ([]event.Event, error) {
	now := i.Now()
	buckets, err := i.Source.Buckets(ctx)
	if err != nil {
		return nil, fmt.Errorf("list buckets: %w", err)
	}
	afk, window := SelectBuckets(buckets, i.Hostname)
	if len(afk)+len(window) == 0 {
		return nil, ErrNoBuckets
	}

	var out []event.Event
	var errs []error
	for _, id := range afk {
		raw, err := i.fetch(ctx, id, since, now)
		if err != nil {
			errs = append(errs, fmt.Errorf("bucket %s: %w", id, err))
			continue
		}
		out = append(out, i.afkEvents(id, raw, now)...)
	}
	for _, id := range window {
		raw, err := i.fetch(ctx, id, since, now)
		if err != nil {
			errs = append(errs, fmt.Errorf("bucket %s: %w", id, err))
			continue
		}
		out = append(out, windowEvents(id, raw, now)...)
	}
	sort.SliceStable(out, func(a, b int) bool {
		if !out[a].At.Equal(out[b].At) {
			return out[a].At.Before(out[b].At)
		}
		return out[a].Key < out[b].Key
	})
	return out, errors.Join(errs...)
}

// SelectBuckets returns the sorted ids of the AFK and window buckets, limited
// to hostname when it is not empty.
func SelectBuckets(buckets []Bucket, hostname string) (afk, window []string) {
	for _, b := range buckets {
		switch {
		case matches(b.ID, afkBucketPrefix, hostname):
			afk = append(afk, b.ID)
		case matches(b.ID, windowBucketPrefix, hostname):
			window = append(window, b.ID)
		}
	}
	sort.Strings(afk)
	sort.Strings(window)
	return afk, window
}

func matches(id, prefix, hostname string) bool {
	host, ok := strings.CutPrefix(id, prefix)
	return ok && (hostname == "" || host == hostname)
}

// fetch pages through the events of a bucket, newest first, until the range
// is exhausted.
func (i *Importer) fetch(ctx context.Context, id string, since, now time.Time) ([]RawEvent, error) {
	var all []RawEvent
	end := now
	for range maxPages {
		page, err := i.Source.Events(ctx, id, since, end, pageLimit)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < pageLimit {
			return all, nil
		}
		oldest := page[0].Timestamp
		for _, e := range page {
			if e.Timestamp.Before(oldest) {
				oldest = e.Timestamp
			}
		}
		if !oldest.Before(end) {
			return all, nil
		}
		end = oldest
	}
	return all, nil
}

type interval struct{ start, end time.Time }

func (i *Importer) afkEvents(bucket string, raw []RawEvent, now time.Time) []event.Event {
	step := i.Interval
	if step <= 0 {
		step = DefaultInterval
	}
	var active []interval
	for _, r := range raw {
		if r.Data.Status != statusNotAFK || r.Duration <= 0 {
			continue
		}
		end := r.Timestamp.Add(time.Duration(r.Duration * float64(time.Second)))
		if end.After(now) {
			end = now
		}
		if end.After(r.Timestamp) {
			active = append(active, interval{r.Timestamp, end})
		}
	}
	sort.Slice(active, func(a, b int) bool { return active[a].start.Before(active[b].start) })

	var out []event.Event
	var prevEnd time.Time
	for _, iv := range active {
		samples := gridSamples(iv, step)
		if len(samples) == 0 {
			continue
		}
		if !prevEnd.IsZero() && iv.start.Sub(prevEnd) >= time.Second {
			resume := samples[0]
			out = append(out, event.Event{
				At:          resume,
				Source:      Source,
				Kind:        event.KindGap,
				IdleSeconds: int(resume.Sub(prevEnd) / time.Second),
				Key:         key(bucket, resume, "gap"),
			})
		}
		for _, at := range samples {
			out = append(out, event.Event{At: at, Source: Source, Kind: event.KindPresence, Key: key(bucket, at, "presence")})
		}
		if iv.end.After(prevEnd) {
			prevEnd = iv.end
		}
	}
	return out
}

// gridSamples returns the multiples of step (counted from the Unix epoch)
// that lie inside [iv.start, iv.end).
func gridSamples(iv interval, step time.Duration) []time.Time {
	first := iv.start.UTC().Truncate(step)
	if first.Before(iv.start) {
		first = first.Add(step)
	}
	var out []time.Time
	for at := first; at.Before(iv.end); at = at.Add(step) {
		out = append(out, at)
	}
	return out
}

func windowEvents(bucket string, raw []RawEvent, now time.Time) []event.Event {
	var out []event.Event
	for _, r := range raw {
		app := strings.TrimSpace(r.Data.App)
		if app == "" || r.Timestamp.After(now) {
			continue
		}
		at := r.Timestamp.UTC()
		out = append(out, event.Event{At: at, Source: Source, Kind: event.KindContext, App: app, Key: key(bucket, at, "context")})
	}
	return out
}

func key(bucket string, at time.Time, kind string) string {
	return fmt.Sprintf("aw:%s:%d:%s", bucket, at.UTC().UnixNano(), kind)
}
