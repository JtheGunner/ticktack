package presence

import (
	"context"
	"errors"
	"time"

	"github.com/JtheGunner/ticktack/internal/collector"
	"github.com/JtheGunner/ticktack/internal/event"
)

// DefaultInterval is the sampling period.
const DefaultInterval = 30 * time.Second

// DefaultSampleTimeout bounds one sampler call. A subprocess that hangs
// longer costs one sample, not the agent.
const DefaultSampleTimeout = 5 * time.Second

// gapFactor is the multiple of Interval above which the time between two
// ticks counts as a gap (typically the machine slept).
const gapFactor = 3

// Agent periodically samples presence and appends events.
type Agent struct {
	Sampler  Sampler
	Interval time.Duration
	// SampleTimeout bounds one Sampler call; zero means DefaultSampleTimeout.
	SampleTimeout time.Duration
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

// Name implements collector.Collector.
func (a *Agent) Name() string { return "presence" }

// Run samples once immediately and then on every tick until ctx is cancelled.
// Cancellation is a clean stop and returns nil.
func (a *Agent) Run(ctx context.Context, sink collector.Sink) error {
	ticks := a.Ticks
	if ticks == nil {
		ticker := time.NewTicker(a.Interval)
		defer ticker.Stop()
		ticks = ticker.C
	}
	a.Tick(ctx, sink)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticks:
			a.Tick(ctx, sink)
		}
	}
}

// Tick performs one sampling step. Errors are logged, never returned.
//
// While paused the tick time is still tracked, so a long pause is not
// mistaken for a sleep gap. A gap (more than three intervals since the
// previous tick) is recorded as a single KindGap event; no samples are
// invented for it.
func (a *Agent) Tick(ctx context.Context, sink collector.Sink) {
	// Strip the monotonic clock reading: on macOS it does not advance while
	// the system sleeps, so gap detection must compare wall-clock times.
	now := a.Now().Round(0)
	previous := a.lastTick
	a.lastTick = now

	if a.Paused() {
		return
	}
	if !previous.IsZero() && now.Sub(previous) > gapFactor*a.Interval {
		a.append(ctx, sink, event.Event{
			At:          now,
			Source:      "presence",
			Kind:        event.KindGap,
			IdleSeconds: int(now.Sub(previous).Round(time.Second) / time.Second),
		})
	}
	idle, app, err := a.sample(ctx)
	if err != nil {
		return
	}
	a.append(ctx, sink, event.Event{
		At:          now,
		Source:      "presence",
		Kind:        event.KindPresence,
		App:         app,
		IdleSeconds: int(idle / time.Second),
	})
}

// sample runs one sampler call under SampleTimeout and logs its failure. An
// expired timeout is logged as such, so a hanging subprocess is told apart
// from a failing one.
func (a *Agent) sample(ctx context.Context) (time.Duration, string, error) {
	timeout := a.SampleTimeout
	if timeout <= 0 {
		timeout = DefaultSampleTimeout
	}
	sampleCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	idle, app, err := a.Sampler.Sample(sampleCtx)
	if err == nil {
		return idle, app, nil
	}
	if errors.Is(sampleCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
		a.logf("presence sample timed out after %s", timeout)
		return 0, "", err
	}
	a.logf("presence sample skipped: %v", err)
	return 0, "", err
}

func (a *Agent) append(ctx context.Context, sink collector.Sink, e event.Event) {
	if err := sink.Append(ctx, e); err != nil {
		a.logf("append %s event failed: %v", e.Kind, err)
	}
}

func (a *Agent) logf(format string, args ...any) {
	if a.Logf != nil {
		a.Logf(format, args...)
	}
}
