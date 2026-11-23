// Package review turns the derived day into numbered entries the user can
// confirm, reassign, discard or split, and decides which items may be booked.
//
// Nothing here talks to a terminal: the command layer feeds it actions and
// renders its entries. Corrections are stored as overrides (events stay
// append-only); confirmations are stored per item together with the minutes
// that were confirmed, so a changed day silently invalidates them.
package review

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/localday"
	"github.com/JtheGunner/ticktack/internal/reporoots"
	"github.com/JtheGunner/ticktack/internal/resolver"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
	"github.com/JtheGunner/ticktack/internal/store"
	"github.com/JtheGunner/ticktack/internal/youtrack"
)

// Reader is the read side of the store needed to derive a day.
type Reader interface {
	EventReader
	Overrides(ctx context.Context, from, to time.Time) ([]event.Override, error)
	LearnedRules(ctx context.Context) ([]store.LearnedRule, error)
	Projects(ctx context.Context) ([]store.Project, time.Time, error)
	DefaultTasks(ctx context.Context) (map[string]string, error)
}

// Store is everything review needs from the persistence layer.
type Store interface {
	Reader
	Confirmations(ctx context.Context, day string) ([]store.Confirmation, error)
	CapAcknowledgementFor(ctx context.Context, day string) (store.CapAcknowledgement, bool, error)
	// InTx runs every write of a session action together with its review log
	// entry in one transaction, so a crash cannot leave the one without the
	// other.
	InTx(ctx context.Context, fn func(tx store.Tx) error) error
}

// Errors returned by session actions.
var (
	ErrNoSuchEntry    = errors.New("no such entry")
	ErrGapNotAttached = errors.New("unassigned time cannot be confirmed: assign a ticket first")
	ErrInvalidTicket  = errors.New("invalid ticket ID")
	ErrInvalidSplit   = errors.New("split time must lie strictly inside the entry")
	ErrNoCapWarning   = errors.New("the day is not over the daily cap")
	// ErrSplitNeedsTicket is returned when splitting unassigned time without
	// a ticket for the second part: the result would still be unassigned.
	ErrSplitNeedsTicket = errors.New("splitting unassigned time needs a ticket for the second part")
	// ErrSplitSameTicket is returned for a split of an override block that
	// keeps its ticket: the override would merge straight back into the block
	// and change nothing.
	ErrSplitSameTicket = errors.New("an override block cannot be split without a different ticket for the second part")
	// ErrBreakNotSplittable is returned for a split of a break entry.
	ErrBreakNotSplittable = errors.New("a break cannot be split: discard it or give the time a ticket")
	// ErrNotConfirmable is returned when confirming idle time or a break.
	ErrNotConfirmable = errors.New("idle time and breaks are never booked: give idle time a ticket first")
	// ErrAlreadyBreak is returned when marking a break as a break.
	ErrAlreadyBreak = errors.New("entry is already a break")
	// ErrNothingToUndo is returned by UndoLast when this session has created no
	// override that is still in place.
	ErrNothingToUndo = errors.New("nothing to undo in this session")
)

// CapBlockedError reports that a day above the daily cap was not acknowledged.
type CapBlockedError struct {
	Day          civil.Date
	TotalMinutes int
	CapMinutes   int
}

func (e *CapBlockedError) Error() string {
	return fmt.Sprintf("%s: %d min exceeds the daily cap of %d min and was not acknowledged",
		e.Day, e.TotalMinutes, e.CapMinutes)
}

// RulesFrom converts the configuration into resolver rules.
func RulesFrom(cfg config.Config) resolver.Rules {
	rules := resolver.Rules{TicketPattern: cfg.TicketPattern}
	for _, r := range cfg.Rules {
		rules.Repos = append(rules.Repos, resolver.RepoRule(r))
	}
	if cfg.DefaultIssue != nil {
		rules.GlobalProject = cfg.DefaultIssue.Project
	}
	return rules
}

