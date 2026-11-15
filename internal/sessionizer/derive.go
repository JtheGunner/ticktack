// Package sessionizer derives time blocks from presence and context events
// and aggregates them into per-ticket, per-day items. Every function in this
// package is pure: no I/O, no clock reads, no globals; the time zone is
// passed in and the output order is deterministic.
//
// Semantics of Derive:
//
//   - Time to book is presence intersected with context. Context events alone
//     never create time; without presence there is no block at all.
//   - A presence sample counts as present only if its idle time is below
//     Config.IdleThreshold. Consecutive present samples at most
//     Config.GapThreshold apart form one presence span (short gaps are
//     bridged). A longer gap, or a gap marker event (event.KindGap) between
//     them, ends the span: gap markers are never bridged and time inside a
//     gap is never counted. A span runs from its first to its last present
//     sample plus at most Config.SampleInterval, so the trailing edge is not
//     over-counted.
//   - The current context is the latest context event at or before a moment
//     (carried forward until it changes). The resolver turns it into ticket,
//     tier and reason. Presence before the first context event yields an
//     unknown block with an empty ticket, so it can be assigned in review.
//   - A context event from ManualSource with a ticket (`ticktack switch`)
//     pins the attribution: later non-manual context events no longer change
//     it until the next manual event. A manual event with an empty ticket
//     clears the pin and restores the latest automatic context. Pinned time
//     is attributed with tier explicit and reason "manual switch to <ID>".
//   - Blocks are split whenever the attributed ticket or the context changes;
//     touching blocks with the same ticket, tier, reason and repo are merged.
//   - A block shorter than Config.MinBlock is merged into the longer touching
//     neighbour with the same ticket; if there is none it is kept as unknown.
//   - Events are sorted by time (then ID) first, so backwards clock jumps
//     never produce a block with End <= Start.
//   - Overrides are applied last: the override interval is cut out of the
//     blocks; a discard override drops it, any other covers the intersection
//     with the presence time with an explicit block. Overrides are inputs, so
//     re-deriving with changed rules keeps them.
package sessionizer

import (
	"sort"
	"strings"
	"time"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/resolver"
)

// Config holds the derivation thresholds. A zero field falls back to the
// value from DefaultConfig.
type Config struct {
	// GapThreshold is the longest gap between present samples that is bridged.
	GapThreshold time.Duration
	// IdleThreshold is the idle time from which a sample no longer counts as
	// present. Zero means GapThreshold.
	IdleThreshold time.Duration
	// SampleInterval is the presence sampling interval; it bounds how far a
	// span extends past its last present sample.
	SampleInterval time.Duration
	// MinBlock is the minimum length of a block before it is merged or
	// demoted to unknown.
	MinBlock time.Duration
}

// DefaultConfig returns the default derivation thresholds.
func DefaultConfig() Config {
	return Config{
		GapThreshold:   config.DefaultGapThreshold,
		IdleThreshold:  config.DefaultGapThreshold,
		SampleInterval: 30 * time.Second,
		MinBlock:       config.DefaultMinBlockDuration,
	}
}

// ConfigFrom builds a Config from the user configuration. Idle threshold and
// sample interval are not user-configurable and use their defaults.
func ConfigFrom(c config.Config) Config {
	cfg := DefaultConfig()
	if c.GapThreshold > 0 {
		cfg.GapThreshold = c.GapThreshold
		cfg.IdleThreshold = c.GapThreshold
	}
	if c.MinBlockDuration > 0 {
		cfg.MinBlock = c.MinBlockDuration
	}
	return cfg
}

func (c Config) withDefaults() Config {
	d := DefaultConfig()
	if c.GapThreshold <= 0 {
		c.GapThreshold = d.GapThreshold
	}
	if c.IdleThreshold <= 0 {
		c.IdleThreshold = c.GapThreshold
	}
	if c.SampleInterval <= 0 {
		c.SampleInterval = d.SampleInterval
	}
	if c.MinBlock < 0 {
		c.MinBlock = 0
	}
	return c
}

// Block is a contiguous stretch of booked time attributed to one ticket.
type Block struct {
	Start, End time.Time
	// Ticket is empty when the block is unknown and awaits assignment.
	Ticket string
	Tier   resolver.Tier
	Reason string
	// Repo is the repository of the context the block was derived from; it
	// is empty for override blocks and when no context existed.
	Repo string
}

// Duration returns the block length.
func (b Block) Duration() time.Duration { return b.End.Sub(b.Start) }

const noContextReason = "no context signal"

// Derive builds the time blocks for the given events, overrides and rules.
// The result is sorted by start time and never contains a block with
// End <= Start. The inputs are not modified.
func Derive(events []event.Event, ov []event.Override, cfg Config, rules resolver.Rules) []Block {
	cfg = cfg.withDefaults()
	sorted := sortedEvents(events)
	spans := presenceSpans(sorted, cfg)
	if len(spans) == 0 {
		return nil
	}
	steps, _ := contextTimeline(sorted)
	blocks := attribute(spans, steps, rules)
	blocks = mergeTouching(blocks)
	blocks = resolveShortBlocks(blocks, cfg.MinBlock)
	blocks = mergeTouching(blocks)
	return applyOverrides(blocks, ov)
}

