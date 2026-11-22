package sessionizer

import (
	"regexp"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/resolver"
)

// ticketMinutes sums the block time per ticket ("" is unassigned time).
func ticketMinutes(blocks []Block) map[string]time.Duration {
	got := map[string]time.Duration{}
	for _, b := range blocks {
		got[b.Ticket] += b.Duration()
	}
	return got
}

// patternChangeHistory is a stored history as existing databases hold it:
// hook ("shell") and scanner ("git") events carry the branch together with
// the ticket that the pattern of their capture time extracted from it. The
// title hint and the manual pin carry a ticket that is not from a branch.
func patternChangeHistory() []event.Event {
	return join(presence(0, 99, 1), []event.Event{
		{At: at(0), Kind: event.KindContext, Source: "shell", Repo: "/r/a", Branch: "feat/ABC-1-PRJ_7", Ticket: "ABC-1"},
		{At: at(20), Kind: event.KindContext, Source: "git", Repo: "/r/a", Branch: "fix/ABC-2", Ticket: "ABC-2"},
		{At: at(40), Kind: event.KindContext, Source: "git", Repo: "/r/a", Branch: "chore/PRJ_9"},
		{At: at(60), Kind: event.KindContext, Source: TitleSource, Ticket: "ABC-5"},
		{At: at(80), Kind: event.KindContext, Source: ManualSource, Ticket: "ABC-6"},
	})
}

func TestDeriveRederivesBranchTicketsWhenThePatternChanges(t *testing.T) {
	overrides := []event.Override{
		{Start: at(5), End: at(10), Ticket: "ZZZ-9"},
		{Start: at(25), End: at(30), Discard: true},
	}
	repoRule := []resolver.RepoRule{{Repo: "/r/a", Ticket: "RULE-1"}}
	tests := []struct {
		name    string
		pattern *regexp.Regexp
		want    map[string]time.Duration
	}{
		{
			name:    "pattern of the capture time",
			pattern: nil,
			want: map[string]time.Duration{
				"ABC-1":  15 * time.Minute, // 0-20 minus the override
				"ABC-2":  15 * time.Minute, // 20-40 minus the discard
				"RULE-1": 20 * time.Minute, // chore/PRJ_9 is no ticket yet
				"ABC-5":  20 * time.Minute, // title hint
				"ABC-6":  19 * time.Minute, // manual pin
				"ZZZ-9":  5 * time.Minute,
			},
		},
		{
			name:    "changed pattern",
			pattern: regexp.MustCompile(`PRJ_\d+`),
			want: map[string]time.Duration{
				"PRJ_7":  15 * time.Minute, // the stored ABC-1 no longer counts
				"RULE-1": 15 * time.Minute, // fix/ABC-2 falls back to the repo rule
				"PRJ_9":  20 * time.Minute, // stored without a ticket, matches now
				"ABC-5":  20 * time.Minute, // not from a branch: kept
				"ABC-6":  19 * time.Minute, // not from a branch: kept
				"ZZZ-9":  5 * time.Minute,  // override survives the re-derivation
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := patternChangeHistory()
			stored := append([]event.Event(nil), events...)
			rules := resolver.Rules{Repos: repoRule, TicketPattern: tt.pattern}
			got := ticketMinutes(Derive(events, overrides, testConfig(), rules))
			if len(got) != len(tt.want) {
				t.Fatalf("minutes = %v, want %v", got, tt.want)
			}
			for ticket, want := range tt.want {
				if got[ticket] != want {
					t.Errorf("%q = %v, want %v (all: %v)", ticket, got[ticket], want, got)
				}
			}
			for i := range events {
				if events[i] != stored[i] {
					t.Fatalf("event %d was rewritten: %+v, stored %+v", i, events[i], stored[i])
				}
			}
		})
	}
}
