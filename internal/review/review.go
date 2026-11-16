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
	"regexp"
	"strings"
	"time"

	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/resolver"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
	"github.com/JtheGunner/ticktack/internal/store"
	"github.com/JtheGunner/ticktack/internal/youtrack"
)

// Reader is the read side of the store needed to derive a day.
type Reader interface {
	Events(ctx context.Context, from, to time.Time) ([]event.Event, error)
	Overrides(ctx context.Context, from, to time.Time) ([]event.Override, error)
	LearnedRules(ctx context.Context) ([]store.LearnedRule, error)
}

// Store is everything review needs from the persistence layer.
type Store interface {
	Reader
	AddOverride(ctx context.Context, o event.Override) error
	AddLearnedRule(ctx context.Context, r store.LearnedRule) (store.LearnedRule, error)
	UpdateLearnedRule(ctx context.Context, r store.LearnedRule) error
	Confirm(ctx context.Context, c store.Confirmation) error
	Confirmations(ctx context.Context, day string) ([]store.Confirmation, error)
	AcknowledgeCap(ctx context.Context, a store.CapAcknowledgement) error
	CapAcknowledgementFor(ctx context.Context, day string) (store.CapAcknowledgement, bool, error)
}

// Errors returned by session actions.
var (
	ErrNoSuchEntry    = errors.New("no such entry")
	ErrGapNotAttached = errors.New("unassigned time cannot be confirmed: assign a ticket first")
	ErrInvalidTicket  = errors.New("invalid ticket ID")
	ErrInvalidSplit   = errors.New("split time must lie strictly inside the entry")
	ErrNoCapWarning   = errors.New("the day is not over the daily cap")
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
	return rules
}

