package git

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/JtheGunner/ticktack/internal/collector"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/pause"
)

// ErrPausesUnavailable matches (errors.Is) the error Run returns when the
// pauses could not be loaded. The pass then stores nothing: it would otherwise
// store time inside a pause.
var ErrPausesUnavailable = errors.New("pauses unavailable")

// pausesError carries the load failure under the text of its cause.
type pausesError struct{ cause error }

func (e *pausesError) Error() string        { return e.cause.Error() }
func (e *pausesError) Unwrap() error        { return e.cause }
func (e *pausesError) Is(target error) bool { return target == ErrPausesUnavailable }

// Skipped is a configured entry that was not scanned, with the reason.
type Skipped struct {
	Entry  string
	Reason string
}

// Scanned is a configured entry that was scanned, with its event count.
type Scanned struct {
	Entry string
	Found int
}

// Report is the outcome of one pass over the configured repositories.
type Report struct {
	Total   int
	Scanned int
	Found   int
	// Repos lists the scanned entries in configuration order.
	Repos   []Scanned
	Skipped []Skipped
}

// Collector is the retroactive git source: one Run reads the reflog of every
// configured repository and stores the context events found. It implements
// collector.Collector.
type Collector struct {
	// Repos are the configured entries, as written.
	Repos []string
	// Resolve turns an entry into the path to scan. A non-empty skip reason
	// skips the entry without scanning it.
	Resolve func(entry string) (path, skipReason string)
	// ScanRepo reads the events of one repository within [from, to].
	ScanRepo func(ctx context.Context, repo string, from, to time.Time, pattern *regexp.Regexp) ([]event.Event, error)
	// Describe turns a failed repository scan into a skip reason.
	Describe func(err error, timedOut bool) string
	// Pattern extracts ticket IDs from branch names; nil means the default.
	Pattern *regexp.Regexp
	// Window returns the time range of a pass that starts at now.
	Window func(now time.Time) (from, to time.Time)
	// Now is the injected clock.
	Now func() time.Time
	// LoadPauses returns the pauses known at now. Events inside them are
	// dropped; when they cannot be loaded the pass stores nothing.
	LoadPauses func(now time.Time) (pause.Intervals, error)
	// RepoTimeout bounds the git work for one repository.
	RepoTimeout time.Duration
	// OnReport, when set, receives the report of a pass after the store was
	// written, with the store error (nil on success). It is not called when
	// the pass failed before scanning.
	OnReport func(Report, error)
}

// Name implements collector.Collector.
func (c *Collector) Name() string { return Source }

// Run makes one pass. A failing repository is recorded in the report and does
// not stop the others. Only a failed append or a cancelled ctx aborts the
// pass. The events of all repositories are stored in one transaction after
// the last scan, so a pass either stores everything it found or nothing and
// holds the write lock only briefly. Appending is idempotent because every
// event carries a deterministic key.
func (c *Collector) Run(ctx context.Context, sink collector.Sink) error {
	now := c.Now()
	paused, err := c.LoadPauses(now)
	if err != nil {
		return &pausesError{cause: err}
	}
	from, to := c.Window(now)

	report := Report{Total: len(c.Repos)}
	var found []event.Event
	for _, entry := range c.Repos {
		if err := ctx.Err(); err != nil {
			return err
		}
		path, skipReason := c.Resolve(entry)
		if skipReason != "" {
			report.Skipped = append(report.Skipped, Skipped{entry, skipReason})
			continue
		}
		repoCtx, cancel := context.WithTimeout(ctx, c.RepoTimeout)
		events, err := c.ScanRepo(repoCtx, path, from, to, c.Pattern)
		timedOut := repoCtx.Err() != nil
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			report.Skipped = append(report.Skipped, Skipped{entry, c.Describe(err, timedOut)})
			continue
		}
		events = paused.Outside(events)
		found = append(found, events...)
		report.Scanned++
		report.Found += len(events)
		report.Repos = append(report.Repos, Scanned{Entry: entry, Found: len(events)})
	}
	appendErr := sink.AppendBatch(ctx, found)
	if c.OnReport != nil {
		c.OnReport(report, appendErr)
	}
	return appendErr
}
