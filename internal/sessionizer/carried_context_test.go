package sessionizer

import (
	"testing"

	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/resolver"
)

// The day loader passes the last context events from before the day as
// ordinary leading events. These tests pin down what Derive makes of them.

func TestDeriveLeadingContextStartsAtFirstPresence(t *testing.T) {
	// testConfig has no age limit for automatic context. A pin of an earlier
	// day still decides the ticket, but only as a suggestion.
	tests := []struct {
		name    string
		leading []event.Event
		ticket  string
		tier    resolver.Tier
		reason  string
	}{
		{
			name:    "branch from the day before",
			leading: []event.Event{ctx(-900, "/r/a", "ABC-1-work")},
			ticket:  "ABC-1",
			tier:    resolver.TierExplicit,
		},
		{
			name:    "pin from the evening before beats the branch",
			leading: []event.Event{ctx(-900, "/r/a", "ABC-1-work"), manual(-600, "pin-1")},
			ticket:  "PIN-1",
			tier:    resolver.TierSuggested,
			reason:  "context unconfirmed for 10h",
		},
		{
			name:    "pin older than the branch still pins",
			leading: []event.Event{manual(-900, "pin-1"), ctx(-600, "/r/a", "ABC-1-work")},
			ticket:  "PIN-1",
			tier:    resolver.TierSuggested,
			reason:  "context unconfirmed for 15h",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocks := Derive(join(tt.leading, presence(0, 30, 1)), nil, testConfig(), resolver.Rules{})
			if len(blocks) != 1 {
				t.Fatalf("want 1 block, got %+v", blocks)
			}
			b := blocks[0]
			if !b.Start.Equal(at(0)) || !b.End.Equal(at(30)) {
				t.Errorf("block range = %v..%v, want first to last presence", b.Start, b.End)
			}
			if b.Ticket != tt.ticket || b.Tier != tt.tier {
				t.Errorf("block = %+v", b)
			}
			if tt.reason != "" && b.Reason != tt.reason {
				t.Errorf("reason = %q, want %q", b.Reason, tt.reason)
			}
		})
	}
}

func TestDeriveLeadingContextAloneCreatesNoTime(t *testing.T) {
	events := []event.Event{ctx(-900, "/r/a", "ABC-1-work"), manual(-600, "PIN-1")}
	if blocks := Derive(events, nil, testConfig(), resolver.Rules{}); len(blocks) != 0 {
		t.Fatalf("context without presence produced blocks: %+v", blocks)
	}
}

func TestDeriveLeadingContextYieldsToTheDaysOwnContext(t *testing.T) {
	events := join(
		[]event.Event{ctx(-900, "/r/a", "ABC-1-work")},
		presence(0, 40, 1),
		[]event.Event{ctx(20, "/r/b", "ABC-2-other")},
	)
	blocks := Derive(events, nil, testConfig(), resolver.Rules{})
	if len(blocks) != 2 {
		t.Fatalf("want 2 blocks, got %+v", blocks)
	}
	if blocks[0].Ticket != "ABC-1" || !blocks[0].Start.Equal(at(0)) || !blocks[0].End.Equal(at(20)) {
		t.Errorf("carried block = %+v", blocks[0])
	}
	if blocks[1].Ticket != "ABC-2" || !blocks[1].End.Equal(at(40)) {
		t.Errorf("own block = %+v", blocks[1])
	}
}
