package collector

import (
	"context"

	"github.com/JtheGunner/ticktack/internal/event"
)

// PauseGate wraps next so that nothing is stored while paused reports true.
// A dropped append is a success, not an error: a paused source is working as
// intended. This is the one place where the pause decides what is stored;
// paused must be fail-closed (see pause.IsPaused).
func PauseGate(next Sink, paused func() bool) Sink {
	return &gate{next: next, paused: paused}
}

type gate struct {
	next   Sink
	paused func() bool
}

func (g *gate) Append(ctx context.Context, e event.Event) error {
	if g.paused() {
		return nil
	}
	return g.next.Append(ctx, e)
}

func (g *gate) AppendBatch(ctx context.Context, events []event.Event) error {
	if g.paused() {
		return nil
	}
	return g.next.AppendBatch(ctx, events)
}