// LoadRules returns the configured rules plus the learned rules stored in r.
// It is the single loading point shared by today, status, review and sync, so
// every command sees the same attribution.
func LoadRules(ctx context.Context, r interface {
	LearnedRules(ctx context.Context) ([]store.LearnedRule, error)
	Projects(ctx context.Context) ([]store.Project, time.Time, error)
	DefaultTasks(ctx context.Context) (map[string]string, error)
}, cfg config.Config) (resolver.Rules, error) {
	rules := RulesFrom(cfg)
	learned, err := r.LearnedRules(ctx)
	if err != nil {
		return resolver.Rules{}, err
	}
	for _, l := range learned {
		rules.Learned = append(rules.Learned, resolver.LearnedRule{ID: l.ID, Repo: l.Repo, Branch: l.BranchPattern, Ticket: l.Ticket})
	}
	if cfg.DefaultIssue == nil {
		return rules, nil
	}
	projects, _, err := r.Projects(ctx)
	if err != nil {
		return resolver.Rules{}, err
	}
	for _, p := range projects {
		rules.Projects = append(rules.Projects, resolver.ProjectInfo{Key: p.Key, Name: p.Name})
	}
	if rules.DefaultTasks, err = r.DefaultTasks(ctx); err != nil {
		return resolver.Rules{}, err
	}
	if len(cfg.RepoRoots) > 0 {
		// Without a home directory "~/" roots cannot be expanded and match
		// nothing, so those repositories fall back to the global task.
		home, _ := os.UserHomeDir()
		roots := cfg.RepoRoots
		rules.FolderOf = func(repo string) (string, bool) { return reporoots.FolderOf(roots, home, repo) }
	}
	return rules, nil
}

// Day is one derived local day.
type Day struct {
	Date       civil.Date
	Start, End time.Time
	Blocks     []sessionizer.Block
	Items      []sessionizer.DayItem
	// TotalMinutes is the rounded total over all items, including
	// unassigned time.
	TotalMinutes int
	// CapMinutes is the configured daily cap; zero when disabled.
	CapMinutes int
	OverCap    bool
	// Rounding reconciles the exact tracked time of the day with
	// TotalMinutes and names the tickets that rounded away.
	Rounding sessionizer.DaySummary
	// Breaks is the time marked as a break; it is never booked.
	Breaks []sessionizer.Break
	// BreakMinutes is the break total, rounded to whole minutes.
	BreakMinutes int
	// IdleGaps are the idle gaps of the day still open for filling.
	IdleGaps []sessionizer.Interval
}

func location(cfg config.Config) *time.Location {
	if cfg.Location == nil {
		return time.Local
	}
	return cfg.Location
}

// LoadDay derives the blocks and aggregated items of one local day. The day
// starts with the context carried over from before it (see
// EventsWithCarriedContext); its time comes from the day's own presence only.
func LoadDay(ctx context.Context, r Reader, cfg config.Config, day civil.Date) (Day, error) {
	loc := location(cfg)
	from, to := localday.Bounds(day, loc)
	in, err := LoadDayInput(ctx, r, cfg, from, to)
	if err != nil {
		return Day{}, err
	}
	blocks := in.Blocks
	rc := sessionizer.RoundingConfigFrom(cfg)
	d := Day{Date: day, Start: from, End: to, CapMinutes: int(rc.DailyCap / time.Minute)}
	for _, b := range blocks {
		if b.End.After(from) && b.Start.Before(to) {
			d.Blocks = append(d.Blocks, b)
		}
	}
	var breakTotal time.Duration
	for _, b := range in.Breaks {
		if b.End.After(from) && b.Start.Before(to) {
			d.Breaks = append(d.Breaks, b)
			breakTotal += minTime(b.End, to).Sub(maxTime(b.Start, from))
		}
	}
	d.BreakMinutes = int((breakTotal + 30*time.Second) / time.Minute)
	for _, g := range in.IdleGaps {
		if g.End.After(from) && g.Start.Before(to) {
			d.IdleGaps = append(d.IdleGaps, g)
		}
	}
	rounded := sessionizer.AggregateReport(blocks, rc, loc)
	for _, s := range rounded.Days {
		if s.Date == day {
			d.Rounding = s
		}
	}
	commits := commitRefs(in.Events, blocks, from, to)
	for _, it := range rounded.Items {
		if it.Date != day {
			continue
		}
		it.Commits = commits[it.Ticket]
		d.Items = append(d.Items, it)
		d.TotalMinutes += it.Minutes
		d.OverCap = d.OverCap || it.OverCap
	}
	return d, nil
}

// itemType is the work item type of the day's items.
func itemType(it sessionizer.DayItem) string {
	if it.Type == "" {
		return sessionizer.DefaultWorkItemType
	}
	return it.Type
}

