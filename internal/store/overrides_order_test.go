package store

import (
	"context"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"
)

// The sessionizer applies overrides in input order and lets later ones win,
// so the store has to hand them out in creation order, not by start time.
func TestOverridesAreOrderedByCreation(t *testing.T) {
	hour := func(h int) time.Time { return base.Add(time.Duration(h) * time.Hour) }
	tests := []struct {
		name string
		add  []event.Override
	}{
		{
			name: "later override with an earlier start stays last",
			add: []event.Override{
				{Start: hour(2), End: hour(3), Ticket: "PRJ-5"},
				{Start: hour(1), End: hour(2), Ticket: "PRJ-5"},
				{Start: hour(1), End: hour(3), Ticket: "PRJ-7"},
			},
		},
		{
			name: "discard created last stays last despite the earliest start",
			add: []event.Override{
				{Start: hour(1), End: hour(2), Ticket: "PRJ-5"},
				{Start: hour(2), End: hour(3), Ticket: "PRJ-5"},
				{Start: hour(0), End: hour(3), Discard: true},
			},
		},
		{
			name: "same start keeps creation order",
			add: []event.Override{
				{Start: hour(1), End: hour(3), Ticket: "PRJ-7"},
				{Start: hour(1), End: hour(2), Ticket: "PRJ-5"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := openTemp(t)
			ctx := context.Background()
			for _, o := range tt.add {
				if err := s.AddOverride(ctx, o); err != nil {
					t.Fatal(err)
				}
			}
			got, err := s.Overrides(ctx, base, base.Add(24*time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tt.add) {
				t.Fatalf("got %d overrides, want %d: %+v", len(got), len(tt.add), got)
			}
			for i, want := range tt.add {
				g := got[i]
				if !g.Start.Equal(want.Start) || !g.End.Equal(want.End) || g.Ticket != want.Ticket || g.Discard != want.Discard {
					t.Fatalf("override %d = %+v, want %+v (all: %+v)", i, g, want, got)
				}
			}
		})
	}
}
