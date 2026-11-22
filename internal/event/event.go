// Package event defines the append-only signals and user overrides that
// ticktack derives work blocks from.
package event

import "time"

// Kind distinguishes the two independent signal families.
type Kind string

const (
	// KindPresence marks events that say the user was at the machine.
	KindPresence Kind = "presence"
	// KindContext marks events that say what the user was working on.
	KindContext Kind = "context"
	// KindGap marks a hole in the presence signal, for example because the
	// machine slept. At is the time the agent resumed and IdleSeconds is the
	// gap length in seconds (rounded), so the gap started at At minus
	// IdleSeconds. No presence samples exist inside a gap; the sessionizer
	// must not bridge it.
	KindGap Kind = "gap"
)

// Event is a single immutable signal. It never carries window titles,
// screenshots, keystrokes or file contents.
type Event struct {
	ID          int64
	At          time.Time
	Source      string
	Kind        Kind
	Repo        string
	Branch      string
	Ticket      string
	App         string
	IdleSeconds int
	// Key is an optional idempotency key. When non-empty, the store keeps at
	// most one event per key.
	Key string
}

// OverrideKind says what a user correction does with its time range.
type OverrideKind string

// Override kinds.
const (
	// OverrideReassign attributes presence time to a ticket.
	OverrideReassign OverrideKind = "reassign"
	// OverrideDiscard drops the time from the day.
	OverrideDiscard OverrideKind = "discard"
	// OverrideBreak drops the time from the day like a discard, but is kept
	// apart from it so the day summary can show breaks as their own sum.
	OverrideBreak OverrideKind = "break"
	// OverrideFill gives idle time (a gap between presence spans) to a ticket.
	// It is created only by an explicit action in review.
	OverrideFill OverrideKind = "fill"
)

// Override is a user correction for a time range. Depending on Kind it assigns
// a ticket to presence time, drops the range (discard or break) or fills idle
// time with a ticket.
type Override struct {
	// ID is the row id; zero until stored.
	ID    int64
	Start time.Time
	End   time.Time
	// Ticket is the target of a reassign or fill.
	Ticket string
	// Discard reports that the range is dropped (kinds discard and break). It
	// is the legacy way to say "discard" when Kind is empty.
	Discard bool
	// Kind is the correction kind; empty derives it from Discard.
	Kind OverrideKind
}

// EffectiveKind returns Kind, or the kind implied by Discard when Kind is
// empty.
func (o Override) EffectiveKind() OverrideKind {
	switch {
	case o.Kind != "":
		return o.Kind
	case o.Discard:
		return OverrideDiscard
	default:
		return OverrideReassign
	}
}

// Drops reports whether the override removes its range from the booked time.
func (o Override) Drops() bool {
	k := o.EffectiveKind()
	return k == OverrideDiscard || k == OverrideBreak
}
