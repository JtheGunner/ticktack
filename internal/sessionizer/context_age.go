package sessionizer

import (
	"fmt"
	"time"

	"github.com/JtheGunner/ticktack/internal/resolver"
)

// staleReasonPrefix starts the reason of a block whose context outlived the
// age limit; the age follows.
const staleReasonPrefix = "context unconfirmed for "

// staleFrom returns the moment from which the context of the step is stale.
// ok is false when the step never goes stale: it carries no context, or it is
// automatic context and the age limit is off.
func (s contextStep) staleFrom(cfg Config) (time.Time, bool) {
	return staleFrom(cfg, s.confirmedAt, s.manualTicket != "")
}

// staleFrom is the single rule for when context goes stale: automatic context
// ContextMaxAge after its last confirmation, a manual pin at the end of the
// calendar day it was set on. The two are independent: a ContextMaxAge of
// zero switches off the limit for automatic context only.
func staleFrom(cfg Config, confirmedAt time.Time, pinned bool) (time.Time, bool) {
	cfg = cfg.withDefaults()
	if confirmedAt.IsZero() {
		return time.Time{}, false
	}
	if pinned {
		local := confirmedAt.In(cfg.Location)
		return time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, cfg.Location), true
	}
	if cfg.ContextMaxAge <= 0 {
		return time.Time{}, false
	}
	return confirmedAt.Add(cfg.ContextMaxAge), true
}

// StaleFrom returns the moment from which Derive treats the context of d as
// stale under c, and whether there is such a moment at all.
func (c Config) StaleFrom(d ContextDecision) (time.Time, bool) {
	return staleFrom(c, d.ConfirmedAt, d.ManualTicket != "")
}

// StaleAttribution is the attribution of time under context that has been
// unconfirmed for age: the ticket stays as a suggestion and the reason names
// the age. An attribution without a ticket, or one that is already no more
// than a suggestion, is returned unchanged.
func StaleAttribution(a resolver.Attribution, age time.Duration) resolver.Attribution {
	if a.Ticket == "" || a.Tier == resolver.TierSuggested || a.Tier == resolver.TierUnknown {
		return a
	}
	return resolver.Attribution{Ticket: a.Ticket, Tier: resolver.TierSuggested, Reason: StaleReason(age)}
}

// NeutralAppReason is the block reason of time spent in a neutral app: the
// ticket of the context stays, as a suggestion. It deliberately does not start
// with the stale prefix, so such time is never counted as carried.
const NeutralAppReason = "foreground app class neutral"

// NeutralAppAttribution is the attribution of time in a neutral app: the
// ticket stays as a suggestion with NeutralAppReason. An attribution without a
// ticket, or one that is already no more than a suggestion (stale context
// keeps its own reason), is returned unchanged.
func NeutralAppAttribution(a resolver.Attribution) resolver.Attribution {
	if a.Ticket == "" || a.Tier == resolver.TierSuggested || a.Tier == resolver.TierUnknown {
		return a
	}
	return resolver.Attribution{Ticket: a.Ticket, Tier: resolver.TierSuggested, Reason: NeutralAppReason}
}

// StaleReason is the block reason for context unconfirmed for age. The age is
// cut to whole minutes.
func StaleReason(age time.Duration) string {
	return staleReasonPrefix + FormatAge(age)
}

// FormatAge renders a context age or age limit in whole hours and minutes.
func FormatAge(age time.Duration) string {
	minutes := int(age / time.Minute)
	hours, rest := minutes/60, minutes%60
	switch {
	case minutes < 1:
		return "under 1m"
	case hours == 0:
		return fmt.Sprintf("%dm", rest)
	case rest == 0:
		return fmt.Sprintf("%dh", hours)
	default:
		return fmt.Sprintf("%dh%dm", hours, rest)
	}
}
