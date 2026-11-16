package explain

import (
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/resolver"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
)

var t0 = time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)

func presenceRun(from time.Time, minutes int) []event.Event {
	var evs []event.Event
	for m := 0; m <= minutes; m++ {
		evs = append(evs, event.Event{At: from.Add(time.Duration(m) * time.Minute), Kind: event.KindPresence, Source: "presence"})
	}
	return evs
}

func TestBlockNamesDecidingRule(t *testing.T) {
	rules := resolver.Rules{
		Repos:   []resolver.RepoRule{{Repo: "/cfg", Ticket: "CFG-1"}},
		Learned: []resolver.LearnedRule{{ID: 7, Repo: "/learn", Branch: "dev", Ticket: "LRN-7"}},
	}
	tests := []struct {
		name        string
		ctx         event.Event
		wantRule    resolver.RuleKind
		wantSource  Source
		wantSkipped int
		wantID      int64
	}{
		{"event ticket", event.Event{Repo: "/x", Branch: "main", Ticket: "evt-1"}, resolver.RuleEventTicket, SourceContext, 0, 0},
		{"branch ticket", event.Event{Repo: "/x", Branch: "ABC-2-fix"}, resolver.RuleBranchTicket, SourceContext, 1, 0},
		{"repo rule", event.Event{Repo: "/cfg/sub", Branch: "main"}, resolver.RuleRepo, SourceContext, 2, 0},
		{"learned rule", event.Event{Repo: "/learn", Branch: "dev"}, resolver.RuleLearned, SourceContext, 3, 7},
		{"unknown", event.Event{Repo: "/none", Branch: "main"}, resolver.RuleNone, SourceContext, 4, 0},
		{"manual", event.Event{Source: sessionizer.ManualSource, Ticket: "MAN-1"}, sessionizer.ManualRule, SourceContext, 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := tc.ctx
			ctx.At, ctx.Kind = t0, event.KindContext
			if ctx.Source == "" {
				ctx.Source = "shell"
			}
			events := append([]event.Event{ctx}, presenceRun(t0, 30)...)
			cfg := sessionizer.DefaultConfig()
			blocks := sessionizer.Derive(events, nil, cfg, rules)
			if len(blocks) != 1 {
				t.Fatalf("blocks = %+v", blocks)
			}
			ex := Block(Input{Block: blocks[0], Events: events, Rules: rules, Config: cfg})
			if ex.Source != tc.wantSource || ex.Decision.Rule != tc.wantRule || len(ex.Skipped) != tc.wantSkipped || ex.Decision.LearnedID != tc.wantID {
				t.Fatalf("explanation = %+v", ex)
			}
			if ex.Event.Source != ctx.Source || !ex.Event.At.Equal(t0) {
				t.Errorf("deciding event = %+v", ex.Event)
			}
			if ex.Presence.Samples == 0 {
				t.Error("no presence evidence")
			}
		})
	}
}

func TestBlockSourcesWithoutContext(t *testing.T) {
	cfg := sessionizer.DefaultConfig()
	events := presenceRun(t0, 30)
	blocks := sessionizer.Derive(events, nil, cfg, resolver.Rules{})
	ex := Block(Input{Block: blocks[0], Events: events, Config: cfg})
	if ex.Source != SourceNoContext {
		t.Fatalf("source = %v", ex.Source)
	}

	ov := []event.Override{{Start: t0, End: t0.Add(10 * time.Minute), Ticket: "OVR-1"}}
	blocks = sessionizer.Derive(events, ov, cfg, resolver.Rules{})
	ex = Block(Input{Block: blocks[0], Events: events, Overrides: ov, Config: cfg})
	if ex.Source != SourceOverride || ex.Override.Ticket != "OVR-1" {
		t.Fatalf("override explanation = %+v", ex)
	}
}

func TestPresenceEvidenceReportsGapsAndNeighbours(t *testing.T) {
	cfg := sessionizer.DefaultConfig()
	events := []event.Event{{At: t0, Kind: event.KindContext, Source: "shell", Repo: "/r", Branch: "ABC-1"}}
	events = append(events, presenceRun(t0, 20)...)
	resume := t0.Add(60 * time.Minute)
	events = append(events, event.Event{At: resume, Kind: event.KindGap, IdleSeconds: 39 * 60, Source: "presence"})
	events = append(events, presenceRun(resume, 5)...)
	blocks := sessionizer.Derive(events, nil, cfg, resolver.Rules{})
	if len(blocks) != 2 {
		t.Fatalf("blocks = %+v", blocks)
	}
	ex := Block(Input{Block: blocks[0], Events: events, Config: cfg})
	if len(ex.Presence.Gaps) != 1 || ex.Presence.Next.IsZero() == false && !ex.Presence.Next.Equal(resume) {
		t.Fatalf("presence = %+v", ex.Presence)
	}
}
