// Package explain names the signal and the rule behind a derived block. It is
// a pure function over the same inputs sessionizer.Derive uses: no I/O, no
// clock reads. It reports only data that events already contain.
package explain

import (
	"sort"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/resolver"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
)

// Source says what decided a block.
type Source string

// Block sources.
const (
	// SourceContext: a context event, attributed by a resolver rule or a
	// manual switch.
	SourceContext Source = "context"
	// SourceOverride: a user override covers the block.
	SourceOverride Source = "override"
	// SourceNoContext: presence without any earlier context event.
	SourceNoContext Source = "no-context"
	// SourceMerged: the block was merged from several context signals with
	// different rules (Merged lists them).
	SourceMerged Source = "merged"
	// SourceTooShort: the block was shorter than the minimum duration and
	// could not be merged into a same-ticket neighbour.
	SourceTooShort Source = "too-short"
)

// Input is everything needed to explain one block.
type Input struct {
	Block     sessionizer.Block
	Events    []event.Event
	Overrides []event.Override
	Rules     resolver.Rules
	Config    sessionizer.Config
}

// Gap is a hole in the presence signal reported by a gap marker.
type Gap struct{ Start, End time.Time }

// Presence is the presence evidence that bounds a block.
type Presence struct {
	// Samples is the number of present samples inside the block.
	Samples int
	// First and Last are the first and last moment of activity (sample time
	// minus its idle time) inside the block, zero without samples.
	First, Last time.Time
	// Previous and Next are the nearest moments of activity outside the block
	// (zero when none exists); a distance above the gap threshold is what ends
	// a presence span.
	Previous, Next time.Time
	// Gaps are the gap markers touching the block, the nearest margin of one
	// gap threshold included.
	Gaps []Gap
}

// Explanation is the traceable reason for a block.
type Explanation struct {
	Block  sessionizer.Block
	Source Source
	// Event is the deciding context event (SourceContext only).
	Event event.Event
	// Decision is the resolver verdict (SourceContext only); for a manual
	// switch its Rule is sessionizer.ManualRule.
	Decision resolver.Decision
	// Merged lists the distinct deciding signals of a SourceMerged block.
	Merged []sessionizer.ContextDecision
	// Skipped lists the higher-priority rules that did not apply, in
	// resolution order (SourceContext only).
	Skipped []resolver.RuleKind
	// Override is the deciding override (SourceOverride only).
	Override event.Override
	// Signal is the repo, branch and ticket the resolver saw.
	Signal resolver.ContextSignal
	// Stale reports that the deciding context was unconfirmed for too long
	// (SourceContext only): the block keeps the ticket of Decision, but only
	// as a suggestion.
	Stale bool
	// Age is how long the context was unconfirmed when its stale time in the
	// block began (the block start, unless a short confirmed block was merged
	// in front of it) and ConfirmedAt when it was last confirmed (both set
	// when Stale).
	Age         time.Duration
	ConfirmedAt time.Time
	// Pinned reports that the stale context is a manual pin from an earlier
	// day; otherwise it outlived MaxAge.
	Pinned   bool
	MaxAge   time.Duration
	Presence Presence
}

var resolutionOrder = []resolver.RuleKind{
	resolver.RuleEventTicket, resolver.RuleBranchTicket, resolver.RuleRepo, resolver.RuleLearned,
}

// Block explains in.Block, which must come from sessionizer.Derive over the
// same events, overrides, config and rules.
func Block(in Input) Explanation {
	ex := Explanation{Block: in.Block, Presence: presenceFor(in)}
	switch in.Block.Reason {
	case sessionizer.OverrideReason:
		ex.Source = SourceOverride
		ex.Override = overrideFor(in)
		return ex
	case sessionizer.MinBlockReason:
		ex.Source = SourceTooShort
		return ex
	}
	steps := stepsOf(in)
	if len(steps) == 0 || in.Block.Reason == sessionizer.NoContextReason {
		ex.Source = SourceNoContext
		return ex
	}
	cd, ok := matchingStep(steps, in.Block)
	if !ok {
		var age time.Duration
		cd, age, ok = staleStep(steps, in)
		if ok {
			ex.Stale = true
			ex.Age = age
			ex.ConfirmedAt = cd.ConfirmedAt
			ex.Pinned = cd.ManualTicket != ""
			ex.MaxAge = in.Config.Normalized().ContextMaxAge
		}
	}
	if !ok {
		ex.Source = SourceMerged
		ex.Merged = distinctSteps(steps)
		return ex
	}
	ex.Source = SourceContext
	ex.Event = cd.Event
	ex.Signal = cd.Signal
	ex.Decision = cd.Decision
	if cd.Decision.Rule == sessionizer.ManualRule {
		return ex
	}
	for _, kind := range resolutionOrder {
		if kind == cd.Decision.Rule {
			break
		}
		ex.Skipped = append(ex.Skipped, kind)
	}
	return ex
}