// LoadRules returns the configured rules plus the learned rules stored in r.
// It is the single loading point shared by today, status, review and sync, so
// every command sees the same attribution.
func LoadRules(ctx context.Context, r interface {
	LearnedRules(ctx context.Context) ([]store.LearnedRule, error)
}, cfg config.Config) (resolver.Rules, error) {
	rules := RulesFrom(cfg)
	learned, err := r.LearnedRules(ctx)
	if err != nil {
		return resolver.Rules{}, err
	}
	for _, l := range learned {
		rules.Learned = append(rules.Learned, resolver.LearnedRule{Repo: l.Repo, Branch: l.BranchPattern, Ticket: l.Ticket})
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
}

func location(cfg config.Config) *time.Location {
	if cfg.Location == nil {
		return time.Local
	}
	return cfg.Location
}

// LoadDay derives the blocks and aggregated items of one local day.
func LoadDay(ctx context.Context, r Reader, cfg config.Config, day civil.Date) (Day, error) {
	loc := location(cfg)
	from := time.Date(day.Year, day.Month, day.Day, 0, 0, 0, 0, loc)
	to := time.Date(day.Year, day.Month, day.Day+1, 0, 0, 0, 0, loc)
	events, err := r.Events(ctx, from, to)
	if err != nil {
		return Day{}, err
	}
	overrides, err := r.Overrides(ctx, from, to)
	if err != nil {
		return Day{}, err
	}
	rules, err := LoadRules(ctx, r, cfg)
	if err != nil {
		return Day{}, err
	}
	blocks := sessionizer.Derive(events, overrides, sessionizer.ConfigFrom(cfg), rules)
	rc := sessionizer.RoundingConfigFrom(cfg)
	d := Day{Date: day, Start: from, End: to, CapMinutes: int(rc.DailyCap / time.Minute)}
	for _, b := range blocks {
		if b.End.After(from) && b.Start.Before(to) {
			d.Blocks = append(d.Blocks, b)
		}
	}
	for _, it := range sessionizer.Aggregate(blocks, rc, loc) {
		if it.Date != day {
			continue
		}
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

// EntryKind distinguishes attributed blocks from unassigned gaps.
type EntryKind int

// Entry kinds.
const (
	KindBlock EntryKind = iota
	KindGap
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
	// Repo and Branch are the context the entry was derived from; empty for
	// override entries and when no context existed.
	Repo, Branch string
	// Minutes is the length of the entry, rounded to whole minutes.
	Minutes int
	// Confirmed is set when the entry's item is confirmed at its current
	// minutes. It is never set for gaps.
	Confirmed bool
}

// Summary counts the outcome of a review session.
type Summary struct {
	Confirmed   int
	Discarded   int
	Unconfirmed int
	// Unassigned counts unassigned items that still need a ticket.
	Unassigned int
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

	discarded int
}

// NewSession derives the day and prepares its entries.
func NewSession(ctx context.Context, st Store, cfg config.Config, day civil.Date, now func() time.Time, opts ...Option) (*Session, error) {
	s := &Session{st: st, cfg: cfg, day: day, now: now}
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
	list := make([]Entry, 0, len(d.Blocks))
	for _, b := range d.Blocks {
		start, end := maxTime(b.Start, d.Start), minTime(b.End, d.End)
		e := Entry{
			Index: len(list) + 1, Kind: KindBlock, Start: start, End: end,
			Ticket: b.Ticket, Tier: b.Tier, Reason: b.Reason, Repo: b.Repo, Branch: b.Branch,
			Minutes: int((end.Sub(start) + 30*time.Second) / time.Minute),
		}
		if b.Ticket == "" {
			e.Kind = KindGap
		} else if it, ok := itemsByTicket[b.Ticket]; ok {
			e.Confirmed = isConfirmed(conf, it)
		}
		list = append(list, e)
	}
	s.view, s.list, s.conf, s.acked = d, list, conf, acked
	s.pending = nil
	return nil
}

// Entries returns the current numbered entries.
func (s *Session) Entries() []Entry { return append([]Entry(nil), s.list...) }

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
	if e.Kind == KindGap {
		return ErrGapNotAttached
	}
	if err := s.confirmItem(ctx, e.Ticket); err != nil {
		return err
	}
	return s.refresh(ctx)
}

// ConfirmAll confirms every attributed, not yet confirmed item. Unassigned
// time is never confirmed. It returns the number of newly confirmed items.
func (s *Session) ConfirmAll(ctx context.Context) (int, error) {
	count := 0
	for _, it := range s.view.Items {
		if it.Ticket == "" || isConfirmed(s.conf, it) {
			continue
		}
		if err := s.confirmItem(ctx, it.Ticket); err != nil {
			return count, err
		}
		count++
	}
	return count, s.refresh(ctx)
}

func (s *Session) confirmItem(ctx context.Context, ticket string) error {
	it, ok := s.itemFor(ticket)
	if !ok {
		return fmt.Errorf("%s: nothing to confirm (rounds to zero)", ticket)
	}
	return s.st.Confirm(ctx, store.Confirmation{
		Day: s.day.String(), Issue: it.Ticket, Type: itemType(it),
		Minutes: it.Minutes, ConfirmedAt: s.now(),
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

// Reassign attributes the entry's interval to ticket by writing an override.
// It also resolves unassigned gaps.
func (s *Session) Reassign(ctx context.Context, n int, ticket string) error {
	e, err := s.entry(n)
	if err != nil {
		return err
	}
	t, err := s.NormalizeTicket(ticket)
	if err != nil {
		return err
	}
	if err := s.st.AddOverride(ctx, event.Override{Start: e.Start, End: e.End, Ticket: t}); err != nil {
		return err
	}
	if err := s.refresh(ctx); err != nil {
		return err
	}
	if e.Repo != "" && (e.Tier == resolver.TierUnknown || e.Tier == resolver.TierLearned) {
		s.pending = &RememberOffer{Repo: e.Repo, Branch: e.Branch, Ticket: t}
	}
	return nil
}

// Discard drops the entry's interval from the day by writing a discard
// override. The override model has no separate break flag, so marking time as
// a break is a discard (see MarkBreak).
func (s *Session) Discard(ctx context.Context, n int) error {
	e, err := s.entry(n)
	if err != nil {
		return err
	}
	if err := s.st.AddOverride(ctx, event.Override{Start: e.Start, End: e.End, Discard: true}); err != nil {
		return err
	}
	s.discarded++
	return s.refresh(ctx)
}

// MarkBreak marks the entry as a break. A break is stored exactly like a
// discard: event.Override carries no extra field, and break time is simply
// not booked.
func (s *Session) MarkBreak(ctx context.Context, n int) error { return s.Discard(ctx, n) }

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
// can be reassigned afterwards.
func (s *Session) Split(ctx context.Context, n int, clock, ticket string) error {
	at, err := s.SplitAt(n, clock)
	if err != nil {
		return err
	}
	e := s.list[n-1]
	target := e.Ticket
	if strings.TrimSpace(ticket) != "" {
		if target, err = s.NormalizeTicket(ticket); err != nil {
			return err
		}
	}
	if err := s.st.AddOverride(ctx, event.Override{Start: at, End: e.End, Ticket: target}); err != nil {
		return err
	}
	return s.refresh(ctx)
}

// AcknowledgeCap records that the user accepts the current day total above
// the daily cap. The acknowledgement is only valid for exactly that total.
func (s *Session) AcknowledgeCap(ctx context.Context) error {
	if !s.view.OverCap {
		return ErrNoCapWarning
	}
	err := s.st.AcknowledgeCap(ctx, store.CapAcknowledgement{
		Day: s.day.String(), TotalMinutes: s.view.TotalMinutes, AcknowledgedAt: s.now(),
	})
	if err != nil {
		return err
	}
	return s.refresh(ctx)
}

// Summary counts the current state of the day.
func (s *Session) Summary() Summary {
	sum := Summary{Discarded: s.discarded}
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
