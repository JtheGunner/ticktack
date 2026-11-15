package cli

import (
	"fmt"
	"time"

	"github.com/JtheGunner/ticktack/internal/review"
)

// User-facing strings of the review command live here so a German variant can
// be added later.
const (
	msgReviewPrompt      = "review> "
	msgReviewNoEntries   = "No time recorded for %s."
	msgReviewHelp        = "Actions: c N confirm | t N TICKET reassign | d N discard | b N break | s N HH:MM [TICKET] split | a acknowledge cap | y confirm all | q quit"
	msgReviewConfirmed   = "Confirmed %s."
	msgReviewConfirmAll  = "Confirmed %d item(s). Unassigned time is never confirmed."
	msgReviewReassigned  = "Entry %d assigned to %s."
	msgReviewDiscarded   = "Entry %d discarded."
	msgReviewBreak       = "Entry %d marked as break (not booked)."
	msgReviewSplit       = "Entry %d split at %s."
	msgReviewCapAcked    = "Daily cap warning acknowledged for this total."
	msgReviewSplitPrompt = "Ticket for the second part (empty keeps %s): "
	msgReviewNextStep    = "Next step: run `ticktack sync` to book the confirmed items."
	msgReviewNothingYet  = "Nothing confirmed yet: `ticktack sync` will book nothing."
	msgReviewCapBlocked  = "Sync is blocked by the daily cap until you acknowledge it (a)."
)

func msgReviewUnknownCommand(line string) string {
	return fmt.Sprintf("Unknown command %q. %s", line, msgReviewHelp)
}

func msgReviewUsage(usage string) string { return "Usage: " + usage }

func msgReviewError(err error) string { return "Error: " + err.Error() }

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

func msgReviewHeader(day string) string { return "Review for " + day + ":" }

func msgReviewEntry(e review.Entry, loc *time.Location) string {
	ticket, status := e.Ticket, ""
	switch {
	case e.Kind == review.KindGap:
		ticket, status = msgUnassigned, "  needs a ticket (t N TICKET)"
	case e.Confirmed:
		status = "  [confirmed]"
	}
	return fmt.Sprintf("%3d  %s-%s  %6s  %-12s %-9s %s%s",
		e.Index, e.Start.In(loc).Format("15:04"), e.End.In(loc).Format("15:04"),
		formatMinutes(e.Minutes), ticket, e.Tier, e.Reason, status)
}
