package sessionizer

import (
	"sort"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/resolver"
)

// ContextDecision is the context in effect at a moment together with the
// event that set it and the rule that attributed it.
type ContextDecision struct {
	// At is the time the context took effect.
	At time.Time
	// Event is the context event that set the context in effect.
	Event event.Event
	// Signal is the repo, branch and ticket fed to the resolver.
	Signal resolver.ContextSignal
	// ManualTicket is set while a manual switch pins the attribution.
	ManualTicket string
	// Decision is the resolver's verdict; for a manual switch it is explicit
	// with the reason "manual switch to <ID>" and rule kind ManualRule.
	Decision resolver.Decision
}

// ManualRule is the rule kind reported for a manual switch.
const ManualRule resolver.RuleKind = "manual-switch"

// ContextSteps returns every context step of the timeline Derive uses, in
// time order, each with its deciding event and rule. It is pure.
func ContextSteps(events []event.Event, rules resolver.Rules) []ContextDecision {
	steps, _ := contextTimeline(sortedEvents(events))
	out := make([]ContextDecision, 0, len(steps))
	for _, step := range steps {
		cd := ContextDecision{At: step.at, Event: step.event, Signal: step.signal, ManualTicket: step.manualTicket}
		if step.manualTicket != "" {
			cd.Decision = resolver.Decision{Attribution: step.attribution(rules), Rule: ManualRule}
		} else {
			cd.Decision = resolver.Decide(step.signal, rules)
		}
		out = append(out, cd)
	}
	return out
}

// ContextAt returns the context in effect at time at, using exactly the same
// timeline as Derive. ok is false when no context event lies at or before at.
// It is pure: the inputs are not modified.
func ContextAt(events []event.Event, rules resolver.Rules, at time.Time) (ContextDecision, bool) {
	steps := ContextSteps(events, rules)
	n := sort.Search(len(steps), func(i int) bool { return steps[i].At.After(at) })
	if n == 0 {
		return ContextDecision{}, false
	}
	return steps[n-1], true
}

// OverrideReason and MinBlockReason are the block reasons of override blocks
// and of blocks demoted for being too short; NoContextReason marks presence
// before any context event.
const (
	OverrideReason  = overrideReason
	MinBlockReason  = minBlockReason
	NoContextReason = noContextReason
)

// Normalized returns the config with every unset field replaced by its
// default, exactly as Derive does.
func (c Config) Normalized() Config { return c.withDefaults() }
