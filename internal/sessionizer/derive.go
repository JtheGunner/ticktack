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
//     returns with the age it has.
//   - The two rules are independent. A ContextMaxAge of zero turns off the
//     age limit of automatic context only: a pin carried over from an earlier
//     day is only tier suggested whatever the limit is.
//   - Foreground app classes (Config.AppClasses, by the bundle ID on each
//     presence sample): an unlisted app is work and changes nothing. Presence
//     in a personal app ends the presence span like a gap and is not counted,
//     so it never shows up as unassigned time, and short-block folding never
//     reaches across it. Presence in a neutral app keeps the ticket of the
//     context but only as tier suggested, with the reason NeutralAppReason, so
//     it is confirmed in review and never booked by auto mode. It does not
//     count as carried context. A manual pin is not lowered, and stale time
//     keeps its own reason.
//   - Blocks are split whenever the attributed ticket or the context changes;
//     touching blocks with the same ticket, tier, reason and repo are merged.
//   - A block shorter than Config.MinBlock is merged into the longer touching
//     neighbour with the same ticket; if there is none it is kept as unknown.
//     Touching unknown blocks of that kind merge into one, so a burst of short
//     context flips shows as a single block that needs a ticket.
//   - A short block with another ticket that touches a block on each side,
//     both with the same ticket, is folded into one of them: the two sides
//     and the short block become one block of that ticket, so a brief look
//     at another branch between two stretches of one ticket costs no review.
//     There is no length limit besides Config.MinBlock. Only a short block
//     of tier explicit or rule is folded (the user knowingly worked
//     elsewhere); one of tier learned, suggested or unknown, which may be
//     presence without any context, is not folded and is demoted as usual.
//     The folded-into neighbour is the one with the lower tier (the longer
//     one on a tie, then the earlier one). The block takes its reason and
//     rule, and the lower of the neighbour's and the short block's tier, so
//     folding never raises a tier. The folded time is recorded in
//     Block.Folded. A block from a manual switch is never folded into or
//     demoted, and neither is a manual switch block taken as a neighbour.
//     Overrides are applied later and are never folded.
//   - Merging never raises a tier. A short suggested block (stale context)
//     next to confirmed time of its ticket stays a suggested block of its own,
//     however short, so stale minutes never end up in a block that is booked
//     without review. A short confirmed block that merges into a suggested
//     neighbour takes that neighbour's tier and reason. With no longer
//     neighbour of its ticket to join, it merges into a touching suggestion
//     of its ticket even when that one is shorter, so a short stretch of
//     presence that context going stale cuts in two keeps its ticket as one
//     suggested block instead of losing it.
//   - Events are sorted by time (then ID) first, so backwards clock jumps
//     never produce a block with End <= Start.
//   - Overrides are applied last: the override interval is cut out of the
//     blocks; a discard or break override drops it (a break is also reported
//     as break time, never booked), a reassign covers the intersection with
//     the presence time with an explicit block. Overrides are inputs, so
//     re-deriving with changed rules keeps them.
//   - Idle gaps are the holes of at least Config.GapThreshold between two
//     presence spans of the range; time before the first and after the last
//     presence is never a gap. A fill override (created only by an explicit
//     action in review) covers its intersection with an idle gap with an
//     explicit block of reason FillReason. It never reaches presence time, so
//     nothing is counted twice, and nothing here infers duration from context:
//     the time exists only because the user asked for it. A gap that a fill,
//     break or discard override covers is not offered again (Result.Gaps).
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
	// ContextMaxAge is how long automatic context is carried at its own tier
	// after it was last confirmed. Zero means no limit; unlike the thresholds
	// above it does not fall back to the default. It does not affect a manual
	// pin, which is carried at its own tier until the end of its day.
	ContextMaxAge time.Duration
	// Location is the zone whose calendar days bound the time a manual pin
	// is carried at its own tier. Nil means UTC.
	Location *time.Location
	// AppClasses maps the bundle ID of a foreground app to its class. It is
	// read-only and nil by default: every app is then config.ClassWork.
	AppClasses map[string]config.AppClass
}

