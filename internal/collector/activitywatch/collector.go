package activitywatch

import (
	"context"
	"time"

	"github.com/JtheGunner/ticktack/internal/collector"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/pause"
)

// PartialError reports that some buckets failed while the events of the
// healthy buckets were stored.
type PartialError struct{ Err error }

func (e *PartialError) Error() string { return e.Err.Error() }
func (e *PartialError) Unwrap() error { return e.Err }

// Collector is the retroactive ActivityWatch source: one Run imports
// everything since From and stores it. It implements collector.Collector.
type Collector struct {
	Importer *Importer
	// From returns where the pass starts reading.
	From func(ctx context.Context) (time.Time, error)
	// LoadPauses returns the pauses known at the importer's now. Events inside
	// them are dropped; when they cannot be loaded the pass stores nothing.
	LoadPauses func(now time.Time) (pause.Intervals, error)
	// OnStored, when set, receives the events that were stored.
	OnStored func([]event.Event)
}

// Name implements collector.Collector.
func (c *Collector) Name() string { return Source }

// Run makes one pass. Healthy buckets are stored even when others failed; the
// failure is then returned as a *PartialError.
func (c *Collector) Run(ctx context.Context, sink collector.Sink) error {
	paused, err := c.LoadPauses(c.Importer.Now())
	if err != nil {
		return err
	}
	from, err := c.From(ctx)
	if err != nil {
		return err
	}
	collected, collectErr := c.Importer.Collect(ctx, from)
	events := paused.Outside(collected)
	if err := sink.AppendBatch(ctx, events); err != nil {
		return err
	}
	if c.OnStored != nil {
		c.OnStored(events)
	}
	if collectErr != nil {
		return &PartialError{Err: collectErr}
	}
	return nil
}