// ConfirmedItems returns the items of a day that may be booked: confirmed,
// still at their confirmed minutes, and attributed to a ticket. When the day
// total exceeds the daily cap the cap must have been acknowledged for exactly
// that total, otherwise a *CapBlockedError is returned and nothing is
// released. Unconfirmed items are never returned.
func ConfirmedItems(ctx context.Context, st Store, cfg config.Config, day civil.Date) ([]sessionizer.DayItem, error) {
	d, err := LoadDay(ctx, st, cfg, day)
	if err != nil {
		return nil, err
	}
	if d.OverCap {
		ok, err := capAcknowledged(ctx, st, d)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, &CapBlockedError{Day: day, TotalMinutes: d.TotalMinutes, CapMinutes: d.CapMinutes}
		}
	}
	confirmed, err := confirmedSet(ctx, st, day)
	if err != nil {
		return nil, err
	}
	var out []sessionizer.DayItem
	for _, it := range d.Items {
		if it.Ticket != "" && isConfirmed(confirmed, it) {
			out = append(out, it)
		}
	}
	return out, nil
}

func capAcknowledged(ctx context.Context, st Store, d Day) (bool, error) {
	a, ok, err := st.CapAcknowledgementFor(ctx, d.Date.String())
	if err != nil {
		return false, err
	}
	return ok && a.TotalMinutes == d.TotalMinutes, nil
}

type itemKey struct{ issue, typ string }

func confirmedSet(ctx context.Context, st Store, day civil.Date) (map[itemKey]int, error) {
	list, err := st.Confirmations(ctx, day.String())
	if err != nil {
		return nil, err
	}
	m := make(map[itemKey]int, len(list))
	for _, c := range list {
		m[itemKey{c.Issue, c.Type}] = c.Minutes
	}
	return m, nil
}

func isConfirmed(set map[itemKey]int, it sessionizer.DayItem) bool {
	minutes, ok := set[itemKey{it.Ticket, itemType(it)}]
	return ok && minutes == it.Minutes
}

// EntryKind distinguishes attributed blocks from unassigned presence, idle
// gaps and breaks.
type EntryKind int

// Entry kinds.
const (
	// KindBlock is time attributed to a ticket.
	KindBlock EntryKind = iota
	// KindUnassigned is presence without a ticket ("needs ticket").
	KindUnassigned
	// KindIdle is an idle gap between presence spans that can be filled with a
	// ticket, marked as a break or discarded. It carries no time until filled.
	KindIdle
	// KindBreak is time marked as a break. It is never booked.
	KindBreak
)

// Entry is one numbered line of the review.
type Entry struct {
	// Index is the 1-based number the user refers to.
	Index      int
	Kind       EntryKind
	Start, End time.Time
	Ticket     string
	Tier       resolver.Tier
	Reason     string
	// Rule is the kind of rule that decided the attribution (see
	// sessionizer.Block.Rule).
	Rule resolver.RuleKind
	// Repo and Branch are the context the entry was derived from; empty for
	// override entries and when no context existed.
	Repo, Branch string
	// Minutes is the length of the entry, rounded to whole minutes.
	Minutes int
	// Confirmed is set when the entry's item is confirmed at its current
	// minutes. It is never set for gaps, idle gaps and breaks.
	Confirmed bool
	// Idle is set on a KindBreak entry that lies in an idle gap rather than in
	// presence time.
	Idle bool
}

// isFill reports whether the entry is time the user filled into an idle gap.
func (e Entry) isFill() bool { return e.Kind == KindBlock && e.Rule == sessionizer.FillRule }

// inIdleTime reports whether the entry's time has no presence behind it, so a
// ticket given to it must be stored as a fill instead of a reassign.
func (e Entry) inIdleTime() bool {
	return e.Kind == KindIdle || e.isFill() || (e.Kind == KindBreak && e.Idle)
}

// Summary counts the outcome of a review session.
type Summary struct {
	Confirmed   int
	Discarded   int
	Unconfirmed int
	// Unassigned counts unassigned items that still need a ticket.
	Unassigned int
	// BreakMinutes is the time marked as a break. It is never booked and not
	// part of any other count.
	BreakMinutes int
	// CapBlocked is set when the day is over the cap and not acknowledged:
	// even confirmed items are not released for booking.
	CapBlocked bool
}

