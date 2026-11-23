package resolver

import (
	"strings"
	"testing"
)

func folderOfRoot(root string) func(string) (string, bool) {
	return func(repo string) (string, bool) {
		rest, ok := strings.CutPrefix(repo, root+"/")
		if !ok {
			return "", false
		}
		folder, _, _ := strings.Cut(rest, "/")
		return folder, true
	}
}

func TestDecideDefaultSteps(t *testing.T) {
	projects := []ProjectInfo{
		{Key: "TICKT", Name: "ticktack"},
		{Key: "HL", Name: "Home-Lab"},
		{Key: "HL2", Name: "home_lab"},
		{Key: "GEN", Name: "General"},
	}
	base := Rules{
		FolderOf:      folderOfRoot("/work"),
		Projects:      projects,
		GlobalProject: "GEN",
	}
	withTasks := base
	withTasks.DefaultTasks = map[string]string{"TICKT": "TICKT-7"}
	noGlobal := base
	noGlobal.GlobalProject = ""

	tests := []struct {
		name       string
		rules      Rules
		signal     ContextSignal
		wantTicket string
		wantTier   Tier
		wantRule   RuleKind
		wantReason string
	}{
		{"unique match gives placeholder", base, ContextSignal{Repo: "/work/ticktack"}, "TICKT:default", TierRule, RuleProjectDefault, "ticktack"},
		{"unique match with known task gives the real key", withTasks, ContextSignal{Repo: "/work/ticktack"}, "TICKT-7", TierRule, RuleProjectDefault, "ticktack"},
		{"worktree below the repo resolves to the repo folder", base, ContextSignal{Repo: "/work/ticktack/.worktrees/x"}, "TICKT:default", TierRule, RuleProjectDefault, ""},
		{"no match falls back to global", base, ContextSignal{Repo: "/work/unknown-repo"}, "GEN:default", TierRule, RuleGlobalDefault, `no project named "unknown-repo"`},
		{"ambiguous names fall back to global", base, ContextSignal{Repo: "/work/homelab"}, "GEN:default", TierRule, RuleGlobalDefault, "ambiguous: HL, HL2"},
		{"repo outside roots falls back to global", base, ContextSignal{Repo: "/elsewhere/ticktack"}, "GEN:default", TierRule, RuleGlobalDefault, "outside repo_roots"},
		{"no match and no global is unknown", noGlobal, ContextSignal{Repo: "/work/unknown-repo"}, "", TierUnknown, RuleNone, ""},
		{"matched project wins without a global", noGlobal, ContextSignal{Repo: "/work/ticktack"}, "TICKT:default", TierRule, RuleProjectDefault, ""},
		{"ticket in branch still wins", base, ContextSignal{Repo: "/work/ticktack", Branch: "feat/ABC-1-x"}, "ABC-1", TierExplicit, RuleBranchTicket, ""},
		{"ticket on event still wins", base, ContextSignal{Repo: "/work/ticktack", Ticket: "xyz-9"}, "XYZ-9", TierExplicit, RuleEventTicket, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := Decide(tt.signal, tt.rules)
			if d.Ticket != tt.wantTicket || d.Tier != tt.wantTier || d.Rule != tt.wantRule {
				t.Fatalf("Decide = {%q %s %s}, want {%q %s %s}", d.Ticket, d.Tier, d.Rule, tt.wantTicket, tt.wantTier, tt.wantRule)
			}
			if !strings.Contains(d.Reason, tt.wantReason) {
				t.Errorf("Reason = %q, want it to contain %q", d.Reason, tt.wantReason)
			}
		})
	}
}

func TestDecideDefaultStepsYieldToRepoAndLearnedRules(t *testing.T) {
	rules := Rules{
		FolderOf:      folderOfRoot("/work"),
		Projects:      []ProjectInfo{{Key: "TICKT", Name: "ticktack"}},
		GlobalProject: "GEN",
		Repos:         []RepoRule{{Repo: "/work/ticktack", Ticket: "RULE-1"}},
	}
	if d := Decide(ContextSignal{Repo: "/work/ticktack"}, rules); d.Ticket != "RULE-1" || d.Rule != RuleRepo {
		t.Errorf("repo rule lost to a default step: %+v", d)
	}
	rules.Repos = nil
	rules.Learned = []LearnedRule{{Repo: "/work/ticktack", Ticket: "LRN-2"}}
	if d := Decide(ContextSignal{Repo: "/work/ticktack"}, rules); d.Ticket != "LRN-2" || d.Rule != RuleLearned {
		t.Errorf("learned rule lost to a default step: %+v", d)
	}
	// A repo that matches no project would get the global standard task.
	rules.Learned = []LearnedRule{{Repo: "/work/other", Ticket: "LRN-3"}}
	if d := Decide(ContextSignal{Repo: "/work/other"}, rules); d.Ticket != "LRN-3" || d.Rule != RuleLearned {
		t.Errorf("learned rule lost to the global default step: %+v", d)
	}
	if d := Decide(ContextSignal{Repo: "/work/third"}, rules); d.Rule != RuleGlobalDefault {
		t.Errorf("Decide = %+v, want the global standard task without a learned rule", d)
	}
}

func TestDecideWithoutDefaultsKeepsUnknown(t *testing.T) {
	d := Decide(ContextSignal{Repo: "/work/ticktack"}, Rules{})
	if d.Tier != TierUnknown || d.Rule != RuleNone || d.Ticket != "" {
		t.Errorf("Decide with zero Rules = %+v, want unknown", d)
	}
}

func TestNormalizeName(t *testing.T) {
	for in, want := range map[string]string{
		"Home-Lab_AI Chat": "homelabaichat",
		"ticktack":         "ticktack",
		"  A - b _ C ":     "abc",
		"":                 "",
	} {
		if got := NormalizeName(in); got != want {
			t.Errorf("NormalizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseDefaultRef(t *testing.T) {
	if got := DefaultRef("TICKT"); got != "TICKT:default" {
		t.Errorf("DefaultRef = %q", got)
	}
	if p, ok := ParseDefaultRef("TICKT:default"); !ok || p != "TICKT" {
		t.Errorf("ParseDefaultRef(TICKT:default) = (%q, %v)", p, ok)
	}
	for _, in := range []string{"TICKT-1", ":default", "", "TICKT:other"} {
		if _, ok := ParseDefaultRef(in); ok {
			t.Errorf("ParseDefaultRef(%q) accepted", in)
		}
	}
}
