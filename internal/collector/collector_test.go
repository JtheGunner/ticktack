package collector

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"
)

type countingSink struct{ appended, batches atomic.Int32 }

func (s *countingSink) Append(context.Context, event.Event) error {
	s.appended.Add(1)
	return nil
}

func (s *countingSink) AppendBatch(context.Context, []event.Event) error {
	s.batches.Add(1)
	return nil
}

type passCollector struct {
	passes atomic.Int32
	err    error
}

func (p *passCollector) Name() string { return "pass" }
func (p *passCollector) Run(context.Context, Sink) error {
	p.passes.Add(1)
	return p.err
}

func TestEveryRunsOnePassAtStartAndThenOnEachTick(t *testing.T) {
	inner := &passCollector{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- Every(5*time.Millisecond, inner, nil).Run(ctx, &countingSink{}) }()
	deadline := time.After(2 * time.Second)
	for inner.passes.Load() < 3 {
		select {
		case <-deadline:
			t.Fatalf("only %d passes", inner.passes.Load())
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("cancellation must be a clean stop, got %v", err)
	}
}

func TestEveryWithoutIntervalMakesOnePass(t *testing.T) {
	inner := &passCollector{}
	if err := Every(0, inner, nil).Run(context.Background(), &countingSink{}); err != nil {
		t.Fatal(err)
	}
	if got := inner.passes.Load(); got != 1 {
		t.Fatalf("passes = %d, want 1", got)
	}
}

func TestEveryReportsAFailedPassAndKeepsGoing(t *testing.T) {
	boom := errors.New("boom")
	inner := &passCollector{err: boom}
	var reported atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() {
		done <- Every(5*time.Millisecond, inner, func(err error) {
			if errors.Is(err, boom) {
				reported.Add(1)
			}
		}).Run(ctx, &countingSink{})
	}()
	deadline := time.After(2 * time.Second)
	for reported.Load() < 2 {
		select {
		case <-deadline:
			t.Fatalf("reported %d failures", reported.Load())
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run = %v", err)
	}
}

func TestSkipWhenDoesNotRunWhilePaused(t *testing.T) {
	inner := &passCollector{}
	paused := true
	c := SkipWhen(func() bool { return paused }, inner)
	if err := c.Run(context.Background(), &countingSink{}); err != nil || inner.passes.Load() != 0 {
		t.Fatalf("paused pass ran: err=%v passes=%d", err, inner.passes.Load())
	}
	paused = false
	if err := c.Run(context.Background(), &countingSink{}); err != nil || inner.passes.Load() != 1 {
		t.Fatalf("pass did not run: err=%v passes=%d", err, inner.passes.Load())
	}
}

func TestPauseGateDropsAppendsAndBatchesWhilePaused(t *testing.T) {
	next := &countingSink{}
	paused := true
	gate := PauseGate(next, func() bool { return paused })
	ctx := context.Background()
	for _, err := range []error{gate.Append(ctx, event.Event{}), gate.AppendBatch(ctx, []event.Event{{}})} {
		if err != nil {
			t.Fatalf("a dropped write is not an error: %v", err)
		}
	}
	if next.appended.Load() != 0 || next.batches.Load() != 0 {
		t.Fatal("stored while paused")
	}
	paused = false
	_ = gate.Append(ctx, event.Event{})
	_ = gate.AppendBatch(ctx, []event.Event{{}})
	if next.appended.Load() != 1 || next.batches.Load() != 1 {
		t.Fatal("did not store after resume")
	}
}