// Session is one review of one day. Every action re-derives the day.
type Session struct {
	st    Store
	cfg   config.Config
	day   civil.Date
	now   func() time.Time
	view  Day
	list  []Entry
	conf  map[itemKey]int
	acked bool

	issues     IssueSource
	fetched    bool
	candidates []youtrack.Issue
	pending    *RememberOffer

	// started identifies the session in the review log; corrections counts
	// what it changed so far.
	started     time.Time
	corrections int
	// logID is the session's review_log row, zero until first written.
	logID int64
	// confirmedHere holds the items this session confirmed itself.
	confirmedHere map[itemKey]struct{}
	// created lists the overrides this session stored and that are still in
	// place, oldest first; UndoLast takes the newest back.
	created []createdOverride
}

// createdOverride is an override this session stored, with what it added to
// the session's counters so that undoing it takes exactly that back.
type createdOverride struct {
	id       int64
	override event.Override
	// correction is set when it counted as a correction in the review log.
	correction bool
}

// NewSession derives the day and prepares its entries.
func NewSession(ctx context.Context, st Store, cfg config.Config, day civil.Date, now func() time.Time, opts ...Option) (*Session, error) {
	s := &Session{st: st, cfg: cfg, day: day, now: now, started: now()}
	for _, opt := range opts {
		opt(s)
	}
	if err := s.refresh(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Session) refresh(ctx context.Context) error {
	d, err := LoadDay(ctx, s.st, s.cfg, s.day)
	if err != nil {
		return err
	}
	conf, err := confirmedSet(ctx, s.st, s.day)
	if err != nil {
		return err
	}
	acked := false
	if d.OverCap {
		if acked, err = capAcknowledged(ctx, s.st, d); err != nil {
			return err
		}
	}
	itemsByTicket := map[string]sessionizer.DayItem{}
	for _, it := range d.Items {
		itemsByTicket[it.Ticket] = it
	}
	list := make([]Entry, 0, len(d.Blocks)+len(d.IdleGaps)+len(d.Breaks))
	for _, b := range d.Blocks {
		start, end := maxTime(b.Start, d.Start), minTime(b.End, d.End)
		e := Entry{
			Index: len(list) + 1, Kind: KindBlock, Start: start, End: end,
			Ticket: b.Ticket, Tier: b.Tier, Reason: b.Reason, Rule: b.Rule, Repo: b.Repo, Branch: b.Branch,
			Minutes: roundedMinutes(start, end),
		}
		if b.Ticket == "" {
			e.Kind = KindUnassigned
		} else if it, ok := itemsByTicket[b.Ticket]; ok {
			e.Confirmed = isConfirmed(conf, it)
		}
		list = append(list, e)
	}
	for _, g := range d.IdleGaps {
		start, end := maxTime(g.Start, d.Start), minTime(g.End, d.End)
		if m := roundedMinutes(start, end); m > 0 {
			list = append(list, Entry{Kind: KindIdle, Start: start, End: end, Minutes: m})
		}
	}
	for _, b := range d.Breaks {
		start, end := maxTime(b.Start, d.Start), minTime(b.End, d.End)
		if m := roundedMinutes(start, end); m > 0 {
			list = append(list, Entry{Kind: KindBreak, Start: start, End: end, Minutes: m, Idle: b.Idle})
		}
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Start.Before(list[j].Start) })
	for i := range list {
		list[i].Index = i + 1
	}
	s.view, s.list, s.conf, s.acked = d, list, conf, acked
	s.pending = nil
	return nil
}

// Entries returns the current numbered entries.
func (s *Session) Entries() []Entry { return append([]Entry(nil), s.list...) }

// Corrections is the number of corrections this session counts in the review
// log; an undone action no longer counts.
func (s *Session) Corrections() int { return s.corrections }

// Day returns the derived day behind the entries.
func (s *Session) Day() Day { return s.view }

// CapWarning describes an exceeded daily cap.
type CapWarning struct {
	TotalMinutes int
	CapMinutes   int
	Acknowledged bool
}

// CapWarning returns the cap warning, or nil when the day is within the cap.
func (s *Session) CapWarning() *CapWarning {
	if !s.view.OverCap {
		return nil
	}
	return &CapWarning{TotalMinutes: s.view.TotalMinutes, CapMinutes: s.view.CapMinutes, Acknowledged: s.acked}
}

func (s *Session) entry(n int) (Entry, error) {
	if n < 1 || n > len(s.list) {
		return Entry{}, fmt.Errorf("%w: %d", ErrNoSuchEntry, n)
	}
	return s.list[n-1], nil
}

