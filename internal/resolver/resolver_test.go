package resolver

import "testing"

var testRules = Rules{
	Repos: []RepoRule{
		{Repo: "/work", Ticket: "WORK-1"},
		{Repo: "/work/app", Ticket: "APP-1"},
		{Repo: "/work/app/sub", Ticket: "SUB-1"},
	},
}

func TestResolveExplicitBeatsRule(t *testing.T) {
	got := Resolve(ContextSignal{Repo: "/work/app", Branch: "feature/PROJ-123-foo"}, testRules)
	if got.Ticket != "PROJ-123" || got.Tier != TierExplicit {
		t.Errorf("got %+v", got)
	}
	got = Resolve(ContextSignal{Repo: "/work/app", Branch: "feature/PROJ-123", Ticket: "evt-9"}, testRules)
	if got.Ticket != "EVT-9" || got.Tier != TierExplicit {
		t.Errorf("event ticket should win: %+v", got)
	}
}

func TestResolveFallsBackToRepoRule(t *testing.T) {
	tests := []struct {
		name, repo, want string
	}{
		{"exact", "/work/app", "APP-1"},
		{"longest prefix", "/work/app/sub/deep", "SUB-1"},
		{"parent", "/work/other", "WORK-1"},
		{"no path-boundary match", "/work/application", "WORK-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Resolve(ContextSignal{Repo: tt.repo, Branch: "main"}, testRules)
			if got.Ticket != tt.want || got.Tier != TierRule {
				t.Errorf("got %+v, want %s", got, tt.want)
			}
		})
	}
}

func TestResolveLearnedBetweenRuleAndUnknown(t *testing.T) {
	rules := Rules{
		Repos:   []RepoRule{{Repo: "/a", Ticket: "RULE-1"}},
		Learned: []LearnedRule{{Repo: "/a", Branch: "dev", Ticket: "LRN-1"}, {Repo: "/b", Ticket: "LRN-2"}},
	}
	if got := Resolve(ContextSignal{Repo: "/a", Branch: "dev"}, rules); got.Tier != TierRule {
		t.Errorf("rule must beat learned: %+v", got)
	}
	got := Resolve(ContextSignal{Repo: "/b/x", Branch: "dev"}, rules)
	if got.Ticket != "LRN-2" || got.Tier != TierLearned {
		t.Errorf("got %+v", got)
	}
	if got := Resolve(ContextSignal{Repo: "/c"}, rules); got.Tier != TierUnknown {
		t.Errorf("got %+v", got)
	}
}

func TestResolveUnknownWhenNothingMatches(t *testing.T) {
	got := Resolve(ContextSignal{Repo: "/elsewhere", Branch: "main"}, testRules)
	if got.Ticket != "" || got.Tier != TierUnknown {
		t.Errorf("got %+v", got)
	}
	if got := Resolve(ContextSignal{}, Rules{}); got.Tier != TierUnknown {
		t.Errorf("empty input: %+v", got)
	}
}

func TestResolveReasonNamesTheDecidingSignal(t *testing.T) {
	tests := []struct {
		name   string
		signal ContextSignal
		want   string
	}{
		{"branch", ContextSignal{Repo: "/work/app", Branch: "feature/PROJ-123-foo"}, `ticket PROJ-123 found in branch "feature/PROJ-123-foo"`},
		{"event", ContextSignal{Repo: "/work/app", Ticket: "X-1"}, "explicit ticket X-1 on event"},
		{"rule", ContextSignal{Repo: "/work/app/sub", Branch: "main"}, "repo rule for /work/app/sub"},
		{"unknown", ContextSignal{Repo: "/zzz", Branch: "main"}, "no ticket in branch and no rule for repo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Resolve(tt.signal, testRules); got.Reason != tt.want {
				t.Errorf("reason = %q, want %q", got.Reason, tt.want)
			}
		})
	}
}

func TestResolveBranchTable(t *testing.T) {
	tests := []struct {
		branch, want string
		tier         Tier
	}{
		{"fix/PROJ-1-and-PROJ-2", "PROJ-1", TierExplicit},
		{"fix/proj-12", "PROJ-12", TierExplicit},
		{"main", "APP-1", TierRule},
		{"", "APP-1", TierRule},
	}
	for _, tt := range tests {
		t.Run(tt.branch, func(t *testing.T) {
			got := Resolve(ContextSignal{Repo: "/work/app", Branch: tt.branch}, testRules)
			if got.Ticket != tt.want || got.Tier != tt.tier {
				t.Errorf("got %+v", got)
			}
		})
	}
}

