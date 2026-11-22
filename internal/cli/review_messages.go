package cli

import (
	"errors"
	"fmt"

	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/review"
	"github.com/JtheGunner/ticktack/internal/store"
	"github.com/JtheGunner/ticktack/internal/suggest"
)

// User-facing strings of the review command live here so a German variant can
// be added later.
const (
	msgReviewPrompt                 = "review> "
	msgReviewNoEntries              = "No time recorded for %s."
	msgReviewProjectsStale          = "Could not refresh the YouTrack project list, using the cached one: %s"
	msgReviewHelpTitle              = "Actions"
	msgReviewConfirmed              = "Confirmed %s."
	msgReviewConfirmAll             = "Confirmed %d item(s). Unassigned time is never confirmed."
	msgReviewReassigned             = "Entry %d assigned to %s."
	msgReviewDiscarded              = "Entry %d discarded."
	msgReviewBreak                  = "Entry %d marked as break (not booked)."
	msgReviewSplit                  = "Entry %d split at %s."
	msgReviewCapAcked               = "Daily cap warning acknowledged for this total."
	msgReviewSplitNeedsTicket       = "Error: unassigned time can only be split together with a ticket for the second part (s <block> <time> <ticket>)."
	msgReviewSplitPrompt            = "Ticket for the second part (empty keeps %s): "
	msgReviewNextStep               = "Next step: run `ticktack sync` to book the confirmed items."
	msgReviewNothingYet             = "Nothing confirmed yet: `ticktack sync` will book nothing."
	msgReviewSuggestionNeedsConfirm = "A suggestion is only a proposal: confirm the entry (c <block>) to release it for booking."
	msgReviewStateConfirmed         = "confirmed"
	msgReviewStateNeedsTicket       = "needs ticket"
	msgReviewStateIdle              = "idle gap"
	msgReviewStateBreak             = "break"
	msgReviewNoTicket               = "-"
	msgReviewIdleReason             = "no activity: assign a ticket (t), break (b) or discard (d)"
	msgReviewBreakReason            = "marked as break, never booked"
	msgReviewSplitCancelled         = "Split cancelled: no answer to the ticket question, nothing was changed."
	msgReviewNothingToUndo          = "Nothing to undo: this session has not changed anything (or everything is undone already)."
	msgColLength                    = "Length"
	msgColState                     = "State"
	msgReviewCapBlocked             = "Sync is blocked by the daily cap until you acknowledge it (a)."
)

func msgReviewUnknownCommand(line string) string {
	return fmt.Sprintf("Unknown command %q.", line)
}

// User-facing strings of the review help.
const (
	msgReviewHelpPlaceholders = "Placeholders"
	msgReviewHelpScreenTitle  = "Help"
)

// reviewCommand is one action of the review prompt as the help shows it.
type reviewCommand struct {
	// Key is what the user types.
	Key string
	// Args are the placeholders that follow the key.
	Args string
	// Verb is the one word shown in the key bar; empty keeps the command out
	// of the bar.
	Verb string
	// Description says what the command does, for the help screen.
	Description string
	// Example is a concrete input, for the help screen.
	Example string
}

// reviewCommandGroup bundles the actions that serve one purpose.
type reviewCommandGroup struct {
	Name     string
	Commands []reviewCommand
}

// reviewHelpGroups is the content of the review help, in display order.
var reviewHelpGroups = []reviewCommandGroup{
	{Name: "Assign", Commands: []reviewCommand{
		{Key: "t", Args: "<block> <ticket>", Verb: "assign", Description: "Assign or fill idle time", Example: "t 3 ABC-12"},
		{Key: "u", Args: "<block> <n>", Description: "Take suggestion <n>", Example: "u 3 2"},
		{Key: "r", Args: "[repo]", Description: "Remember last assignment", Example: "r"},
	}},
	{Name: "Confirm", Commands: []reviewCommand{
		{Key: "c", Args: "<block>", Verb: "confirm", Description: "Confirm a block", Example: "c 3"},
		{Key: "y", Verb: "confirm all", Description: "Confirm all assigned blocks", Example: "y"},
	}},
	{Name: "Adjust", Commands: []reviewCommand{
		{Key: "s", Args: "<block> <time> [<ticket>]", Verb: "split", Description: "Split a block at a time", Example: "s 3 09:30 ABC-12"},
		{Key: "b", Args: "<block>", Verb: "break", Description: "Mark block or idle as break", Example: "b 3"},
		{Key: "d", Args: "<block>", Verb: "discard", Description: "Discard a block", Example: "d 3"},
		{Key: "z", Verb: "undo", Description: "Undo last action", Example: "z"},
	}},
	{Name: "Other", Commands: []reviewCommand{
		{Key: "a", Description: "Acknowledge daily cap", Example: "a"},
		{Key: "q", Verb: "quit", Description: "Quit", Example: "q"},
		{Key: "?", Verb: "help", Description: "Show this help (also: help)", Example: "?"},
	}},
}