func (s *Session) itemFor(ticket string) (sessionizer.DayItem, bool) {
	for _, it := range s.view.Items {
		if it.Ticket == ticket {
			return it, true
		}
	}
	return sessionizer.DayItem{}, false
}

// Confirm confirms the item (ticket of the entry, whole day) at its current
// minutes. Unassigned entries cannot be confirmed.
func (s *Session) Confirm(ctx context.Context, n int) error {
	e, err := s.entry(n)
	if err != nil {
		return err
	}
	switch e.Kind {
	case KindUnassigned:
		return ErrGapNotAttached
	case KindIdle, KindBreak:
		return ErrNotConfirmable
	}
	return s.confirmItems(ctx, []string{e.Ticket})
}

// ConfirmAll confirms every attributed, not yet confirmed item. Unassigned
// time is never confirmed. It returns the number of newly confirmed items.
func (s *Session) ConfirmAll(ctx context.Context) (int, error) {
	var tickets []string
	for _, it := range s.view.Items {
		if it.Ticket == "" || isConfirmed(s.conf, it) {
			continue
		}
		tickets = append(tickets, it.Ticket)
	}
	if err := s.confirmItems(ctx, tickets); err != nil {
		return 0, err
	}
	return len(tickets), nil
}

// confirmItems confirms the items of tickets and logs the session in one
// transaction: either every confirmation and the log entry are stored, or
// none.
func (s *Session) confirmItems(ctx context.Context, tickets []string) error {
	confirmations := make([]store.Confirmation, 0, len(tickets))
	keys := make([]itemKey, 0, len(tickets))
	for _, ticket := range tickets {
		it, ok := s.itemFor(ticket)
		if !ok {
			return fmt.Errorf("%s: nothing to confirm (rounds to zero)", ticket)
		}
		confirmations = append(confirmations, store.Confirmation{
			Day: s.day.String(), Issue: it.Ticket, Type: itemType(it),
			Minutes: it.Minutes, ConfirmedAt: s.now(),
		})
		keys = append(keys, itemKey{it.Ticket, itemType(it)})
	}
	return s.apply(ctx, false, keys, nil, func(tx store.Tx) error {
		for _, c := range confirmations {
			if err := tx.Confirm(ctx, c); err != nil {
				return err
			}
		}
		return nil
	})
}

// NormalizeTicket upper-cases ticket and validates it against the configured
// ticket pattern (full match).
func (s *Session) NormalizeTicket(ticket string) (string, error) {
	return NormalizeTicket(s.cfg, ticket)
}

// NormalizeTicket upper-cases ticket and validates it against the configured
// ticket pattern (full match). The map command uses it before storing a
// learned rule.
func NormalizeTicket(cfg config.Config, ticket string) (string, error) {
	t := strings.ToUpper(strings.TrimSpace(ticket))
	pattern := cfg.TicketPattern
	if pattern == nil {
		pattern = regexp.MustCompile(config.DefaultTicketPattern)
	}
	re, err := regexp.Compile("^(?:" + pattern.String() + ")$")
	if err != nil || !re.MatchString(t) {
		return "", fmt.Errorf("%w: %q (pattern %s)", ErrInvalidTicket, ticket, pattern)
	}
	return t, nil
}

// ticketOverride attributes [start, e.End) of entry e to ticket. Time without
// presence (idle gaps, filled time, breaks in a gap) can only be given a
// ticket by a fill; everything else is a reassign.
func ticketOverride(e Entry, start time.Time, ticket string) event.Override {
	kind := event.OverrideReassign
	if e.inIdleTime() {
		kind = event.OverrideFill
	}
	return event.Override{Start: start, End: e.End, Ticket: ticket, Kind: kind}
}

// Reassign attributes the entry's interval to ticket by writing an override.
// It also resolves unassigned gaps. On an idle gap it fills the gap: time
// without presence that exists only because the user asked for it here.
func (s *Session) Reassign(ctx context.Context, n int, ticket string) error {
	e, err := s.entry(n)
	if err != nil {
		return err
	}
	t, err := s.NormalizeTicket(ticket)
	if err != nil {
		return err
	}
	if err := s.storeOverride(ctx, e.Kind == KindBlock, ticketOverride(e, e.Start, t)); err != nil {
		return err
	}
	if e.Repo != "" && learnedRuleWouldApply(e.Rule) {
		s.pending = &RememberOffer{Repo: e.Repo, Branch: e.Branch, Ticket: t}
	}
	return nil
}