func TestResolveIsDeterministic(t *testing.T) {
	rules := Rules{Repos: []RepoRule{
		{Repo: "/r", Ticket: "B-1"}, {Repo: "/r", Ticket: "A-1"}, {Repo: "/r/", Ticket: "C-1"},
	}}
	signal := ContextSignal{Repo: "/r/x", Branch: "main"}
	first := Resolve(signal, rules)
	for i := 0; i < 50; i++ {
		if got := Resolve(signal, rules); got != first {
			t.Fatalf("run %d: %+v != %+v", i, got, first)
		}
	}
	reversed := Rules{Repos: []RepoRule{rules.Repos[2], rules.Repos[1], rules.Repos[0]}}
	if got := Resolve(signal, reversed); got != first {
		t.Errorf("order-dependent: %+v != %+v", got, first)
	}
}

func TestResolveIgnoresLearnedRuleThatMatchesEverything(t *testing.T) {
	rules := Rules{Learned: []LearnedRule{{Ticket: "ALL-1"}}}
	got := Resolve(ContextSignal{Repo: "/a", Branch: "dev"}, rules)
	if got.Tier != TierUnknown {
		t.Fatalf("got %+v", got)
	}
}

func TestResolveConfigRuleBeatsLearnedRule(t *testing.T) {
	rules := Rules{
		Repos:   []RepoRule{{Repo: "/a", Ticket: "CFG-1"}},
		Learned: []LearnedRule{{Repo: "/a", Branch: "dev", Ticket: "LRN-1"}},
	}
	got := Resolve(ContextSignal{Repo: "/a", Branch: "dev"}, rules)
	if got.Ticket != "CFG-1" || got.Tier != TierRule {
		t.Fatalf("got %+v", got)
	}
	got = Resolve(ContextSignal{Repo: "/a", Branch: "ABC-5-x"}, Rules{Learned: rules.Learned, TicketPattern: nil})
	if got.Tier != TierExplicit {
		t.Fatalf("explicit branch must beat learned: %+v", got)
	}
}

func TestDecideAgreesWithResolve(t *testing.T) {
	rules := Rules{
		Repos:   []RepoRule{{Repo: "/cfg", Ticket: "CFG-1"}, {Repo: "/cfg/deep", Ticket: "CFG-2"}},
		Learned: []LearnedRule{{ID: 1, Repo: "/l", Branch: "dev", Ticket: "LRN-1"}, {ID: 2, Repo: "/l", Ticket: "LRN-2"}, {ID: 3, Ticket: "ALL-1"}},
	}
	tests := []struct {
		name   string
		signal ContextSignal
		kind   RuleKind
		id     int64
	}{
		{"event ticket", ContextSignal{Repo: "/cfg", Ticket: "evt-9"}, RuleEventTicket, 0},
		{"branch ticket", ContextSignal{Repo: "/cfg", Branch: "ABC-3-x"}, RuleBranchTicket, 0},
		{"config rule longest prefix", ContextSignal{Repo: "/cfg/deep/sub", Branch: "main"}, RuleRepo, 0},
		{"config rule shorter prefix", ContextSignal{Repo: "/cfg/other", Branch: "main"}, RuleRepo, 0},
		{"learned branch specific", ContextSignal{Repo: "/l", Branch: "dev"}, RuleLearned, 1},
		{"learned repo only", ContextSignal{Repo: "/l", Branch: "main"}, RuleLearned, 2},
		{"empty learned rule ignored", ContextSignal{Repo: "/none", Branch: "main"}, RuleNone, 0},
		{"unknown", ContextSignal{}, RuleNone, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := Decide(tc.signal, rules)
			if d.Attribution != Resolve(tc.signal, rules) {
				t.Fatalf("Decide %+v != Resolve %+v", d.Attribution, Resolve(tc.signal, rules))
			}
			if d.Rule != tc.kind || d.LearnedID != tc.id {
				t.Fatalf("rule = %v id %d, want %v id %d", d.Rule, d.LearnedID, tc.kind, tc.id)
			}
		})
	}
}
