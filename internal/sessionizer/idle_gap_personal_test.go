package sessionizer

import (
	"testing"

	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/resolver"
)

func assertGaps(t *testing.T, got []Interval, want [][2]float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("gaps = %+v, want %v", got, want)
	}
	for i, w := range want {
		if !got[i].Start.Equal(at(w[0])) || !got[i].End.Equal(at(w[1])) {
			t.Errorf("gap %d = %v-%v, want %v-%v", i, got[i].Start, got[i].End, at(w[0]), at(w[1]))
		}
	}
}

// Personal time is not counted and must never turn into an idle gap.
func TestPersonalStretchIsNotAnIdleGap(t *testing.T) {
	cfg := appClassConfig()
	git := ctx(0, "/r/a", "ABC-1-x")
	tests := []struct {
		name   string
		events []event.Event
		want   [][2]float64
	}{
		{
			name:   "personal stretch between two work stretches",
			events: join([]event.Event{git}, presenceApp(0, 30, "ide"), presenceApp(31, 61, "music"), presenceApp(62, 90, "ide")),
			want:   nil,
		},
		{
			name:   "absence, personal, absence: only the absent parts remain",
			events: join([]event.Event{git}, presenceApp(0, 20, "ide"), presenceApp(40, 50, "music"), presenceApp(70, 90, "ide")),
			want:   [][2]float64{{20, 40}, {50, 70}},
		},
		{
			name:   "absence before personal time, work right after",
			events: join([]event.Event{git}, presenceApp(0, 20, "ide"), presenceApp(40, 60, "music"), presenceApp(61, 90, "ide")),
			want:   [][2]float64{{20, 40}},
		},
		{
			name:   "plain absence stays a gap",
			events: join([]event.Event{git}, presenceApp(0, 20, "ide"), presenceApp(40, 60, "ide")),
			want:   [][2]float64{{20, 40}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertGaps(t, DeriveDay(tt.events, nil, cfg, resolver.Rules{}).Gaps, tt.want)
		})
	}
}

func TestFillIsClippedToTheNonPersonalPartOfAGap(t *testing.T) {
	cfg := appClassConfig()
	events := join([]event.Event{ctx(0, "/r/a", "ABC-1-x")},
		presenceApp(0, 20, "ide"), presenceApp(40, 50, "music"), presenceApp(70, 90, "ide"))
	ov := []event.Override{
		{Start: at(15), End: at(80), Ticket: "MEET-1", Kind: event.OverrideFill},
		{Start: at(15), End: at(80), Kind: event.OverrideBreak},
	}
	res := DeriveDay(events, ov[:1], cfg, resolver.Rules{})
	var fills []span
	for _, b := range res.Blocks {
		if b.Ticket == "MEET-1" {
			fills = append(fills, span{b.Ticket, float64(b.Start.Sub(at(0)).Minutes()), float64(b.End.Sub(at(0)).Minutes())})
		}
	}
	if len(fills) != 2 || fills[0] != (span{"MEET-1", 20, 40}) || fills[1] != (span{"MEET-1", 50, 70}) {
		t.Errorf("fill blocks = %+v, want 20-40 and 50-70 (never the personal 40-50)", fills)
	}
	// A break over the same range likewise leaves personal time out.
	res = DeriveDay(events, ov[1:], cfg, resolver.Rules{})
	var idle float64
	for _, b := range res.Breaks {
		if b.Idle {
			idle += b.Duration().Minutes()
		}
	}
	if idle != 40 {
		t.Errorf("idle break minutes = %v, want 40 (20-40 and 50-70)", idle)
	}
}
