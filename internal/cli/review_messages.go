package cli

import (
	"errors"
	"fmt"

	"github.com/JtheGunner/ticktack/internal/review"
	"github.com/JtheGunner/ticktack/internal/store"
	"github.com/JtheGunner/ticktack/internal/suggest"
)

// User-facing strings of the review command live here so a German variant can
// be added later.
const (
	msgReviewPrompt                 = "review> "
	msgReviewNoEntries              = "No time recorded for %s."
	msgReviewHelp                   = "Actions: c N confirm | t N TICKET reassign | u N K take suggestion K | r [repo] remember last reassignment | d N discard | b N break | s N HH:MM [TICKET] split | a acknowledge cap | y confirm all | q quit"
	msgReviewConfirmed              = "Confirmed %s."
	msgReviewConfirmAll             = "Confirmed %d item(s). Unassigned time is never confirmed."
	msgReviewReassigned             = "Entry %d assigned to %s."
	msgReviewDiscarded              = "Entry %d discarded."
	msgReviewBreak                  = "Entry %d marked as break (not booked)."
	msgReviewSplit                  = "Entry %d split at %s."
	msgReviewCapAcked               = "Daily cap warning acknowledged for this total."
	msgReviewSplitNeedsTicket       = "Error: unassigned time can only be split together with a ticket for the second part (s N HH:MM TICKET)."
	msgReviewSplitPrompt            = "Ticket for the second part (empty keeps %s): "
	msgReviewNextStep               = "Next step: run `ticktack sync` to book the confirmed items."
	msgReviewNothingYet             = "Nothing confirmed yet: `ticktack sync` will book nothing."
	msgReviewSuggestionNeedsConfirm = "A suggestion is only a proposal: confirm the entry (c N) to release it for booking."
	msgReviewStateConfirmed         = "confirmed"
	msgReviewStateNeedsTicket       = "needs ticket"
	msgColLength                    = "Length"
	msgColState                     = "State"
	msgReviewCapBlocked             = "Sync is blocked by the daily cap until you acknowledge it (a)."
)

func msgReviewUnknownCommand(line string) string {
	return fmt.Sprintf("Unknown command %q. %s", line, msgReviewHelp)
}

func msgReviewSuggestionsHeader(index int) string {
	return fmt.Sprintf("Suggestions for %d (assigned to you, In Progress; take one with u %d K):", index, index)
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
	return fmt.Sprintf("Summary: %d confirmed, %d discarded, %d unconfirmed, %d unassigned.",
		s.Confirmed, s.Discarded, s.Unconfirmed, s.Unassigned)
}

func msgReviewHeading(day string) string { return "Review for " + day }