// appClass returns the class of the app; an unlisted app, or a sample without
// an app, is config.ClassWork.
func (c Config) appClass(app string) config.AppClass {
	if class, ok := c.AppClasses[app]; ok {
		return class
	}
	return config.ClassWork
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
// limit and the zone are taken as they are: a zero context_max_age means no
// age limit for automatic context.
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
	cfg.AppClasses = c.AppClasses
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
	// Rule is the kind of rule that decided the attribution: a resolver rule
	// kind, ManualRule for a manual switch or OverrideRule for a user
	// override. It is resolver.RuleNone when nothing decided.
	Rule resolver.RuleKind
	// Repo is the repository of the context the block was derived from; it
	// is empty for override blocks and when no context existed.
	Repo string
	// Branch is the branch of that context, empty under the same conditions
	// as Repo. Blocks on different branches are never merged.
	Branch string
	// Folded lists the short blocks of other attributions that were folded
	// into this block because blocks of its ticket enclose them. Their time is
	// part of the block and carries its tier and rule.
	Folded []Fold `json:",omitempty"`
}

// Fold records a short block that was folded into an enclosing block (see
// the package comment). Ticket is empty when the folded block had none.
type Fold struct {
	Start, End time.Time
	Ticket     string
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
	return DeriveDay(events, ov, cfg, rules).Blocks
}

