package sessionizer

import (
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/resolver"
)

func TestFillOnlyCoversIdleGaps(t *testing.T) {
	// Presence 0-30 and 50-80 with a 20 minute hole in between.
	events := join(presence(0, 30, 1), presence(50, 80, 1), []event.Event{ctx(0, "/r/a", "ABC-1-work")})
	fill := func(from, to float64) event.Override {
		return event.Override{Start: at(from), End: at(to), Ticket: "MEET-1", Kind: event.OverrideFill}
	}
	tests := []struct {
		name      string
		overrides []event.Override
		want      []span
	}{
		{"no override leaves the hole", nil, []span{{"ABC-1", 0, 30}, {"ABC-1", 50, 80}}},
		{
			"fill of the whole gap",
			[]event.Override{fill(30, 50)},
			[]span{{"ABC-1", 0, 30}, {"MEET-1", 30, 50}, {"ABC-1", 50, 80}},
		},
		{
			"fill is clipped to the gap and never overlaps presence",
			[]event.Override{fill(20, 65)},
			[]span{{"ABC-1", 0, 30}, {"MEET-1", 30, 50}, {"ABC-1", 50, 80}},
		},
		{
			"fill of part of the gap",
			[]event.Override{fill(35, 45)},
			[]span{{"ABC-1", 0, 30}, {"MEET-1", 35, 45}, {"ABC-1", 50, 80}},
		},
		{
			"fill before the first presence is ignored",
			[]event.Override{fill(-30, -5)},
			[]span{{"ABC-1", 0, 30}, {"ABC-1", 50, 80}},
		},
		{
			"fill after the last presence is ignored",
			[]event.Override{fill(85, 120)},
			[]span{{"ABC-1", 0, 30}, {"ABC-1", 50, 80}},
		},
		{
			"fill purely inside presence is ignored",
			[]event.Override{fill(5, 25)},
			[]span{{"ABC-1", 0, 30}, {"ABC-1", 50, 80}},
		},
		{
			"discard after a fill removes it again",
			[]event.Override{fill(30, 50), {Start: at(30), End: at(50), Kind: event.OverrideDiscard}},
			[]span{{"ABC-1", 0, 30}, {"ABC-1", 50, 80}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocks := Derive(events, tt.overrides, testConfig(), resolver.Rules{})
			if len(blocks) != len(tt.want) {
				t.Fatalf("blocks = %+v, want %+v", blocks, tt.want)
			}
			for i, w := range tt.want {
				b := blocks[i]
				if b.Ticket != w.ticket || !b.Start.Equal(at(w.start)) || !b.End.Equal(at(w.end)) {
					t.Fatalf("block %d = %s %v-%v, want %+v", i, b.Ticket, b.Start, b.End, w)
				}
				if b.Ticket == "MEET-1" && (b.Tier != resolver.TierExplicit || b.Reason != FillReason || b.Rule != FillRule) {
					t.Errorf("fill block = tier %s reason %q rule %q, want explicit %q fill", b.Tier, b.Reason, b.Rule, FillReason)
				}
			}
		})
	}
}

func TestOpenGapsExcludeWhatOverridesCover(t *testing.T) {
	events := join(presence(0, 30, 1), presence(50, 80, 1), []event.Event{ctx(0, "/r/a", "ABC-1-work")})
	tests := []struct {
		name      string
		overrides []event.Override
		want      []Interval
	}{
		{"untouched", nil, []Interval{{at(30), at(50)}}},
		{"filled", []event.Override{{Start: at(30), End: at(50), Ticket: "X-1", Kind: event.OverrideFill}}, nil},
		{"partly filled", []event.Override{{Start: at(30), End: at(40), Ticket: "X-1", Kind: event.OverrideFill}}, []Interval{{at(40), at(50)}}},
		{"break", []event.Override{{Start: at(30), End: at(50), Kind: event.OverrideBreak}}, nil},
		{"discard", []event.Override{{Start: at(30), End: at(50), Discard: true}}, nil},
		{"reassign of presence leaves the gap", []event.Override{{Start: at(0), End: at(30), Ticket: "X-1"}}, []Interval{{at(30), at(50)}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeriveDay(events, tt.overrides, testConfig(), resolver.Rules{}).Gaps
			if len(got) != len(tt.want) {
				t.Fatalf("gaps = %+v, want %+v", got, tt.want)
			}
			for i := range got {
				if !got[i].Start.Equal(tt.want[i].Start) || !got[i].End.Equal(tt.want[i].End) {
					t.Errorf("gap %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestHolesShorterThanTheGapThresholdAreNotGaps(t *testing.T) {
	// A gap marker ends the span although the hole is only 3 minutes.
	events := join(presence(0, 10, 1), presence(13, 20, 1), []event.Event{
		ctx(0, "/r/a", "ABC-1-work"),
		{At: at(13), Kind: event.KindGap, IdleSeconds: 180},
	})
	res := DeriveDay(events, []event.Override{{Start: at(10), End: at(13), Ticket: "X-1", Kind: event.OverrideFill}}, testConfig(), resolver.Rules{})
	if len(res.Gaps) != 0 {
		t.Errorf("gaps = %+v, want none below the threshold", res.Gaps)
	}
	for _, b := range res.Blocks {
		if b.Ticket == "X-1" {
			t.Errorf("fill of a sub-threshold hole produced %+v", b)
		}
	}
}

func TestBreaksAreReportedNotBooked(t *testing.T) {
	events := join(presence(0, 30, 1), presence(50, 80, 1), []event.Event{ctx(0, "/r/a", "ABC-1-work")})
	ov := []event.Override{
		{Start: at(20), End: at(60), Kind: event.OverrideBreak},
	}
	res := DeriveDay(events, ov, testConfig(), resolver.Rules{})
	var booked time.Duration
	for _, b := range res.Blocks {
		booked += b.Duration()
	}
	if booked != 40*time.Minute {
		t.Errorf("booked = %v, want 40m (presence 0-20 and 60-80)", booked)
	}
	want := []Break{{Start: at(20), End: at(30)}, {Start: at(30), End: at(50), Idle: true}, {Start: at(50), End: at(60)}}
	if len(res.Breaks) != len(want) {
		t.Fatalf("breaks = %+v, want %+v", res.Breaks, want)
	}
	for i, w := range want {
		g := res.Breaks[i]
		if !g.Start.Equal(w.Start) || !g.End.Equal(w.End) || g.Idle != w.Idle {
			t.Errorf("break %d = %+v, want %+v", i, g, w)
		}
	}
	// A later reassign takes the time out of the break again.
	ov = append(ov, event.Override{Start: at(20), End: at(30), Ticket: "X-1"})
	res = DeriveDay(events, ov, testConfig(), resolver.Rules{})
	if len(res.Breaks) != 2 || !res.Breaks[0].Start.Equal(at(30)) {
		t.Errorf("breaks after reassign = %+v", res.Breaks)
	}
}

func TestLegacyDiscardFlagMeansDiscard(t *testing.T) {
	o := event.Override{Discard: true}
	if o.EffectiveKind() != event.OverrideDiscard || !o.Drops() {
		t.Errorf("kind = %q", o.EffectiveKind())
	}
	if k := (event.Override{Ticket: "A-1"}).EffectiveKind(); k != event.OverrideReassign {
		t.Errorf("kind = %q", k)
	}
}
