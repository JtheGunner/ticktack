package cli

import (
	"fmt"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/resolver"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
)

// User-facing strings of `explain` live here so a German variant can be added
// later. Callers pass already sanitised arguments.
const (
	msgExplainNoBlocks  = "no block to explain: no time recorded for %s"
	msgExplainBadTarget = "%q is neither a block number nor a time as HH:MM"
	msgExplainNoSuchNum = "no block number %d on %s (valid: 1-%d); see `ticktack today`"
	msgExplainNoBlockAt = "no block contains %s on %s; see `ticktack today`"
	msgExplainHeader    = "Block %d on %s: %s-%s (%s)"
	msgLabelSignal      = "Signal"
	msgLabelRule        = "Rule"
	msgLabelNotApplied  = "Not applied"
	msgLabelPresence    = "Presence"
	msgExplainPresence  = "%d sample(s) between %s and %s"
	msgExplainNoSamples = "no present sample inside the block"
	msgExplainBefore    = "previous present sample: %s (%s before the block)"
	msgExplainAfter     = "next present sample: %s (%s after the block)"
	msgExplainGap       = "idle gap: %s-%s"

	msgExplainMergedSignal = "none single: the block was merged from several signals with different rules"
	msgExplainMergedItem   = "- %s -> %s"
	msgExplainNoContext    = "none: presence without any earlier context event"
	msgExplainTooShort     = "none: the block is shorter than the minimum duration and has no same-ticket neighbour, nor blocks of one ticket on both sides"
	msgLabelFolded         = "Folded"
	msgExplainNoRule       = "no rule matched (no ticket on the event or in the branch, no configured or learned rule)"
)

func msgExplainEventSignal(source, kind, at, repo, branch, ticket string) string {
	return fmt.Sprintf("%s event from %s at %s (repo %s, branch %s, ticket %s)", kind, source, at, repo, branch, ticket)
}

// msgExplainStale names the age of the context as the reason a block is only
// a suggestion. confirmedAt is when the context was last confirmed.
func msgExplainStale(reason, confirmedAt, maxAge string, pinned bool) string {
	if pinned {
		return fmt.Sprintf("%s: the manual switch was set at %s and is exempt from context_max_age only on that day; confirm the ticket in review", reason, confirmedAt)
	}
	return fmt.Sprintf("%s: last confirmed at %s, limit %s (context_max_age); confirm the ticket in review", reason, confirmedAt, maxAge)
}

// msgExplainAppClass names the app class as the reason a block is only a
// suggestion. app is the bundle ID and can be empty.
func msgExplainAppClass(class config.AppClass, app string) string {
	if app == "" {
		return fmt.Sprintf("%s: the foreground app is in class %q (app_classes); the ticket stays, confirm it in review", sessionizer.NeutralAppReason, class)
	}
	return fmt.Sprintf("%s: the foreground app %s is in class %q (app_classes); the ticket stays, confirm it in review", sessionizer.NeutralAppReason, app, class)
}

// msgExplainFolded names the rule that put a short block of another ticket
// into this block. ticket is the display label of the folded block's ticket.
func msgExplainFolded(start, end, ticket string) string {
	return fmt.Sprintf("%s-%s (%s) was shorter than the minimum duration and lies between blocks of this ticket; folded in (rule: confirmed short block between two blocks of one ticket; the tier is the lower of the two)", start, end, ticket)
}

func msgExplainOverrideSignal(start, end string) string {
	return fmt.Sprintf("manual override %s-%s", start, end)
}

func msgExplainOverrideRule(ticket string) string {
	return "override assigns " + ticket + " (corrections are stored as overrides, never as events)"
}

func msgExplainFillSignal(start, end string) string {
	return fmt.Sprintf("manual fill %s-%s (no presence recorded)", start, end)
}

func msgExplainFillRule(ticket string) string {
	return "you filled idle time with " + ticket + " in review; the time exists only because of that action (fills are stored as overrides, never as events)"
}

func msgExplainRuleText(d resolver.Decision, ticket string) string {
	switch d.Rule {
	case resolver.RuleEventTicket:
		return "explicit ticket " + ticket + " carried by the event"
	case resolver.RuleBranchTicket:
		return "ticket " + ticket + " found in the branch name"
	case resolver.RuleRepo:
		return fmt.Sprintf("configured rule: repo prefix %s -> %s (config file)", d.RuleRepo, ticket)
	case resolver.RuleLearned:
		return fmt.Sprintf("learned rule #%d: repo %s, branch %s -> %s (`ticktack map`)",
			d.LearnedID, anyIfEmpty(d.RuleRepo), anyIfEmpty(d.RuleBranch), ticket)
	case resolver.RuleProjectDefault:
		return fmt.Sprintf("standard task of the matching project %s (%s)", ticket, d.Reason)
	case resolver.RuleGlobalDefault:
		return fmt.Sprintf("global standard task %s (%s)", ticket, d.Reason)
	case sessionizer.ManualRule:
		return "manual switch to " + ticket + " (`ticktack switch`) pins the attribution"
	default:
		return msgExplainNoRule
	}
}

func msgExplainSkipped(kind resolver.RuleKind, repo, branch string) string {
	switch kind {
	case resolver.RuleEventTicket:
		return "no explicit ticket on the event"
	case resolver.RuleBranchTicket:
		return fmt.Sprintf("no ticket ID in branch %s", branch)
	case resolver.RuleRepo:
		return fmt.Sprintf("no configured rule matches repo %s", repo)
	case resolver.RuleLearned:
		return fmt.Sprintf("no learned rule matches repo %s on branch %s", repo, branch)
	default:
		return string(kind)
	}
}

func anyIfEmpty(s string) string {
	if s == "" {
		return "(any)"
	}
	return s
}
