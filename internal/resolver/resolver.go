// Package resolver derives a ticket attribution from a context signal and
// rules. It is a pure function: no I/O, no clock access.
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

// Resolve attributes the signal to a ticket. Order: explicit ticket (event or
// branch name), repo rule, learned rule, unknown.
func Resolve(signal ContextSignal, rules Rules) Attribution {
	if ticket := strings.ToUpper(strings.TrimSpace(signal.Ticket)); ticket != "" {
		return Attribution{Ticket: ticket, Tier: TierExplicit, Reason: fmt.Sprintf("explicit ticket %s on event", ticket)}
	}
	if ticket := ticketid.Extract(signal.Branch, rules.TicketPattern); ticket != "" {
		return Attribution{
			Ticket: ticket,
			Tier:   TierExplicit,
			Reason: fmt.Sprintf("ticket %s found in branch %q", ticket, signal.Branch),
		}
	}
	if rule, ok := matchRepoRule(rules.Repos, signal.Repo); ok {
		return Attribution{Ticket: rule.Ticket, Tier: TierRule, Reason: "repo rule for " + signal.Repo}
	}
	if rule, ok := matchLearned(rules.Learned, signal.Repo, signal.Branch); ok {
		return Attribution{Ticket: rule.Ticket, Tier: TierLearned, Reason: "learned rule for " + signal.Repo}
	}
	return Attribution{Tier: TierUnknown, Reason: "no ticket in branch and no rule for repo"}
}