// learnedRuleWouldApply reports whether a learned rule for the context of an
// entry decided by rule would change its attribution next time. That holds
// when nothing decided, or a learned rule or a standard task did: the resolver
// checks learned rules before both standard-task steps. A ticket on the event
// or in the branch, a repo rule, a manual switch and an override all rank
// before learned rules, so remembering would have no effect there.
func learnedRuleWouldApply(rule resolver.RuleKind) bool {
	switch rule {
	case resolver.RuleNone, resolver.RuleLearned, resolver.RuleProjectDefault, resolver.RuleGlobalDefault:
		return true
	default:
		return false
	}
}

// Discard drops the entry's interval from the day by writing a discard
// override. On an idle gap it dismisses the gap so it is not offered again.
func (s *Session) Discard(ctx context.Context, n int) error {
	e, err := s.entry(n)
	if err != nil {
		return err
	}
	return s.storeOverride(ctx, e.Kind == KindBlock, event.Override{Start: e.Start, End: e.End, Kind: event.OverrideDiscard})
}

// MarkBreak marks the entry as a break: a break override of its own kind, not
// a discard. Break time is never booked and shows as its own sum in the day
// summary.
func (s *Session) MarkBreak(ctx context.Context, n int) error {
	e, err := s.entry(n)
	if err != nil {
		return err
	}
	if e.Kind == KindBreak {
		return ErrAlreadyBreak
	}
	return s.storeOverride(ctx, e.Kind == KindBlock, event.Override{Start: e.Start, End: e.End, Kind: event.OverrideBreak})
}

// SplitAt resolves a local "HH:MM" on the session's day for entry n.
func (s *Session) SplitAt(n int, clock string) (time.Time, error) {
	e, err := s.entry(n)
	if err != nil {
		return time.Time{}, err
	}
	parsed, err := time.Parse("15:04", clock)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid time %q: expected HH:MM", clock)
	}
	loc := location(s.cfg)
	at := time.Date(s.day.Year, s.day.Month, s.day.Day, parsed.Hour(), parsed.Minute(), 0, 0, loc)
	if !at.After(e.Start) || !at.Before(e.End) {
		return time.Time{}, ErrInvalidSplit
	}
	return at, nil
}

// Split cuts entry n at the local time clock (HH:MM) and attributes the
// second part to ticket with an override. An empty ticket keeps the entry's
// current ticket for the second part, so it becomes a separate entry that
// can be reassigned afterwards. That is refused for an override block (it
// would merge straight back, changing nothing; ErrSplitSameTicket) and for a
// break (ErrBreakNotSplittable). On an idle gap the second part is filled.
func (s *Session) Split(ctx context.Context, n int, clock, ticket string) error {
	at, err := s.SplitAt(n, clock)
	if err != nil {
		return err
	}
	e := s.list[n-1]
	if e.Kind == KindBreak {
		return ErrBreakNotSplittable
	}
	target := e.Ticket
	if strings.TrimSpace(ticket) != "" {
		if target, err = s.NormalizeTicket(ticket); err != nil {
			return err
		}
	}
	if target == "" {
		// An unassigned gap split without a ticket yields two unassigned
		// halves; storing that as a correction would only break the streak.
		return ErrSplitNeedsTicket
	}
	if target == e.Ticket && (e.Rule == sessionizer.OverrideRule || e.Rule == sessionizer.FillRule) {
		return ErrSplitSameTicket
	}
	return s.storeOverride(ctx, e.Kind == KindBlock, ticketOverride(e, at, target))
}

// storeOverride stores o together with the review log entry and remembers it
// for UndoLast. correction says whether it changes time that had a ticket.
func (s *Session) storeOverride(ctx context.Context, correction bool, o event.Override) error {
	track := &createdOverride{override: o, correction: correction}
	return s.apply(ctx, correction, nil, track, func(tx store.Tx) error {
		id, err := tx.AddOverrideID(ctx, o)
		track.id = id
		return err
	})
}