func sortedEvents(events []event.Event) []event.Event {
	out := make([]event.Event, len(events))
	copy(out, events)
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.Before(out[j].At)
		}
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		// A gap marker is stamped with the resume time, so at equal time it
		// precedes the first sample after the gap.
		return out[i].Kind == event.KindGap && out[j].Kind != event.KindGap
	})
	return out
}

type interval struct{ start, end time.Time }

// presenceSpans folds the sorted events into bridged presence spans.
func presenceSpans(sorted []event.Event, cfg Config) []interval {
	var spans []interval
	var cur *interval
	var last time.Time

	// closeSpan ends the open span at last+SampleInterval, limited by limit
	// when limit lies after the last sample.
	closeSpan := func(limit time.Time) {
		if cur == nil {
			return
		}
		end := last.Add(cfg.SampleInterval)
		if limit.After(last) && limit.Before(end) {
			end = limit
		}
		if end.After(cur.start) {
			spans = append(spans, interval{cur.start, end})
		}
		cur = nil
	}

	for _, e := range sorted {
		switch e.Kind {
		case event.KindGap:
			closeSpan(e.At.Add(-time.Duration(e.IdleSeconds) * time.Second))
		case event.KindPresence:
			if time.Duration(e.IdleSeconds)*time.Second >= cfg.IdleThreshold {
				continue
			}
			if cur != nil && e.At.Sub(last) > cfg.GapThreshold {
				closeSpan(e.At)
			}
			if cur == nil {
				cur = &interval{start: e.At}
			}
			last = e.At
		}
	}
	closeSpan(time.Time{})
	return spans
}

// ManualSource is the event source of context events written by
// `ticktack switch`.
const ManualSource = "manual"

type contextStep struct {
	at     time.Time
	signal resolver.ContextSignal
	// manualTicket is set while a manual switch is in effect.
	manualTicket string
}

// attribution resolves the step. A manual switch is explicit by definition.
func (s contextStep) attribution(rules resolver.Rules) resolver.Attribution {
	if s.manualTicket != "" {
		return resolver.Attribution{
			Ticket: s.manualTicket,
			Tier:   resolver.TierExplicit,
			Reason: "manual switch to " + s.manualTicket,
		}
	}
	return resolver.Resolve(s.signal, rules)
}

// contextTimeline folds the sorted context events into steps. After a manual
// event with a ticket, non-manual context events produce no steps until the
// next manual event: a new switch, or a clear event with an empty ticket,
// which restores the latest automatic signal. It also returns the latest
// non-manual signal.
func contextTimeline(sorted []event.Event) ([]contextStep, resolver.ContextSignal) {
	var steps []contextStep
	var latestAuto resolver.ContextSignal
	manualTicket := ""
	for _, e := range sorted {
		if e.Kind != event.KindContext {
			continue
		}
		if e.Source == ManualSource {
			manualTicket = strings.ToUpper(strings.TrimSpace(e.Ticket))
			steps = append(steps, contextStep{at: e.At, signal: latestAuto, manualTicket: manualTicket})
			continue
		}
		latestAuto = resolver.ContextSignal{Repo: e.Repo, Branch: e.Branch, Ticket: e.Ticket}
		if manualTicket == "" {
			steps = append(steps, contextStep{at: e.At, signal: latestAuto})
		}
	}
	return steps, latestAuto
}

// CurrentContext is the context in effect after a list of events.
type CurrentContext struct {
	// At is the time of the event that set the current attribution.
	At time.Time
	// Signal is the latest automatically detected repo, branch and ticket. It
	// stays up to date while a manual switch overrides the attribution.
	Signal resolver.ContextSignal
	// Attribution is the effective verdict, honouring manual switches.
	Attribution resolver.Attribution
	// Manual reports whether a manual switch determines the attribution.
	Manual bool
}

// LatestContext returns the context in effect after all events, applying the
// same manual-switch rule as Derive. ok is false without any context event.
func LatestContext(events []event.Event, rules resolver.Rules) (CurrentContext, bool) {
	steps, latestAuto := contextTimeline(sortedEvents(events))
	if len(steps) == 0 {
		return CurrentContext{}, false
	}
	last := steps[len(steps)-1]
	return CurrentContext{
		At:          last.at,
		Signal:      latestAuto,
		Attribution: last.attribution(rules),
		Manual:      last.manualTicket != "",
	}, true
}

