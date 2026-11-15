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

// Override is a user correction for a time range. It either assigns a ticket
// or discards the range.
type Override struct {
	Start   time.Time
	End     time.Time
	Ticket  string
	Discard bool
}
