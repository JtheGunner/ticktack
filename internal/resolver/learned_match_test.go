package resolver

import "testing"

// TestLearnedRuleMatchingIsExactBranchAndRepoPrefix pins the matching
// dialect: the branch is compared for equality (no globs) and the repository
// as a path prefix on a path boundary.
func TestLearnedRuleMatchingIsExactBranchAndRepoPrefix(t *testing.T) {
	tests := []struct {
		name   string
		rule   LearnedRule
		signal ContextSignal
		want   bool
	}{
		{"exact branch", LearnedRule{Repo: "/w/app", Branch: "dev", Ticket: "L-1"}, ContextSignal{Repo: "/w/app", Branch: "dev"}, true},
		{"branch is not a prefix match", LearnedRule{Repo: "/w/app", Branch: "dev", Ticket: "L-1"}, ContextSignal{Repo: "/w/app", Branch: "dev-2"}, false},
		{"branch is case-sensitive", LearnedRule{Repo: "/w/app", Branch: "dev", Ticket: "L-1"}, ContextSignal{Repo: "/w/app", Branch: "Dev"}, false},
		{"star is a literal, not a glob", LearnedRule{Repo: "/w/app", Branch: "feature/*", Ticket: "L-1"}, ContextSignal{Repo: "/w/app", Branch: "feature/login"}, false},
		{"star matches only itself", LearnedRule{Repo: "/w/app", Branch: "feature/*", Ticket: "L-1"}, ContextSignal{Repo: "/w/app", Branch: "feature/*"}, true},
		{"repo prefix covers subdirectory", LearnedRule{Repo: "/w/app", Ticket: "L-1"}, ContextSignal{Repo: "/w/app/sub", Branch: "x"}, true},
		{"repo prefix stops at a path boundary", LearnedRule{Repo: "/w/app", Ticket: "L-1"}, ContextSignal{Repo: "/w/application", Branch: "x"}, false},
		{"repo is not a glob", LearnedRule{Repo: "/w/*", Ticket: "L-1"}, ContextSignal{Repo: "/w/app", Branch: "x"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Resolve(tt.signal, Rules{Learned: []LearnedRule{tt.rule}})
			if matched := got.Tier == TierLearned; matched != tt.want {
				t.Errorf("matched = %v, want %v (%+v)", matched, tt.want, got)
			}
		})
	}
}
