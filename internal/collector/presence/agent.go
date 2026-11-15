package presence

import (
	"context"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"
)

// DefaultInterval is the sampling period.
const DefaultInterval = 30 * time.Second

// gapFactor is the multiple of Interval above which the time between two
// ticks counts as a gap (typically the machine slept).
const gapFactor = 3

// Appender stores events. *store.Store satisfies it.
type Appender interface {
	Append(ctx context.Context, e event.Event) error
}

// Agent periodically samples presence and appends events.
type Agent struct {
	Sampler  Sampler
	Sink     Appender
	Interval time.Duration
	// Now is the injected clock.
	Now func() time.Time
	// Paused reports whether tracking is paused; nothing is stored then.
	Paused func() bool
	// Logf receives non-fatal errors. It must never write to stdout.
	Logf func(format string, args ...any)
	// Ticks optionally replaces the internal ticker (for tests).
	Ticks <-chan time.Time

	lastTick time.Time
}

// Run samples once immediately and then on every tick until ctx is cancelled.
// Cancellation is a clean stop and returns nil.
func (a *Agent) Run(ctx context.Context) error {
	ticks := a.Ticks
	if ticks == nil {
		ticker := time.NewTicker(a.Interval)
		defer ticker.Stop()
		ticks = ticker.C
	}
	a.Tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticks:
			a.Tick(ctx)
		}
	}
}

// Tick performs one sampling step. Errors are logged, never returned.
//
// While paused the tick time is still tracked, so a long pause is not
// mistaken for a sleep gap. A gap (more than three intervals since the
// previous tick) is recorded as a single KindGap event; no samples are
// invented for it.
func (a *Agent) Tick(ctx context.Context) {
	now := a.Now()
	previous := a.lastTick
	a.lastTick = now

	if a.Paused() {
		return
	}
	if !previous.IsZero() && now.Sub(previous) > gapFactor*a.Interval {
		a.append(ctx, event.Event{
			At:          now,
			Source:      "presence",
			Kind:        event.KindGap,
			IdleSeconds: int(now.Sub(previous).Round(time.Second) / time.Second),
		})
	}
	idle, app, err := a.Sampler.Sample(ctx)
	if err != nil {
		a.logf("presence sample skipped: %v", err)
		return
	}
	a.append(ctx, event.Event{
		At:          now,
		Source:      "presence",
		Kind:        event.KindPresence,
		App:         app,
		IdleSeconds: int(idle / time.Second),
	})
}

func (a *Agent) append(ctx context.Context, e event.Event) {
	if err := a.Sink.Append(ctx, e); err != nil {
		a.logf("append %s event failed: %v", e.Kind, err)
	}
}

func (a *Agent) logf(format string, args ...any) {
	if a.Logf != nil {
		a.Logf(format, args...)
	}
}