// attribute intersects every span with the context timeline.
func attribute(spans []interval, steps []contextStep, rules resolver.Rules) []Block {
	var blocks []Block
	for _, span := range spans {
		// idx is the number of steps at or before the span start.
		idx := sort.Search(len(steps), func(i int) bool { return steps[i].at.After(span.start) })
		from := span.start
		for {
			to := span.end
			if idx < len(steps) && steps[idx].at.Before(to) {
				to = steps[idx].at
			}
			if to.After(from) {
				blocks = append(blocks, blockFor(from, to, idx, steps, rules))
			}
			if !to.Before(span.end) {
				break
			}
			from = to
			idx++
		}
	}
	return blocks
}

// blockFor builds the block for [from, to) under the context in effect after
// the first n steps.
func blockFor(from, to time.Time, n int, steps []contextStep, rules resolver.Rules) Block {
	if n == 0 {
		return Block{Start: from, End: to, Tier: resolver.TierUnknown, Reason: noContextReason}
	}
	step := steps[n-1]
	a := step.attribution(rules)
	return Block{Start: from, End: to, Ticket: a.Ticket, Tier: a.Tier, Reason: a.Reason, Repo: step.signal.Repo}
}

func sameAttribution(a, b Block) bool {
	return a.Ticket == b.Ticket && a.Tier == b.Tier && a.Reason == b.Reason && a.Repo == b.Repo
}

// mergeTouching joins neighbours that touch and carry the same attribution.
func mergeTouching(blocks []Block) []Block {
	var out []Block
	for _, b := range blocks {
		if n := len(out); n > 0 && out[n-1].End.Equal(b.Start) && sameAttribution(out[n-1], b) {
			out[n-1].End = b.End
			continue
		}
		out = append(out, b)
	}
	return out
}

// resolveShortBlocks merges blocks shorter than minBlock into the longer
// touching same-ticket neighbour and demotes the rest to unknown.
func resolveShortBlocks(blocks []Block, minBlock time.Duration) []Block {
	if minBlock <= 0 {
		return blocks
	}
	for {
		merged := false
		for i := range blocks {
			if blocks[i].Duration() >= minBlock {
				continue
			}
			target := mergeTarget(blocks, i)
			if target < 0 {
				continue
			}
			lo, hi := i, target
			if lo > hi {
				lo, hi = hi, lo
			}
			combined := blocks[target]
			combined.Start, combined.End = blocks[lo].Start, blocks[hi].End
			blocks = append(blocks[:lo], append([]Block{combined}, blocks[hi+1:]...)...)
			merged = true
			break
		}
		if !merged {
			break
		}
	}
	for i := range blocks {
		if blocks[i].Duration() < minBlock && !isDemoted(blocks[i]) {
			blocks[i].Ticket = ""
			blocks[i].Tier = resolver.TierUnknown
			blocks[i].Reason = "block shorter than minimum duration"
		}
	}
	return blocks
}

func isDemoted(b Block) bool {
	return b.Tier == resolver.TierUnknown && b.Ticket == ""
}

// mergeTarget returns the index of the longer touching neighbour of block i
// with the same ticket, or -1. A tie prefers the previous block.
func mergeTarget(blocks []Block, i int) int {
	best := -1
	candidate := func(j int) {
		if j < 0 || j >= len(blocks) || blocks[j].Ticket != blocks[i].Ticket {
			return
		}
		if blocks[j].Duration() < blocks[i].Duration() {
			return
		}
		if best < 0 || blocks[j].Duration() > blocks[best].Duration() {
			best = j
		}
	}
	if i > 0 && blocks[i-1].End.Equal(blocks[i].Start) {
		candidate(i - 1)
	}
	if i+1 < len(blocks) && blocks[i].End.Equal(blocks[i+1].Start) {
		candidate(i + 1)
	}
	return best
}

const overrideReason = "manual override"

// applyOverrides cuts every override out of the blocks in input order (later
// overrides win) and, unless it discards, covers the part of it that lies in
// presence time with an explicit block.
func applyOverrides(base []Block, ov []event.Override) []Block {
	current := base
	for _, o := range ov {
		if !o.End.After(o.Start) {
			continue
		}
		var next []Block
		for _, b := range current {
			next = append(next, cut(b, o.Start, o.End)...)
		}
		if !o.Discard {
			for _, b := range base {
				start, end := maxTime(b.Start, o.Start), minTime(b.End, o.End)
				if end.After(start) {
					next = append(next, Block{Start: start, End: end, Ticket: o.Ticket, Tier: resolver.TierExplicit, Reason: overrideReason})
				}
			}
		}
		current = next
	}
	sort.SliceStable(current, func(i, j int) bool { return current[i].Start.Before(current[j].Start) })
	return mergeTouching(current)
}

// cut removes [start, end) from b and returns the remaining pieces.
func cut(b Block, start, end time.Time) []Block {
	if !b.Start.Before(end) || !b.End.After(start) {
		return []Block{b}
	}
	var out []Block
	if b.Start.Before(start) {
		left := b
		left.End = start
		out = append(out, left)
	}
	if b.End.After(end) {
		right := b
		right.Start = end
		out = append(out, right)
	}
	return out
}

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