// stepsOf returns the context steps that can have contributed to the block:
// the one in effect at its start and those beginning inside it.
func stepsOf(in Input) []sessionizer.ContextDecision {
	all := sessionizer.ContextSteps(in.Events, in.Rules)
	var out []sessionizer.ContextDecision
	for i, s := range all {
		if s.At.After(in.Block.Start) {
			if s.At.Before(in.Block.End) {
				out = append(out, s)
			}
			continue
		}
		if i+1 == len(all) || all[i+1].At.After(in.Block.Start) {
			out = append(out, s)
		}
	}
	return out
}

// matchingStep returns the first step whose attribution equals the block's
// own ticket, tier and reason; a merged short block keeps the neighbour's
// reason, so the step at its start does not always match.
func matchingStep(steps []sessionizer.ContextDecision, b sessionizer.Block) (sessionizer.ContextDecision, bool) {
	for _, s := range steps {
		a := s.Decision.Attribution
		if a.Ticket == b.Ticket && a.Tier == b.Tier && a.Reason == b.Reason {
			return s, true
		}
	}
	return sessionizer.ContextDecision{}, false
}

// staleStep returns the first step whose context was stale during the block
// and whose stale attribution (see sessionizer.StaleAttribution) equals the
// block's own, together with the age of that context when its stale time in
// the block began. That moment is not always the block start: a short
// confirmed block that merged into a stale neighbour takes the neighbour's
// tier and reason, so the block can begin before its context went stale.
func staleStep(steps []sessionizer.ContextDecision, in Input) (sessionizer.ContextDecision, time.Duration, bool) {
	b := in.Block
	for i, s := range steps {
		staleFrom, ok := in.Config.StaleFrom(s)
		if !ok {
			continue
		}
		// The step is in effect inside the block from from to until.
		from, until := laterOf(b.Start, s.At), b.End
		if i+1 < len(steps) {
			until = steps[i+1].At
		}
		staleStart := laterOf(from, staleFrom)
		if !staleStart.Before(until) {
			continue
		}
		age := staleStart.Sub(s.ConfirmedAt)
		a := sessionizer.StaleAttribution(s.Decision.Attribution, age)
		if a != s.Decision.Attribution && a.Ticket == b.Ticket && a.Tier == b.Tier && a.Reason == b.Reason {
			return s, age, true
		}
	}
	return sessionizer.ContextDecision{}, 0, false
}

func laterOf(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// distinctSteps drops steps that repeat an earlier attribution.
func distinctSteps(steps []sessionizer.ContextDecision) []sessionizer.ContextDecision {
	var out []sessionizer.ContextDecision
	seen := map[resolver.Attribution]bool{}
	for _, s := range steps {
		if !seen[s.Decision.Attribution] {
			seen[s.Decision.Attribution] = true
			out = append(out, s)
		}
	}
	return out
}

// overrideFor returns the last non-discarding override covering the block
// start (later overrides win, as in Derive).
func overrideFor(in Input) event.Override {
	var found event.Override
	for _, o := range in.Overrides {
		if o.Discard || !o.End.After(o.Start) {
			continue
		}
		if !in.Block.Start.Before(o.Start) && in.Block.Start.Before(o.End) {
			found = o
		}
	}
	return found
}

func presenceFor(in Input) Presence {
	cfg := in.Config.Normalized()
	var p Presence
	b := in.Block
	for _, e := range in.Events {
		switch e.Kind {
		case event.KindGap:
			gap := Gap{Start: e.At.Add(-time.Duration(e.IdleSeconds) * time.Second), End: e.At}
			if gap.End.After(b.Start.Add(-cfg.GapThreshold)) && gap.Start.Before(b.End.Add(cfg.GapThreshold)) {
				p.Gaps = append(p.Gaps, gap)
			}
		case event.KindPresence:
			idle := time.Duration(e.IdleSeconds) * time.Second
			if idle >= cfg.IdleThreshold {
				continue
			}
			// A sample is evidence of activity at the last input, the same
			// moment Derive uses; judging it by its own timestamp would place
			// the sample that ends a block outside of it.
			active := e.At.Add(-idle)
			switch {
			case active.Before(b.Start):
				if active.After(p.Previous) {
					p.Previous = active
				}
			case active.After(b.End):
				if p.Next.IsZero() || active.Before(p.Next) {
					p.Next = active
				}
			default:
				p.Samples++
				if p.First.IsZero() || active.Before(p.First) {
					p.First = active
				}
				if active.After(p.Last) {
					p.Last = active
				}
			}
		}
	}
	sort.Slice(p.Gaps, func(i, j int) bool { return p.Gaps[i].Start.Before(p.Gaps[j].Start) })
	return p
}
