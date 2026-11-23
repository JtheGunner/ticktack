// Package collector defines what every event source of ticktack looks like and
// the shared pieces that run them.
//
// A Collector has a name and one entry point, Run. It writes events to a Sink
// and never to the store directly, so one place decides what reaches the store.
// Two run models exist:
//
//   - Long-running sources (presence, window title) keep running inside Run
//     until the context is cancelled. Cancellation is a clean stop and returns
//     nil.
//   - Retroactive sources (git reflog scan, ActivityWatch import) do exactly
//     one pass inside Run and return. Wrap them in Every to repeat the pass.
//
// The concrete sources live in the sub-packages presence, title, git and
// activitywatch. The shell hook is its own command but writes through the same
// Sink.
package collector

import (
	"context"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"
)

// Collector is an event source.
type Collector interface {
	// Name identifies the source in logs.
	Name() string
	// Run delivers events to sink. A long-running source blocks until ctx is
	// cancelled and then returns nil; a retroactive source makes one pass and
	// returns. Failures that end a pass are returned.
	Run(ctx context.Context, sink Sink) error
}

// Sink receives the events of a collector. *store.Store satisfies it.
type Sink interface {
	// Append stores one event.
	Append(ctx context.Context, e event.Event) error
	// AppendBatch stores all events in one transaction: all or none.
	AppendBatch(ctx context.Context, events []event.Event) error
}

// Every repeats the single pass of c: once at start and then every interval,
// until ctx is cancelled (which returns nil). A failed pass does not stop the
// repetition; onError, when not nil, receives it unless ctx is already
// cancelled. A non-positive interval makes one pass only.
func Every(interval time.Duration, c Collector, onError func(error)) Collector {
	return &every{interval: interval, inner: c, onError: onError}
}

type every struct {
	interval time.Duration
	inner    Collector
	onError  func(error)
}

func (e *every) Name() string { return e.inner.Name() }

func (e *every) Run(ctx context.Context, sink Sink) error {
	pass := func() {
		if err := e.inner.Run(ctx, sink); err != nil && ctx.Err() == nil && e.onError != nil {
			e.onError(err)
		}
	}
	pass()
	if e.interval <= 0 {
		return nil
	}
	ticker := time.NewTicker(e.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			pass()
		}
	}
}

// SkipWhen makes a pass of c do nothing while paused reports true.
func SkipWhen(paused func() bool, c Collector) Collector {
	return &skipWhen{paused: paused, inner: c}
}

type skipWhen struct {
	paused func() bool
	inner  Collector
}

func (s *skipWhen) Name() string { return s.inner.Name() }

func (s *skipWhen) Run(ctx context.Context, sink Sink) error {
	if s.paused() {
		return nil
	}
	return s.inner.Run(ctx, sink)
}
