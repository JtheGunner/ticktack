package presence

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"
)

type memorySink struct {
	mu     sync.Mutex
	events []event.Event
	err    error
}

func (m *memorySink) Append(_ context.Context, e event.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.events = append(m.events, e)
	return nil
}

var t0 = time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)

type harness struct {
	agent  *Agent
	sink   *memorySink
	fake   *Fake
	now    time.Time
	paused bool
	logs   []string
}

func newHarness() *harness {
	h := &harness{sink: &memorySink{}, fake: &Fake{}, now: t0}
	h.agent = &Agent{
		Sampler:  h.fake,
		Sink:     h.sink,
		Interval: 30 * time.Second,
		Now:      func() time.Time { return h.now },
		Paused:   func() bool { return h.paused },
		Logf:     func(format string, _ ...any) { h.logs = append(h.logs, format) },
	}
	return h
}

func (h *harness) tick(d time.Duration) {
	h.now = h.now.Add(d)
	h.agent.Tick(context.Background())
}

func TestAgentAppendsOnlyWhenNotPaused(t *testing.T) {
	h := newHarness()
	h.fake.Push(Sample{Idle: 4 * time.Second, App: "com.apple.Terminal"})
	h.fake.Push(Sample{Idle: 1 * time.Second, App: "com.apple.Terminal"})
	h.tick(0)
	h.paused = true
	h.tick(30 * time.Second)
	h.paused = false
	h.tick(30 * time.Second)
	if len(h.sink.events) != 2 {
		t.Fatalf("got %d events, want 2: %+v", len(h.sink.events), h.sink.events)
	}
	if h.fake.Calls() != 2 {
		t.Fatalf("sampler called %d times, want 2 (not while paused)", h.fake.Calls())
	}
	e := h.sink.events[0]
	if e.Source != "presence" || e.Kind != event.KindPresence || e.IdleSeconds != 4 || !e.At.Equal(t0) {
		t.Fatalf("unexpected event %+v", e)
	}
}

func TestAgentLongPauseIsNotAGap(t *testing.T) {
	h := newHarness()
	h.fake.Push(Sample{App: "a"})
	h.fake.Push(Sample{App: "a"})
	h.tick(0)
	h.paused = true
	for i := 0; i < 20; i++ {
		h.tick(30 * time.Second)
	}
	h.paused = false
	h.tick(30 * time.Second)
	for _, e := range h.sink.events {
		if e.Kind == event.KindGap {
			t.Fatalf("pause misread as gap: %+v", e)
		}
	}
}

func TestAgentSkipsSampleOnError(t *testing.T) {
	h := newHarness()
	h.fake.PushError(errors.New("ioreg failed"))
	h.fake.Push(Sample{App: "a"})
	h.tick(0)
	if len(h.sink.events) != 0 {
		t.Fatalf("expected no event after sample error, got %+v", h.sink.events)
	}
	if len(h.logs) == 0 {
		t.Fatal("sample error was not logged")
	}
	h.tick(30 * time.Second)
	if len(h.sink.events) != 1 {
		t.Fatalf("agent did not recover: %+v", h.sink.events)
	}
}

func TestAgentLogsAppendFailure(t *testing.T) {
	h := newHarness()
	h.sink.err = errors.New("disk full")
	h.fake.Push(Sample{App: "a"})
	h.tick(0)
	if len(h.logs) == 0 {
		t.Fatal("append error was not logged")
	}
}

func TestAgentDetectsSleepGap(t *testing.T) {
	h := newHarness()
	h.fake.Push(Sample{App: "a"})
	h.fake.Push(Sample{App: "a"})
	h.tick(0)
	h.tick(8*time.Hour + 20*time.Second) // process was suspended
	got := h.sink.events
	if len(got) != 3 {
		t.Fatalf("want presence, gap, presence; got %+v", got)
	}
	gap := got[1]
	if gap.Kind != event.KindGap || gap.Source != "presence" {
		t.Fatalf("second event is not a gap marker: %+v", gap)
	}
	if want := int((8*time.Hour + 20*time.Second).Seconds()); gap.IdleSeconds != want {
		t.Fatalf("gap length %d, want %d", gap.IdleSeconds, want)
	}
	if !gap.At.Equal(h.now) {
		t.Fatalf("gap At %v, want resume time %v", gap.At, h.now)
	}
	presence := 0
	for _, e := range got {
		if e.Kind == event.KindPresence {
			presence++
		}
	}
	if presence != 2 {
		t.Fatalf("samples were invented for the gap: %+v", got)
	}
}

func TestAgentNoGapAtThreshold(t *testing.T) {
	h := newHarness()
	h.fake.Push(Sample{App: "a"})
	h.fake.Push(Sample{App: "a"})
	h.tick(0)
	h.tick(3 * h.agent.Interval) // exactly 3x is not larger than 3x
	for _, e := range h.sink.events {
		if e.Kind == event.KindGap {
			t.Fatalf("unexpected gap: %+v", e)
		}
	}
}

func TestAgentStoresBundleIDOnly(t *testing.T) {
	h := newHarness()
	h.fake.Push(Sample{Idle: 2 * time.Second, App: "com.jetbrains.goland"})
	h.tick(0)
	want := event.Event{Source: "presence", Kind: event.KindPresence, App: "com.jetbrains.goland", IdleSeconds: 2, At: t0}
	if len(h.sink.events) != 1 || h.sink.events[0] != want {
		t.Fatalf("got %+v, want %+v", h.sink.events, want)
	}
}

func TestAgentRunStopsOnContextCancel(t *testing.T) {
	h := newHarness()
	h.fake.Push(Sample{App: "a"})
	ticks := make(chan time.Time)
	h.agent.Ticks = ticks
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.agent.Run(ctx) }()
	ticks <- h.now
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
	if len(h.sink.events) < 1 {
		t.Fatal("expected at least the immediate sample")
	}
}

// A monotonic clock reading does not advance during macOS sleep, so the agent
// must work on wall-clock times only. Such a reading cannot be forged without
// real sleeping, so the test asserts that no monotonic reading survives.
func TestAgentUsesWallClockOnly(t *testing.T) {
	h := newHarness()
	h.agent.Now = time.Now // carries a monotonic reading
	h.fake.Push(Sample{App: "a"})
	h.agent.Tick(context.Background())
	if len(h.sink.events) != 1 {
		t.Fatalf("got %+v", h.sink.events)
	}
	if s := h.sink.events[0].At.String(); strings.Contains(s, "m=") {
		t.Fatalf("stored time carries a monotonic reading: %s", s)
	}
}
