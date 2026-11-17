package sessionizer

import (
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/resolver"
)

// idleRun returns presence samples every step minutes from..to inclusive. The
// idle time of each sample counts from lastInput (in minutes); samples taken
// before lastInput report no idle time.
func idleRun(from, to, step, lastInput float64) []event.Event {
	var out []event.Event
	for m := from; m <= to; m += step {
		idle := 0.0
		if m > lastInput {
			idle = m - lastInput
		}
		out = append(out, event.Event{At: at(m), Kind: event.KindPresence, IdleSeconds: int(idle * 60)})
	}
	return out
}

func deriveIdle(parts ...[]event.Event) []Block {
	events := join(append(parts, []event.Event{ctx(0, "/r/a", "ABC-1-work")})...)
	return Derive(events, nil, testConfig(), resolver.Rules{})
}

func wantBlocks(t *testing.T, got []Block, want ...[2]float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("want %d blocks %v, got %+v", len(want), want, got)
	}
	for i, w := range want {
		if !got[i].Start.Equal(at(w[0])) || !got[i].End.Equal(at(w[1])) {
			t.Fatalf("block %d = %v..%v, want %v..%v (all: %+v)", i, got[i].Start, got[i].End, at(w[0]), at(w[1]), got)
		}
	}
}

func TestDeriveEndsBlockAtLastInput(t *testing.T) {
	// Active until minute 10, then away until minute 50. The idle counter
	// grows while away; time after the last input is not work.
	blocks := deriveIdle(
		idleRun(0, 10, 0.5, 10),
		idleRun(10.5, 49.5, 0.5, 10),
		idleRun(50, 60, 0.5, 60),
	)
	wantBlocks(t, blocks, [2]float64{0, 10}, [2]float64{50, 60})
}

func TestDeriveDoesNotCountAbsenceLongerThanGapThreshold(t *testing.T) {
	blocks := deriveIdle(
		idleRun(0, 10, 0.5, 10),
		idleRun(10.5, 15.5, 0.5, 10),
		idleRun(16, 20, 0.5, 20),
	)
	wantBlocks(t, blocks, [2]float64{0, 10}, [2]float64{16, 20})
	if got := total(blocks); got != 14*time.Minute {
		t.Fatalf("total = %v, want 14m", got)
	}
}

func TestDeriveBridgesAbsenceUpToGapThreshold(t *testing.T) {
	blocks := deriveIdle(
		idleRun(0, 10, 0.5, 10),
		idleRun(10.5, 13.5, 0.5, 10),
		idleRun(14, 20, 0.5, 20),
	)
	wantBlocks(t, blocks, [2]float64{0, 20})
}

func TestDeriveDoesNotBackdateIntoSleepGap(t *testing.T) {
	// The first sample after the wake-up still reports the idle time from
	// before the sleep (3 minutes). The activity it points at lies inside the
	// gap and must not be counted.
	blocks := deriveIdle(
		idleRun(0, 10, 0.5, 10),
		[]event.Event{
			{At: at(40), Kind: event.KindGap, IdleSeconds: 1800},
			{At: at(40), Kind: event.KindPresence, IdleSeconds: 180},
		},
		idleRun(40.5, 45, 0.5, 45),
	)
	wantBlocks(t, blocks, [2]float64{0, 10}, [2]float64{40, 45})
}

func TestDeriveStartsSpanAtItsFirstSample(t *testing.T) {
	// The first sample after a gap reports 4 minutes of idle time. The span
	// starts at the sample, not at the stale input before it, so a block never
	// begins ahead of the context event the returning user triggered.
	blocks := deriveIdle(
		[]event.Event{{At: at(30), Kind: event.KindPresence, IdleSeconds: 240}},
		idleRun(30.5, 35, 0.5, 35),
	)
	wantBlocks(t, blocks, [2]float64{30, 35})
}

func TestConfigFromKeepsIdleThresholdIndependentOfGapThreshold(t *testing.T) {
	cfg := ConfigFrom(config.Config{GapThreshold: 10 * time.Minute})
	if cfg.GapThreshold != 10*time.Minute || cfg.IdleThreshold != config.DefaultIdleThreshold {
		t.Fatalf("gap threshold must not move the idle threshold: %+v", cfg)
	}
	cfg = ConfigFrom(config.Config{GapThreshold: 10 * time.Minute, IdleThreshold: 2 * time.Minute})
	if cfg.IdleThreshold != 2*time.Minute {
		t.Fatalf("IdleThreshold = %v, want 2m", cfg.IdleThreshold)
	}
}
