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
//     Config.IdleThreshold, and it is evidence of activity at the moment of
//     the last input (sample time minus idle time). Activity moments at most
//     Config.GapThreshold apart form one presence span (short gaps are
//     bridged). A longer gap, or a gap marker event (event.KindGap) between
//     them, ends the span: gap markers are never bridged and time inside a
//     gap is never counted. A span runs from its first to its last activity,
//     so idle time after the last input is never booked.
//   - The current context is the latest context event at or before a moment
//     (carried forward until it changes). The resolver turns it into ticket,
//     tier and reason. Presence before the first context event yields an
//     unknown block with an empty ticket, so it can be assigned in review.
//   - Precedence of the context, strongest first: a manual pin, then a
//     window-title hint, then git/shell context.
//   - A context event from ManualSource with a ticket (`ticktack switch`)
//     pins the attribution: later non-manual context events no longer change
//     it until the next manual event. A manual event with an empty ticket
//     clears the pin and restores the latest automatic context. Pinned time
//     is attributed with tier explicit and reason "manual switch to <ID>".
//   - A window-title hint (source TitleSource, App set) beats git/shell
//     context, but only while its app is the frontmost app, which is read
//     from the App of the latest presence sample. While another app is in
//     front, git/shell context applies; the hint applies again when its app
//     returns to the front. A hint that names only the repository the git
//     context is already in leaves the git branch ticket in place.
//   - A non-manual context event without repo, branch and ticket (an app-only
//     hint) carries no attribution signal and is ignored.
//   - Carried context loses certainty with age. The age of a context is the
//     time since it was last confirmed: by the context event that set it, by
//     any later context event of the same source (a shell prompt, a checkout,
//     a commit), or by a context event of another source that names the same
//     repository and branch. A title hint that names only the repository the
//     git context is in confirms that context while its app is frontmost.
//     Presence from Config.ContextMaxAge after the last confirmation on keeps
//     the ticket but is only tier suggested, with the reason "context
//     unconfirmed for <age>", so it is confirmed in review instead of being
//     booked silently. Context that resolves to no ticket is left as it is.
//   - A manual pin is exempt from that limit on the day it was set (a
//     calendar day in Config.Location). From the following midnight on it
//     still applies, but only as tier suggested. A new pin starts a new day
//     of exemption. Clearing a pin confirms nothing: the automatic context
//     returns with the age it has. A ContextMaxAge of zero turns both limits
//     off.
//   - Blocks are split whenever the attributed ticket or the context changes;
//     touching blocks with the same ticket, tier, reason and repo are merged.
//   - A block shorter than Config.MinBlock is merged into the longer touching
//     neighbour with the same ticket; if there is none it is kept as unknown.
//     Touching unknown blocks of that kind merge into one, so a burst of short
//     context flips shows as a single block that needs a ticket.
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
	// present. It is independent of GapThreshold.
	IdleThreshold time.Duration
	// MinBlock is the minimum length of a block before it is merged or
	// demoted to unknown.
	MinBlock time.Duration
	// ContextMaxAge is how long context is carried at its own tier after it
	// was last confirmed. Zero means no limit; unlike the thresholds above it
	// does not fall back to the default.
	ContextMaxAge time.Duration
	// Location is the zone whose calendar days bound the exemption of a
	// manual pin from ContextMaxAge. Nil means UTC.
	Location *time.Location
}

// DefaultConfig returns the default derivation thresholds.
func DefaultConfig() Config {
	return Config{
		GapThreshold:  config.DefaultGapThreshold,
		IdleThreshold: config.DefaultIdleThreshold,
		MinBlock:      config.DefaultMinBlockDuration,
		ContextMaxAge: config.DefaultContextMaxAge,
	}
}

// ConfigFrom builds a Config from the user configuration. The context age
// limit and the zone are taken as they are: a zero context_max_age means off.
func ConfigFrom(c config.Config) Config {
	cfg := DefaultConfig()
	if c.GapThreshold > 0 {
		cfg.GapThreshold = c.GapThreshold
	}
	if c.IdleThreshold > 0 {
		cfg.IdleThreshold = c.IdleThreshold
	}
	if c.MinBlockDuration > 0 {
		cfg.MinBlock = c.MinBlockDuration
	}
	cfg.ContextMaxAge = c.ContextMaxAge
	cfg.Location = c.Location
	return cfg
}