// DeriveDay is Derive with the by-products of the overrides: the break time
// and the idle gaps still open for filling. Derive is its Blocks.
func DeriveDay(events []event.Event, ov []event.Override, cfg Config, rules resolver.Rules) Result {
	cfg = cfg.withDefaults()
	sorted := sortedEvents(events)
	spans := presenceSpans(sorted, cfg)
	if len(spans) == 0 {
		return Result{}
	}
	steps, _ := contextTimeline(sorted)
	blocks := attribute(spans, steps, rules, cfg)
	blocks = mergeTouching(blocks)
	blocks = resolveShortBlocks(blocks, cfg.MinBlock)
	blocks = mergeTouching(blocks)
	return applyOverrides(blocks, idleGaps(spans, cfg.GapThreshold), ov)
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

// interval is a stretch of presence. neutral is set when the foreground app
// during it is of class neutral.
type interval struct {
	start, end time.Time
	neutral    bool
}

// presenceSpans folds the sorted events into presence spans.
//
// A present sample is evidence of activity at At minus its idle time, the
// moment of the last input. That moment is never earlier than the latest gap
// marker, so time slept through is not credited. A span starts at its first
// sample, so it never begins ahead of the context event the returning user
// triggered, and ends at its last activity; idle time after the last input is
// not work. An activity more than GapThreshold after the previous one starts
// a new span.
//
// The class of the sample's own app (Config.AppClasses) decides how its time
// counts: a personal app ends the span like a gap does, without moving the
// floor, so the time in it is never counted. A neutral app keeps the span, but
// the stretch up to its activity is returned as a separate neutral interval.
// Touching intervals of one span differ only in that flag.
func presenceSpans(sorted []event.Event, cfg Config) []interval {
	var spans []interval
	var cur *interval
	var floor time.Time

	flush := func() {
		if cur != nil && cur.end.After(cur.start) {
			spans = append(spans, *cur)
		}
	}
	closeSpan := func() {
		flush()
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
			class := cfg.appClass(e.App)
			if class == config.ClassPersonal {
				closeSpan()
				continue
			}
			neutral := class == config.ClassNeutral
			active := maxTime(e.At.Add(-idle), floor)
			if cur != nil && active.Sub(cur.end) > cfg.GapThreshold {
				closeSpan()
			}
			if cur == nil {
				cur = &interval{start: e.At, end: e.At, neutral: neutral}
			}
			if cur.neutral != neutral {
				flush()
				cur = &interval{start: cur.end, end: cur.end, neutral: neutral}
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

// decision resolves the step and names the deciding rule. A manual switch is
// explicit by definition.
func (s contextStep) decision(rules resolver.Rules) resolver.Decision {
	if s.manualTicket != "" {
		return resolver.Decision{
			Attribution: resolver.Attribution{
				Ticket: s.manualTicket,
				Tier:   resolver.TierExplicit,
				Reason: "manual switch to " + s.manualTicket,
			},
			Rule: ManualRule,
		}
	}
	return resolver.Decide(s.signal, rules)
}

// attribution is the verdict of decision without the deciding rule.
func (s contextStep) attribution(rules resolver.Rules) resolver.Attribution {
	return s.decision(rules).Attribution
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
				blocks = append(blocks, blockFor(from, to, idx, steps, rules, cfg, span.neutral))
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
// the block is stale as a whole or not at all. Time in a neutral app keeps the
// ticket but only as a suggestion (see NeutralAppAttribution); a manual pin is
// the user's own statement and is not lowered, and stale time already carries
// the lower tier and its own reason.
func blockFor(from, to time.Time, n int, steps []contextStep, rules resolver.Rules, cfg Config, neutral bool) Block {
	if n == 0 {
		return Block{Start: from, End: to, Tier: resolver.TierUnknown, Reason: noContextReason, Rule: resolver.RuleNone}
	}
	step := steps[n-1]
	d := step.decision(rules)
	a := d.Attribution
	if stale, ok := step.staleFrom(cfg); ok && !from.Before(stale) {
		// The rule that produced the ticket stays: staleness only lowers the tier.
		a = StaleAttribution(a, from.Sub(step.confirmedAt))
	}
	if neutral && step.manualTicket == "" {
		a = NeutralAppAttribution(a)
	}
	return Block{
		Start: from, End: to, Ticket: a.Ticket, Tier: a.Tier, Reason: a.Reason, Rule: d.Rule,
		Repo: step.signal.Repo, Branch: step.signal.Branch,
	}
}

func sameAttribution(a, b Block) bool {
	return a.Ticket == b.Ticket && a.Tier == b.Tier && a.Reason == b.Reason && a.Rule == b.Rule &&
		a.Repo == b.Repo && a.Branch == b.Branch
}

// mergeTouching joins neighbours that touch and carry the same attribution.
func mergeTouching(blocks []Block) []Block {
	var out []Block
	for _, b := range blocks {
		if n := len(out); n > 0 && out[n-1].End.Equal(b.Start) && sameAttribution(out[n-1], b) {
			out[n-1].End = b.End
			out[n-1].Folded = concatFolds(out[n-1].Folded, b.Folded)
			continue
		}
		out = append(out, b)
	}
	return out
}

// resolveShortBlocks merges blocks shorter than minBlock into the longer
// touching same-ticket neighbour and demotes the rest to unknown, except for
// suggestions that were kept apart from confirmed time of their ticket.
func resolveShortBlocks(blocks []Block, minBlock time.Duration) []Block {
	if minBlock <= 0 {
		return blocks
	}
	for {
		merged := false
		for i := range blocks {
			if blocks[i].Duration() >= minBlock || isManual(blocks[i]) {
				continue
			}
			target := mergeTarget(blocks, i)
			folded := false
			if target < 0 {
				target = foldTarget(blocks, i)
				folded = target >= 0
			}
			if target < 0 {
				continue
			}
			lo, hi := i, target
			if lo > hi {
				lo, hi = hi, lo
			}
			combined := blocks[target]
			combined.Start, combined.End = blocks[lo].Start, blocks[hi].End
			var entry []Fold
			if folded {
				entry = []Fold{{Start: blocks[i].Start, End: blocks[i].End, Ticket: blocks[i].Ticket}}
				if tierRank[blocks[i].Tier] < tierRank[combined.Tier] {
					combined.Tier = blocks[i].Tier
				}
			}
			combined.Folded = concatFolds(blocks[target].Folded, blocks[i].Folded, entry)
			blocks = append(blocks[:lo], append([]Block{combined}, blocks[hi+1:]...)...)
			merged = true
			break
		}
		if !merged {
			break
		}
	}
	for i := range blocks {
		if blocks[i].Duration() < minBlock && !isManual(blocks[i]) && !isDemoted(blocks[i]) && !touchesConfirmedTime(blocks, i) {
			blocks[i].Ticket = ""
			blocks[i].Tier = resolver.TierUnknown
			blocks[i].Reason = minBlockReason
			blocks[i].Rule = resolver.RuleNone
			// The old context no longer describes the block; keeping it would
			// stop neighbouring demoted blocks from merging into one.
			blocks[i].Repo = ""
			blocks[i].Branch = ""
		}
	}
	return blocks
}

// isManual reports whether the block comes from a manual switch. An explicit
// statement of the user is never folded away or demoted for being short.
func isManual(b Block) bool {
	return b.Rule == ManualRule
}

var tierRank = map[resolver.Tier]int{
	resolver.TierExplicit:  4,
	resolver.TierRule:      3,
	resolver.TierLearned:   2,
	resolver.TierSuggested: 1,
	resolver.TierUnknown:   0,
}

// foldTarget returns the index of the neighbour block i is folded into, or
// -1. Block i must touch a block on each side and both must carry the same
// ticket, which is not the ticket of block i (a block of the same ticket is
// merged, not folded). Of the two the one with the lower tier is taken, then
// the longer, then the earlier, so folded time never gets a higher tier than
// the lowest enclosing block has. Nothing is folded when block i is below
// tier rule (see canBeFolded) or when a neighbour is a manual switch block.
func foldTarget(blocks []Block, i int) int {
	if i == 0 || i+1 >= len(blocks) || !canBeFolded(blocks[i]) {
		return -1
	}
	prev, next := blocks[i-1], blocks[i+1]
	if !prev.End.Equal(blocks[i].Start) || !blocks[i].End.Equal(next.Start) {
		return -1
	}
	if prev.Ticket == "" || prev.Ticket != next.Ticket || prev.Ticket == blocks[i].Ticket {
		return -1
	}
	if isManual(prev) || isManual(next) {
		return -1
	}
	target := i - 1
	switch {
	case tierRank[next.Tier] < tierRank[prev.Tier]:
		target = i + 1
	case tierRank[next.Tier] == tierRank[prev.Tier] && next.Duration() > prev.Duration():
		target = i + 1
	}
	return target
}

// canBeFolded reports whether a short block of another attribution may be
// folded away: only confirmed time (tier explicit or rule) may, because the
// user knowingly worked elsewhere. A block of a lower tier may be presence
// without any context, and folding it would turn it into confirmed time of
// the enclosing ticket.
func canBeFolded(b Block) bool {
	return tierRank[b.Tier] >= tierRank[resolver.TierRule]
}

// concatFolds joins fold lists into a new list ordered by start time; it
// returns nil when there is nothing to join.
func concatFolds(lists ...[]Fold) []Fold {
	var out []Fold
	for _, l := range lists {
		out = append(out, l...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

// clipFolds returns the folds that lie in [start, end), cut to that range.
func clipFolds(folds []Fold, start, end time.Time) []Fold {
	var out []Fold
	for _, f := range folds {
		f.Start, f.End = maxTime(f.Start, start), minTime(f.End, end)
		if f.End.After(f.Start) {
			out = append(out, f)
		}
	}
	return out
}

func isDemoted(b Block) bool {
	return b.Tier == resolver.TierUnknown && b.Ticket == ""
}

// upgrades reports whether merging short into target would raise the tier of
// its time: a suggested block, which is what stale context yields, joins only
// another suggested block. The other way round is allowed, the combined block
// then carries the lower tier of the target.
func upgrades(short, target Block) bool {
	return short.Tier == resolver.TierSuggested && target.Tier != resolver.TierSuggested
}

// touchesConfirmedTime reports whether block i is a suggestion next to a
// block of its own ticket that it must not merge into. Such a block is kept
// as it is: the ticket is not in doubt, only the minutes need confirming.
func touchesConfirmedTime(blocks []Block, i int) bool {
	refused := func(j int) bool {
		return blocks[j].Ticket == blocks[i].Ticket && upgrades(blocks[i], blocks[j])
	}
	if i > 0 && blocks[i-1].End.Equal(blocks[i].Start) && refused(i-1) {
		return true
	}
	return i+1 < len(blocks) && blocks[i].End.Equal(blocks[i+1].Start) && refused(i+1)
}

// mergeTarget returns the index of the longer touching neighbour of block i
// with the same ticket that the block may merge into, or -1. A tie prefers
// the previous block.
//
// A shorter neighbour is a target too when it is a suggestion that may not
// merge into block i: the two would otherwise both lose a ticket that is not
// in doubt, as when the moment context goes stale cuts one short stretch of
// presence in two. Such a neighbour is shorter than any regular target, so it
// is chosen only when there is none.
func mergeTarget(blocks []Block, i int) int {
	best := -1
	candidate := func(j int) {
		if j < 0 || j >= len(blocks) || blocks[j].Ticket != blocks[i].Ticket || upgrades(blocks[i], blocks[j]) {
			return
		}
		if blocks[j].Duration() < blocks[i].Duration() && !upgrades(blocks[j], blocks[i]) {
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

const (
	overrideReason = "manual override"
	// fillReason is user-facing display text of a filled idle gap.
	fillReason = "manuell gefüllt"
)

// FillRule is the rule kind reported for a block created by filling an idle
// gap in review.
const FillRule resolver.RuleKind = "fill"

// OverrideRule is the rule kind reported for a block created by a user
// override.
const OverrideRule resolver.RuleKind = "override"

// Interval is a stretch of time.
type Interval struct{ Start, End time.Time }

// Break is time the user marked as a break. Idle reports that it lies in an
// idle gap (no presence) rather than in presence time; the two are never
// merged into one break.
type Break struct {
	Start, End time.Time
	Idle       bool
}

// Duration returns the break length.
func (b Break) Duration() time.Duration { return b.End.Sub(b.Start) }

// Result is a derived range: the blocks to book, the breaks (never booked)
// and the idle gaps still offered for filling.
type Result struct {
	Blocks []Block
	Breaks []Break
	// Gaps are the holes of at least Config.GapThreshold between two presence
	// spans, minus what a fill, break or discard override already covers.
	// Time before the first and after the last presence is never a gap.
	Gaps []Interval
}

// idleGaps returns the holes of at least threshold between consecutive
// presence spans.
func idleGaps(spans []interval, threshold time.Duration) []Interval {
	var gaps []Interval
	for i := 1; i < len(spans); i++ {
		g := Interval{Start: spans[i-1].end, End: spans[i].start}
		if g.End.After(g.Start) && g.End.Sub(g.Start) >= threshold {
			gaps = append(gaps, g)
		}
	}
	return gaps
}

// applyOverrides applies every override in input order (later overrides win).
// Each one is cut out of the blocks and breaks first, then:
//   - reassign covers the part in presence time with an explicit block;
//   - fill covers the part in an idle gap with an explicit block of reason
//     FillReason; it never reaches presence time, so nothing is counted twice;
//   - break records the part in presence time and in idle gaps as break time;
//   - discard only cuts.
func applyOverrides(base []Block, gaps []Interval, ov []event.Override) Result {
	current := base
	var breaks []Break
	for _, o := range ov {
		if !o.End.After(o.Start) {
			continue
		}
		// A fill acts on idle time only: it must not cut presence time.
		touched := []Interval{{o.Start, o.End}}
		if o.EffectiveKind() == event.OverrideFill {
			touched = clipToGaps(gaps, o)
		}
		var next []Block
		for _, b := range current {
			pieces := []Block{b}
			for _, iv := range touched {
				var rest []Block
				for _, p := range pieces {
					rest = append(rest, cut(p, iv.Start, iv.End)...)
				}
				pieces = rest
			}
			next = append(next, pieces...)
		}
		for _, iv := range touched {
			breaks = cutBreaks(breaks, iv.Start, iv.End)
		}
		switch o.EffectiveKind() {
		case event.OverrideReassign:
			for _, b := range base {
				if start, end := maxTime(b.Start, o.Start), minTime(b.End, o.End); end.After(start) {
					next = append(next, Block{Start: start, End: end, Ticket: o.Ticket, Tier: resolver.TierExplicit, Reason: overrideReason, Rule: OverrideRule})
				}
			}
		case event.OverrideFill:
			for _, iv := range touched {
				next = append(next, Block{Start: iv.Start, End: iv.End, Ticket: o.Ticket, Tier: resolver.TierExplicit, Reason: fillReason, Rule: FillRule})
			}
		case event.OverrideBreak:
			for _, b := range base {
				if start, end := maxTime(b.Start, o.Start), minTime(b.End, o.End); end.After(start) {
					breaks = append(breaks, Break{Start: start, End: end})
				}
			}
			for _, iv := range clipToGaps(gaps, o) {
				breaks = append(breaks, Break{Start: iv.Start, End: iv.End, Idle: true})
			}
		}
		current = next
	}
	sort.SliceStable(current, func(i, j int) bool { return current[i].Start.Before(current[j].Start) })
	return Result{Blocks: mergeTouching(current), Breaks: mergeBreaks(breaks), Gaps: openGaps(gaps, ov)}
}

// clipToGaps returns the parts of o that lie in an idle gap.
func clipToGaps(gaps []Interval, o event.Override) []Interval {
	var out []Interval
	for _, g := range gaps {
		if start, end := maxTime(g.Start, o.Start), minTime(g.End, o.End); end.After(start) {
			out = append(out, Interval{start, end})
		}
	}
	return out
}

// cutBreaks removes [start, end) from the breaks.
func cutBreaks(breaks []Break, start, end time.Time) []Break {
	var out []Break
	for _, b := range breaks {
		if !b.Start.Before(end) || !b.End.After(start) {
			out = append(out, b)
			continue
		}
		if b.Start.Before(start) {
			out = append(out, Break{Start: b.Start, End: start, Idle: b.Idle})
		}
		if b.End.After(end) {
			out = append(out, Break{Start: end, End: b.End, Idle: b.Idle})
		}
	}
	return out
}

// mergeBreaks sorts the breaks and joins touching ones of the same kind.
func mergeBreaks(breaks []Break) []Break {
	sort.SliceStable(breaks, func(i, j int) bool { return breaks[i].Start.Before(breaks[j].Start) })
	var out []Break
	for _, b := range breaks {
		if n := len(out); n > 0 && out[n-1].Idle == b.Idle && out[n-1].End.Equal(b.Start) {
			out[n-1].End = b.End
			continue
		}
		out = append(out, b)
	}
	return out
}

// openGaps returns the idle gaps minus the ranges of every fill, break and
// discard override, so time that was filled, marked as a break or dismissed is
// not offered again. A reassign never touches idle time and is ignored.
func openGaps(gaps []Interval, ov []event.Override) []Interval {
	var open []Interval
	for _, g := range gaps {
		pieces := []Interval{g}
		for _, o := range ov {
			if o.EffectiveKind() == event.OverrideReassign || !o.End.After(o.Start) {
				continue
			}
			var next []Interval
			for _, p := range pieces {
				if !p.Start.Before(o.End) || !p.End.After(o.Start) {
					next = append(next, p)
					continue
				}
				if p.Start.Before(o.Start) {
					next = append(next, Interval{p.Start, o.Start})
				}
				if p.End.After(o.End) {
					next = append(next, Interval{o.End, p.End})
				}
			}
			pieces = next
		}
		open = append(open, pieces...)
	}
	return open
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
		left.Folded = clipFolds(b.Folded, left.Start, left.End)
		out = append(out, left)
	}
	if b.End.After(end) {
		right := b
		right.Start = end
		right.Folded = clipFolds(b.Folded, right.Start, right.End)
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
