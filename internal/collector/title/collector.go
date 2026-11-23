package title

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/JtheGunner/ticktack/internal/collector"
	"github.com/JtheGunner/ticktack/internal/event"
)

// EventSource is the Source value of every event emitted here.
const EventSource = "title"

// DefaultInterval is the sampling period. It matches the presence agent but
// runs in its own loop, so a slow osascript call never delays presence.
const DefaultInterval = 30 * time.Second

// sampleTimeout bounds one title read.
const sampleTimeout = 5 * time.Second

// Collector samples the frontmost window title, reduces it to a Hint and
// appends a context event when the hint is usable. The title itself never
// leaves Tick: it is not stored, logged or put into an error.
type Collector struct {
	Source Source
	// Projects maps an IDE project name to the absolute repository path.
	Projects map[string]string
	// TicketPattern extracts ticket IDs; nil means the default pattern.
	TicketPattern *regexp.Regexp
	Interval      time.Duration
	// Now is the injected clock.
	Now func() time.Time
	// Paused reports whether tracking is paused; nothing is read then.
	Paused func() bool
	// Logf receives non-fatal errors. It must never write to stdout.
	Logf func(format string, args ...any)
	// Ticks optionally replaces the internal ticker (for tests).
	Ticks <-chan time.Time
	// Status, when set, receives the outcome of every title read (not the
	// title) so `ticktack doctor` can report what the agent experienced.
	Status StatusRecorder

	failing       bool
	statusFailing bool
}

// Name implements collector.Collector.
func (c *Collector) Name() string { return EventSource }

// Run samples once immediately and then on every tick until ctx is cancelled.
func (c *Collector) Run(ctx context.Context, sink collector.Sink) error {
	ticks := c.Ticks
	if ticks == nil {
		interval := c.Interval
		if interval <= 0 {
			interval = DefaultInterval
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		ticks = ticker.C
	}
	c.Tick(ctx, sink)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticks:
			c.Tick(ctx, sink)
		}
	}
}

// Tick performs one sampling step. Errors are logged, never returned. While
// paused the window is not even read.
//
// The emitted event carries only the frontmost app's bundle ID (so the
// sessionizer can apply the hint only while that app is in front), the
// ticket ID (when found) and the
// repository configured for the project name (when mapped). No event is
// appended when neither is known.
func (c *Collector) Tick(ctx context.Context, sink collector.Sink) {
	if c.Paused != nil && c.Paused() {
		return
	}
	readCtx, cancel := context.WithTimeout(ctx, sampleTimeout)
	window, err := c.Source.Frontmost(readCtx)
	cancel()
	c.recordProbe(err)
	if err != nil {
		// Log the first failure of a streak only: a missing permission would
		// otherwise fill the log every interval.
		if !c.failing {
			c.logf("title sample skipped: %v", err)
		}
		c.failing = true
		return
	}
	c.failing = false

	hint := Parse(window.App, window.Title, c.TicketPattern)
	e := event.Event{At: c.Now(), Source: EventSource, Kind: event.KindContext, App: window.App, Ticket: hint.Ticket}
	if hint.Project != "" {
		e.Repo = c.Projects[hint.Project]
	}
	if e.Ticket == "" && e.Repo == "" {
		return
	}
	if err := sink.Append(ctx, e); err != nil {
		c.logf("append %s event failed: %v", EventSource, err)
	}
}

// recordProbe stores the outcome of one title read in the status file. Only
// the outcome is stored, never the window. A failing write is logged once per
// streak.
func (c *Collector) recordProbe(readErr error) {
	if c.Status == nil {
		return
	}
	state := ProbeOK
	switch {
	case errors.Is(readErr, ErrPermission):
		state = ProbeDenied
	case readErr != nil:
		state = ProbeFailed
	}
	if err := c.Status.Record(state, c.Now()); err != nil {
		if !c.statusFailing {
			c.logf("title probe status not written: %v", err)
		}
		c.statusFailing = true
		return
	}
	c.statusFailing = false
}

func (c *Collector) logf(format string, args ...any) {
	if c.Logf != nil {
		c.Logf(format, args...)
	}
}