// reviewKeyBarKeys are the commands of the key bar, in display order.
var reviewKeyBarKeys = []string{"c", "t", "s", "d", "b", "z", "y", "?", "q"}

// reviewPlaceholders explains every placeholder the help uses.
var reviewPlaceholders = []struct{ Name, Meaning string }{
	{"<block>", "number from the # column"},
	{"<ticket>", "ticket key, e.g. ABC-12"},
	{"<time>", "time of day, HH:MM"},
	{"<n>", "suggestion number"},
	{"[repo]", "optional: whole repository"},
}

// reviewCommandByKey returns the documented command for key; the zero value
// when there is none.
func reviewCommandByKey(key string) reviewCommand {
	for _, g := range reviewHelpGroups {
		for _, c := range g.Commands {
			if c.Key == key {
				return c
			}
		}
	}
	return reviewCommand{}
}

// reviewUsage returns the plain "key args" text of a command.
func reviewUsage(key string) string {
	c := reviewCommandByKey(key)
	if c.Args == "" {
		return c.Key
	}
	return c.Key + " " + c.Args
}

func msgReviewSuggestionsHeader(index int) string {
	return fmt.Sprintf("Suggestions for %d (assigned to you, In Progress; take one with u %d <n>):", index, index)
}

func msgReviewSuggestion(k int, c suggest.Candidate) string {
	return fmt.Sprintf("     %d) %s  %s", k, SanitizeForTerminal(c.Key), SanitizeForTerminal(c.Summary))
}

func msgReviewRememberOffer(o review.RememberOffer) string {
	if o.Branch == "" {
		return fmt.Sprintf("Remember %s for %s? Type r to write the rule.", SanitizeForTerminal(o.Ticket), SanitizeForTerminal(o.Repo))
	}
	return fmt.Sprintf("Remember %s for %s? Type r for branch %q only, or r repo for the whole repository.",
		SanitizeForTerminal(o.Ticket), SanitizeForTerminal(o.Repo), SanitizeForTerminal(o.Branch))
}

func msgReviewRemembered(r store.LearnedRule) string {
	branch := SanitizeForTerminal(r.BranchPattern)
	if branch == "" {
		branch = "(any branch)"
	}
	return fmt.Sprintf("Learned rule #%d: repo %s, branch %s -> %s (visible and editable; tier learned).",
		r.ID, SanitizeForTerminal(r.Repo), branch, SanitizeForTerminal(r.Ticket))
}

func msgReviewUsage(usage string) string { return "Usage: " + usage }

func msgReviewError(err error) string {
	if errors.Is(err, review.ErrSplitNeedsTicket) {
		return msgReviewSplitNeedsTicket
	}
	return "Error: " + SanitizeForTerminal(err.Error())
}

func msgReviewCapWarning(w review.CapWarning) string {
	state := "NOT acknowledged: sync is blocked"
	if w.Acknowledged {
		state = "acknowledged"
	}
	return fmt.Sprintf("WARNING: day total %s exceeds the daily cap of %s (%s).",
		formatMinutes(w.TotalMinutes), formatMinutes(w.CapMinutes), state)
}

func msgReviewSummary(s review.Summary) string {
	text := fmt.Sprintf("Summary: %d confirmed, %d discarded, %d unconfirmed, %d unassigned",
		s.Confirmed, s.Discarded, s.Unconfirmed, s.Unassigned)
	if s.BreakMinutes > 0 {
		text += fmt.Sprintf(", %s of breaks (not booked)", formatMinutes(s.BreakMinutes))
	}
	return text + "."
}

// msgReviewBreakTotal is the day's own break sum, kept apart from the booked
// totals.
func msgReviewBreakTotal(minutes int) string {
	return fmt.Sprintf("Breaks: %s (never booked, not part of the totals).", formatMinutes(minutes))
}

// msgReviewUndone reports an undone override; corrections is what the review
// log counts for the session afterwards.
func msgReviewUndone(kind event.OverrideKind, ticket, from, to string, corrections int) string {
	var what string
	switch kind {
	case event.OverrideFill:
		what = "fill of " + from + "-" + to + " with " + ticket
	case event.OverrideBreak:
		what = "break " + from + "-" + to
	case event.OverrideDiscard:
		what = "discard of " + from + "-" + to
	default:
		what = "assignment of " + from + "-" + to + " to " + ticket
	}
	return fmt.Sprintf("Undid the last action: %s. The review log counts %d correction(s) for this session.", what, corrections)
}

func msgReviewHeading(day string) string { return "Review for " + day }