func (c Config) withDefaults() Config {
	d := DefaultConfig()
	if c.GapThreshold <= 0 {
		c.GapThreshold = d.GapThreshold
	}
	if c.IdleThreshold <= 0 {
		c.IdleThreshold = d.IdleThreshold
	}
	if c.MinBlock < 0 {
		c.MinBlock = 0
	}
	if c.ContextMaxAge < 0 {
		c.ContextMaxAge = 0
	}
	if c.Location == nil {
		c.Location = time.UTC
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
	// Branch is the branch of that context, empty under the same conditions
	// as Repo. Blocks on different branches are never merged.
	Branch string
}

// Duration returns the block length.
func (b Block) Duration() time.Duration { return b.End.Sub(b.Start) }

const (
	noContextReason = "no context signal"
	minBlockReason  = "block shorter than minimum duration"
)

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
	blocks := attribute(spans, steps, rules, cfg)
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

// presenceSpans folds the sorted events into presence spans.
//
// A present sample is evidence of activity at At minus its idle time, the
// moment of the last input. That moment is never earlier than the latest gap
// marker, so time slept through is not credited. A span starts at its first
// sample, so it never begins ahead of the context event the returning user
// triggered, and ends at its last activity; idle time after the last input is
// not work. An activity more than GapThreshold after the previous one starts
// a new span.
func presenceSpans(sorted []event.Event, cfg Config) []interval {
	var spans []interval
	var cur *interval
	var floor time.Time

	closeSpan := func() {
		if cur != nil && cur.end.After(cur.start) {
			spans = append(spans, *cur)
		}
		cur = nil
	}

	for _, e := range sorted {
		switch e.Kind {
		case event.KindGap:
			closeSpan()
			floor = e.At
		case event.KindPresence:
			idle := time.Duration(e.IdleSeconds) * time.Second
			if idle >= cfg.IdleThreshold {
				continue
			}
			active := maxTime(e.At.Add(-idle), floor)
			if cur != nil && active.Sub(cur.end) > cfg.GapThreshold {
				closeSpan()
			}
			if cur == nil {
				cur = &interval{start: e.At, end: e.At}
			}
			if active.After(cur.end) {
				cur.end = active
			}
		}
	}
	closeSpan()
	return spans
}

// ManualSource is the event source of context events written by
// `ticktack switch`.
const ManualSource = "manual"

type contextStep struct {
	at time.Time
	// event is the context event that created the step.
	event  event.Event
	signal resolver.ContextSignal
	// manualTicket is set while a manual switch is in effect.
	manualTicket string
	// confirmedAt is when the context of the step was last confirmed, which
	// can be earlier than at. It is zero when the step carries no context.
	confirmedAt time.Time
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

// TitleSource is the event source of window-title hints.
const TitleSource = "title"

// autoContext is the automatic (non-manual) context: the latest git or shell
// signal, and the latest window-title hint together with the frontmost app.
type autoContext struct {
	git   contextSource
	hint  contextSource
	front string // bundle ID of the frontmost app, from presence samples
}

type contextSource struct {
	event  event.Event
	signal resolver.ContextSignal
	ok     bool
	// confirmedAt is the time of the latest context event that set or
	// confirmed the signal.
	confirmedAt time.Time
}

// confirmedBy records e as a confirmation when it names the repository and
// branch of the source.
func (s *contextSource) confirmedBy(e event.Event) {
	if s.ok && s.signal.Repo == e.Repo && s.signal.Branch == e.Branch {
		s.confirmedAt = maxTime(s.confirmedAt, e.At)
	}
}

// current returns the effective automatic signal and the event behind it. A
// title hint that names its app applies only while that app is frontmost; a
// hint that is only a repository does not hide the branch of the same
// repository from git. Hints without an app are treated as plain git/shell
// context events (latest wins), as before apps were recorded on hints. A
// repository-only hint that yields to git still confirms the git context.
func (a autoContext) current() contextSource {
	if a.hint.ok && a.hint.event.App == a.front {
		if a.hint.signal.Ticket == "" && a.git.ok && a.git.signal.Repo == a.hint.signal.Repo {
			confirmed := a.git
			confirmed.confirmedAt = maxTime(a.git.confirmedAt, a.hint.confirmedAt)
			return confirmed
		}
		return a.hint
	}
	return a.git
}

// contextTimeline folds the sorted events into steps. Precedence, strongest
// first:
//
//  1. A manual pin (event from ManualSource with a ticket): later automatic
//     context produces no steps until the next manual event; an empty
//     manual ticket clears the pin and restores the automatic context.
//  2. A window-title hint (Source TitleSource with an App), only while that
//     app is the frontmost app according to the latest presence sample with
//     an app. A hint without an App is an ordinary context event.
//  3. The latest git or shell context.
//
// Presence samples take part only to track the frontmost app: they add a step
// when a change of frontmost app changes the effective automatic signal. It
// also returns the effective automatic signal at the end.
//
// Every step records when its context was last confirmed (see the package
// comment); a step that only repeats the context in effect moves that time
// forward.
func contextTimeline(sorted []event.Event) ([]contextStep, resolver.ContextSignal) {
	var steps []contextStep
	var auto autoContext
	manualTicket := ""
	for _, e := range sorted {
		before := auto.current()
		switch {
		case e.Kind == event.KindPresence:
			if e.App == "" || e.App == auto.front {
				continue
			}
			auto.front = e.App
			after := auto.current()
			if manualTicket == "" && after.ok && (!before.ok || after.signal != before.signal) {
				steps = append(steps, contextStep{at: e.At, event: after.event, signal: after.signal, confirmedAt: after.confirmedAt})
			}
			continue
		case e.Kind != event.KindContext:
			continue
		case e.Source == ManualSource:
			manualTicket = strings.ToUpper(strings.TrimSpace(e.Ticket))
			// A pin is confirmed by its own event; clearing it confirms nothing.
			confirmedAt := before.confirmedAt
			if manualTicket != "" {
				confirmedAt = e.At
			}
			steps = append(steps, contextStep{at: e.At, event: e, signal: before.signal, manualTicket: manualTicket, confirmedAt: confirmedAt})
			continue
		case e.Repo == "" && e.Branch == "" && e.Ticket == "":
			// An app-only hint (for example from ActivityWatch) carries no
			// attribution signal and must not wipe the current context.
			continue
		}
		src := contextSource{
			event: e, signal: resolver.ContextSignal{Repo: e.Repo, Branch: e.Branch, Ticket: e.Ticket},
			ok: true, confirmedAt: e.At,
		}
		if e.Source == TitleSource && e.App != "" {
			auto.hint = src
			auto.git.confirmedBy(e)
		} else {
			auto.git = src
			auto.hint.confirmedBy(e)
		}
		if manualTicket == "" {
			now := auto.current()
			steps = append(steps, contextStep{at: e.At, event: now.event, signal: now.signal, confirmedAt: now.confirmedAt})
		}
	}
	return steps, auto.current().signal
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

// attribute intersects every span with the context timeline. A span is cut
// at every context step and at the moment the context in effect goes stale.
func attribute(spans []interval, steps []contextStep, rules resolver.Rules, cfg Config) []Block {
	var blocks []Block
	for _, span := range spans {
		// idx is the number of steps at or before the span start.
		idx := sort.Search(len(steps), func(i int) bool { return steps[i].at.After(span.start) })
		from := span.start
		for from.Before(span.end) {
			to := span.end
			reachesNextStep := idx < len(steps) && steps[idx].at.Before(to)
			if reachesNextStep {
				to = steps[idx].at
			}
			if idx > 0 {
				if stale, ok := steps[idx-1].staleFrom(cfg); ok && stale.After(from) && stale.Before(to) {
					to = stale
					reachesNextStep = false
				}
			}
			if to.After(from) {
				blocks = append(blocks, blockFor(from, to, idx, steps, rules, cfg))
			}
			from = to
			if reachesNextStep {
				idx++
			}
		}
	}
	return blocks
}

// blockFor builds the block for [from, to) under the context in effect after
// the first n steps. The caller cuts at the moment the context goes stale, so
// the block is stale as a whole or not at all.
func blockFor(from, to time.Time, n int, steps []contextStep, rules resolver.Rules, cfg Config) Block {
	if n == 0 {
		return Block{Start: from, End: to, Tier: resolver.TierUnknown, Reason: noContextReason}
	}
	step := steps[n-1]
	a := step.attribution(rules)
	if stale, ok := step.staleFrom(cfg); ok && !from.Before(stale) {
		a = StaleAttribution(a, from.Sub(step.confirmedAt))
	}
	return Block{Start: from, End: to, Ticket: a.Ticket, Tier: a.Tier, Reason: a.Reason, Repo: step.signal.Repo, Branch: step.signal.Branch}
}

func sameAttribution(a, b Block) bool {
	return a.Ticket == b.Ticket && a.Tier == b.Tier && a.Reason == b.Reason && a.Repo == b.Repo && a.Branch == b.Branch
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
			blocks[i].Reason = minBlockReason
			// The old context no longer describes the block; keeping it would
			// stop neighbouring demoted blocks from merging into one.
			blocks[i].Repo = ""
			blocks[i].Branch = ""
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
