package sessionizer

import (
	"testing"

	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/resolver"
)

func titleEvent(m float64, repo, ticket string) event.Event {
	return event.Event{At: at(m), Kind: event.KindContext, Source: "title", Repo: repo, Ticket: ticket}
}

// Context events from the window-title collector need no special handling:
// a repo-only hint resolves through the repo rules (tier rule), a ticket hint
// is an explicit ticket on the event, and a manual pin still wins.
func TestDeriveTitleHintsResolveLikeOtherContextEvents(t *testing.T) {
	rules := resolver.Rules{Repos: []resolver.RepoRule{{Repo: "/r/ide", Ticket: "IDE-1"}}}

	blocks := Derive(join(presence(0, 20, 1), []event.Event{titleEvent(0, "/r/ide", "")}), nil, testConfig(), rules)
	if len(blocks) != 1 || blocks[0].Ticket != "IDE-1" || blocks[0].Tier != resolver.TierRule {
		t.Fatalf("repo hint blocks = %+v", blocks)
	}

	blocks = Derive(join(presence(0, 20, 1), []event.Event{titleEvent(0, "", "WEB-7")}), nil, testConfig(), rules)
	if len(blocks) != 1 || blocks[0].Ticket != "WEB-7" || blocks[0].Tier != resolver.TierExplicit {
		t.Fatalf("ticket hint blocks = %+v", blocks)
	}
}

func TestDeriveManualPinWinsOverTitleHints(t *testing.T) {
	events := join(
		presence(0, 30, 1),
		[]event.Event{manual(0, "PIN-1"), titleEvent(10, "/r/ide", ""), titleEvent(15, "", "WEB-7")},
	)
	rules := resolver.Rules{Repos: []resolver.RepoRule{{Repo: "/r/ide", Ticket: "IDE-1"}}}
	blocks := Derive(events, nil, testConfig(), rules)
	if len(blocks) != 1 || blocks[0].Ticket != "PIN-1" {
		t.Fatalf("blocks = %+v", blocks)
	}
}
