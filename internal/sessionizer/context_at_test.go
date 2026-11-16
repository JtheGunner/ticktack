package sessionizer

import (
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/resolver"
)

func TestContextAtAgreesWithDerive(t *testing.T) {
	base := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	at := func(m int) time.Time { return base.Add(time.Duration(m) * time.Minute) }
	auto := func(m int, repo, branch string) event.Event {
		return event.Event{At: at(m), Source: "shell", Kind: event.KindContext, Repo: repo, Branch: branch}
	}
	manual := func(m int, ticket string) event.Event {
		return event.Event{At: at(m), Source: ManualSource, Kind: event.KindContext, Ticket: ticket}
	}
	hint := event.Event{At: at(5), Source: "aw", Kind: event.KindContext, App: "Editor"}
	rules := resolver.Rules{Repos: []resolver.RepoRule{{Repo: "/a", Ticket: "AAA-1"}, {Repo: "/b", Ticket: "BBB-1"}}}
	tests := []struct {
		name   string
		events []event.Event
	}{
		{"plain context change", []event.Event{auto(0, "/a", "main"), auto(10, "/b", "main")}},
		{"manual pin", []event.Event{auto(0, "/a", "main"), manual(10, "pin-1")}},
		{"cleared pin", []event.Event{auto(0, "/a", "main"), manual(10, "PIN-1"), manual(20, "")}},
		{"app-only hint ignored", []event.Event{auto(0, "/a", "main"), hint}},
		{"non-manual event while pinned", []event.Event{auto(0, "/a", "main"), manual(5, "PIN-1"), auto(10, "/b", "main")}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			events := tc.events
			for m := 0; m <= 30; m++ {
				events = append(events, event.Event{At: at(m), Source: "presence", Kind: event.KindPresence})
			}
			cfg := Config{MinBlock: time.Second}
			blocks := Derive(events, nil, cfg, rules)
			if len(blocks) == 0 {
				t.Fatal("no blocks")
			}
			for _, b := range blocks {
				cd, ok := ContextAt(events, rules, b.Start)
				if !ok {
					t.Fatalf("no context at %v", b.Start)
				}
				if cd.Decision.Ticket != b.Ticket || cd.Decision.Tier != b.Tier || cd.Decision.Reason != b.Reason {
					t.Errorf("at %v: ContextAt %+v, Derive %+v", b.Start, cd.Decision.Attribution, b)
				}
			}
		})
	}
}

func TestContextAtBeforeFirstContext(t *testing.T) {
	if _, ok := ContextAt(nil, resolver.Rules{}, time.Now()); ok {
		t.Fatal("expected no context")
	}
}
