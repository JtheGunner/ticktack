package sessionizer

import (
	"testing"

	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/resolver"
)

// Overrides arrive in creation order. Where they overlap, the one created
// last decides, regardless of where its interval starts.
func TestDeriveLatestCreatedOverrideWins(t *testing.T) {
	assign := func(from, to float64, ticket string) event.Override {
		return event.Override{Start: at(from), End: at(to), Ticket: ticket}
	}
	discard := func(from, to float64) event.Override {
		return event.Override{Start: at(from), End: at(to), Discard: true}
	}
	tests := []struct {
		name      string
		overrides []event.Override
		want      []span
	}{
		{
			name:      "reassigning the merged entry replaces both halves",
			overrides: []event.Override{assign(0, 30, "PRJ-5"), assign(30, 60, "PRJ-5"), assign(0, 60, "PRJ-7")},
			want:      []span{{"PRJ-7", 0, 60}},
		},
		{
			name:      "halves created in reverse start order are replaced too",
			overrides: []event.Override{assign(30, 60, "PRJ-5"), assign(0, 30, "PRJ-5"), assign(0, 60, "PRJ-7")},
			want:      []span{{"PRJ-7", 0, 60}},
		},
		{
			name:      "discarding the merged entry leaves nothing bookable",
			overrides: []event.Override{assign(0, 30, "PRJ-5"), assign(30, 60, "PRJ-5"), discard(0, 60)},
			want:      nil,
		},
		{
			name: "assign, reassign, discard",
			overrides: []event.Override{
				assign(0, 30, "PRJ-5"), assign(30, 60, "PRJ-5"), assign(0, 60, "PRJ-7"), discard(0, 60),
			},
			want: nil,
		},
		{
			name:      "assigning after a discard brings the time back",
			overrides: []event.Override{discard(0, 60), assign(0, 60, "PRJ-7")},
			want:      []span{{"PRJ-7", 0, 60}},
		},
		{
			name:      "later override with a later start wins only where it overlaps",
			overrides: []event.Override{assign(0, 40, "PRJ-5"), assign(20, 60, "PRJ-7")},
			want:      []span{{"PRJ-5", 0, 20}, {"PRJ-7", 20, 60}},
		},
		{
			name:      "later override with an earlier start wins only where it overlaps",
			overrides: []event.Override{assign(20, 60, "PRJ-7"), assign(0, 40, "PRJ-5")},
			want:      []span{{"PRJ-5", 0, 40}, {"PRJ-7", 40, 60}},
		},
		{
			name:      "later override inside an earlier one splits it",
			overrides: []event.Override{assign(0, 60, "PRJ-5"), assign(20, 40, "PRJ-7")},
			want:      []span{{"PRJ-5", 0, 20}, {"PRJ-7", 20, 40}, {"PRJ-5", 40, 60}},
		},
		{
			name:      "later discard inside an earlier assignment cuts a hole",
			overrides: []event.Override{assign(0, 60, "PRJ-5"), discard(20, 40)},
			want:      []span{{"PRJ-5", 0, 20}, {"PRJ-5", 40, 60}},
		},
	}
	events := join(presence(0, 60, 1), []event.Event{ctx(0, "/r/a", "ABC-1-work")})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocks := Derive(events, tt.overrides, testConfig(), resolver.Rules{})
			if len(blocks) != len(tt.want) {
				t.Fatalf("blocks = %+v, want %+v", blocks, tt.want)
			}
			for i, w := range tt.want {
				b := blocks[i]
				if b.Ticket != w.ticket || !b.Start.Equal(at(w.start)) || !b.End.Equal(at(w.end)) {
					t.Fatalf("block %d = %s %v-%v, want %+v (all: %+v)", i, b.Ticket, b.Start, b.End, w, blocks)
				}
				if b.Tier != resolver.TierExplicit {
					t.Fatalf("block %d tier = %s, want explicit", i, b.Tier)
				}
			}
		})
	}
}