// UndoLast takes back the override this session created last: it deletes it
// and removes what it added to the review log's correction count, so the
// session ends as if the action had never happened. Overrides of earlier
// sessions are never touched; with nothing to undo it returns
// ErrNothingToUndo and changes nothing. The learned rule a reassign may have
// led to is not part of the override and stays; remove it with `map rm`.
func (s *Session) UndoLast(ctx context.Context) (event.Override, error) {
	n := len(s.created)
	if n == 0 {
		return event.Override{}, ErrNothingToUndo
	}
	last := s.created[n-1]
	corrections := s.corrections
	if last.correction {
		corrections--
	}
	err := s.st.InTx(ctx, func(tx store.Tx) error {
		if err := tx.DeleteOverride(ctx, last.id); err != nil {
			return err
		}
		_, err := tx.SaveReviewRecord(ctx, s.reviewRecord(corrections, len(s.confirmedHere)))
		return err
	})
	if err != nil {
		return event.Override{}, err
	}
	s.created, s.corrections = s.created[:n-1], corrections
	return last.override, s.refresh(ctx)
}

// AcknowledgeCap records that the user accepts the current day total above
// the daily cap. The acknowledgement is only valid for exactly that total.
func (s *Session) AcknowledgeCap(ctx context.Context) error {
	if !s.view.OverCap {
		return ErrNoCapWarning
	}
	ack := store.CapAcknowledgement{Day: s.day.String(), TotalMinutes: s.view.TotalMinutes, AcknowledgedAt: s.now()}
	return s.apply(ctx, false, nil, nil, func(tx store.Tx) error { return tx.AcknowledgeCap(ctx, ack) })
}

// apply runs write and the session's review log entry in one transaction,
// then re-derives the day. A correction is a change to time that already had
// a ticket; filling or dropping unassigned time, confirmations (newly the
// keys given) and cap acknowledgements are not.
// Recording with every action means an aborted session still leaves what it
// did, and the single transaction means the log never lags behind the stored
// correction. The session's counters change only after the commit. A non-nil
// created is the override write stores; it is kept for UndoLast once the
// commit succeeded.
func (s *Session) apply(ctx context.Context, correction bool, confirmed []itemKey, created *createdOverride, write func(tx store.Tx) error) error {
	corrections := s.corrections
	if correction {
		corrections++
	}
	confirmedHere := make(map[itemKey]struct{}, len(s.confirmedHere)+len(confirmed))
	for k := range s.confirmedHere {
		confirmedHere[k] = struct{}{}
	}
	for _, k := range confirmed {
		confirmedHere[k] = struct{}{}
	}
	var logID int64
	err := s.st.InTx(ctx, func(tx store.Tx) error {
		if err := write(tx); err != nil {
			return err
		}
		var err error
		logID, err = tx.SaveReviewRecord(ctx, s.reviewRecord(corrections, len(confirmedHere)))
		return err
	})
	if err != nil {
		return err
	}
	s.corrections, s.confirmedHere, s.logID = corrections, confirmedHere, logID
	if created != nil {
		s.created = append(s.created, *created)
	}
	return s.refresh(ctx)
}

// reviewRecord is the session's own review log row: its corrections and the
// items it confirmed itself. Items confirmed in earlier sessions do not count.
func (s *Session) reviewRecord(corrections, confirmed int) store.ReviewRecord {
	return store.ReviewRecord{
		ID: s.logID, Day: s.day.String(), SessionStart: s.started, FinishedAt: s.now(),
		Corrections: corrections, Confirmed: confirmed,
	}
}

// Finish records the end of the session in the review log. Call it when the
// user ends the review, also when the input ended.
func (s *Session) Finish(ctx context.Context) error {
	return s.st.InTx(ctx, func(tx store.Tx) error {
		id, err := tx.SaveReviewRecord(ctx, s.reviewRecord(s.corrections, len(s.confirmedHere)))
		if err != nil {
			return err
		}
		s.logID = id
		return nil
	})
}

// Summary counts the current state of the day.
func (s *Session) Summary() Summary {
	sum := Summary{BreakMinutes: s.view.BreakMinutes}
	for _, c := range s.created {
		if c.override.Kind == event.OverrideDiscard {
			sum.Discarded++
		}
	}
	for _, it := range s.view.Items {
		switch {
		case it.Ticket == "":
			sum.Unassigned++
		case isConfirmed(s.conf, it):
			sum.Confirmed++
		default:
			sum.Unconfirmed++
		}
	}
	sum.CapBlocked = s.view.OverCap && !s.acked
	return sum
}

// roundedMinutes is the length of [start, end) in whole minutes, rounded to the
// nearest.
func roundedMinutes(start, end time.Time) int {
	return int((end.Sub(start) + 30*time.Second) / time.Minute)
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
