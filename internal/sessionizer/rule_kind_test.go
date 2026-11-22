package sessionizer

import (
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/resolver"
)

func TestDeriveCarriesDecidingRule(t *testing.T) {
	rules := resolver.Rules{
		Repos:         []resolver.RepoRule{{Repo: "/r/ruled", Ticket: "CFG-1"}},
		Learned:       []resolver.LearnedRule{{Repo: "/r/learned", Ticket: "LRN-1"}},
		GlobalProject: "GEN",
	}
	tests := []struct {
		name   string
		events []event.Event
		rules  resolver.Rules
		want   resolver.RuleKind
	}{
		{name: "no context", want: resolver.RuleNone},
		{name: "no rule", events: []event.Event{ctx(0, "/r/a", "spike")}, want: resolver.RuleNone},
		{name: "branch ticket", events: []event.Event{ctx(0, "/r/a", "ABC-1-work")}, rules: rules, want: resolver.RuleBranchTicket},
		{name: "repo rule", events: []event.Event{ctx(0, "/r/ruled", "spike")}, rules: rules, want: resolver.RuleRepo},
		{name: "learned rule", events: []event.Event{ctx(0, "/r/learned", "spike")}, rules: rules, want: resolver.RuleLearned},
		{name: "global default", events: []event.Event{ctx(0, "/r/a", "spike")}, rules: rules, want: resolver.RuleGlobalDefault},
		{name: "manual switch", events: []event.Event{ctx(0, "/r/a", "spike"), manual(0, "PROJ-1")}, rules: rules, want: ManualRule},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocks := Derive(join(presence(0, 30, 1), tt.events), nil, testConfig(), tt.rules)
			if len(blocks) != 1 || blocks[0].Rule != tt.want {
				t.Fatalf("blocks = %+v, want one block decided by %s", blocks, tt.want)
			}
		})
	}
}

func TestDeriveMarksOverrideAndDemotedBlocks(t *testing.T) {
	events := join(presence(0, 30, 1), []event.Event{ctx(0, "/r/a", "ABC-1-work")})
	ov := []event.Override{{Start: at(0), End: at(30), Ticket: "XYZ-9"}}
	blocks := Derive(events, ov, testConfig(), resolver.Rules{})
	if len(blocks) != 1 || blocks[0].Rule != OverrideRule {
		t.Fatalf("override blocks = %+v, want rule %s", blocks, OverrideRule)
	}

	cfg := testConfig()
	cfg.MinBlock = 5 * time.Minute
	short := join(presence(0, 30, 1), []event.Event{ctx(0, "/r/a", "ABC-1-work"), ctx(28, "/r/b", "ABC-2-other")})
	blocks = Derive(short, nil, cfg, resolver.Rules{})
	if len(blocks) != 2 {
		t.Fatalf("blocks = %+v, want the short tail demoted", blocks)
	}
	if last := blocks[1]; last.Ticket != "" || last.Reason != minBlockReason || last.Rule != resolver.RuleNone {
		t.Fatalf("demoted block = %+v, want no deciding rule", last)
	}
}
