// Package resolver derives a ticket attribution from a context signal and
// rules. It is a pure function: no I/O, no clock access.
//
// The resolver only judges one signal. Which signal is in effect is decided
// by the sessionizer, in this precedence: an explicit pin (ticktack switch),
// then a window-title hint while its app is frontmost, then git/shell
// context. Within one signal the order is: ticket on the event, ticket in
// the branch name, repo rule, learned rule, the standard task of the project
// matching the repo's folder, the global standard task, unknown.
package resolver

import (
	"fmt"
	"strings"

	"github.com/JtheGunner/ticktack/internal/ticketid"
)

// Tier is the discrete confidence of an attribution.
type Tier string

// Confidence tiers, from most to least certain.
const (
	TierExplicit  Tier = "explicit"
	TierRule      Tier = "rule"
	TierLearned   Tier = "learned"
	TierSuggested Tier = "suggested"
	TierUnknown   Tier = "unknown"
)

// ContextSignal is the context observed for a moment in time.
type ContextSignal struct {
	Repo   string // absolute repository path
	Branch string
	Ticket string // explicit ticket ID already known, e.g. from an event
}

// Attribution is the resolver's explainable verdict.
type Attribution struct {
	Ticket string
	Tier   Tier
	Reason string
}

// RuleKind names the rule that decided an attribution.
type RuleKind string

// Rule kinds, in resolution order.
const (
	RuleEventTicket  RuleKind = "event-ticket"
	RuleBranchTicket RuleKind = "branch-ticket"
	RuleRepo         RuleKind = "repo-rule"
	RuleLearned      RuleKind = "learned-rule"
	// RuleProjectDefault is the standard task of the project whose name
	// matches the repository's folder; RuleGlobalDefault the global one.
	RuleProjectDefault RuleKind = "project-default"
	RuleGlobalDefault  RuleKind = "global-default"
	RuleNone           RuleKind = "none"
)

// Decision is an Attribution together with the rule that produced it.
type Decision struct {
	Attribution
	Rule RuleKind
	// RuleRepo and RuleBranch are the match criteria of the deciding repo or
	// learned rule; RuleBranch is only set for learned rules.
	RuleRepo   string
	RuleBranch string
	// LearnedID is the store ID of the deciding learned rule (0 when unknown).
	LearnedID int64
}

// Resolve attributes the signal to a ticket. Order: explicit ticket (event or
// branch name), repo rule, learned rule, unknown.
func Resolve(signal ContextSignal, rules Rules) Attribution {
	return Decide(signal, rules).Attribution
}

// Decide is Resolve that also reports which rule decided.
func Decide(signal ContextSignal, rules Rules) Decision {
	if ticket := strings.ToUpper(strings.TrimSpace(signal.Ticket)); ticket != "" {
		return Decision{
			Attribution: Attribution{Ticket: ticket, Tier: TierExplicit, Reason: fmt.Sprintf("explicit ticket %s on event", ticket)},
			Rule:        RuleEventTicket,
		}
	}
	if ticket := ticketid.Extract(signal.Branch, rules.TicketPattern); ticket != "" {
		return Decision{
			Attribution: Attribution{
				Ticket: ticket,
				Tier:   TierExplicit,
				Reason: fmt.Sprintf("ticket %s found in branch %q", ticket, signal.Branch),
			},
			Rule: RuleBranchTicket,
		}
	}
	if rule, ok := matchRepoRule(rules.Repos, signal.Repo); ok {
		return Decision{
			Attribution: Attribution{Ticket: rule.Ticket, Tier: TierRule, Reason: "repo rule for " + signal.Repo},
			Rule:        RuleRepo,
			RuleRepo:    rule.Repo,
		}
	}
	if rule, ok := matchLearned(rules.Learned, signal.Repo, signal.Branch); ok {
		return Decision{
			Attribution: Attribution{Ticket: rule.Ticket, Tier: TierLearned, Reason: "learned rule for " + signal.Repo},
			Rule:        RuleLearned,
			RuleRepo:    rule.Repo,
			RuleBranch:  rule.Branch,
			LearnedID:   rule.ID,
		}
	}
	if decision, ok := decideDefault(signal.Repo, rules); ok {
		return decision
	}
	return Decision{
		Attribution: Attribution{Tier: TierUnknown, Reason: "no ticket in branch and no rule for repo"},
		Rule:        RuleNone,
	}
}
