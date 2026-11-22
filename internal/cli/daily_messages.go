package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/JtheGunner/ticktack/internal/sessionizer"
)

// User-facing strings of the daily commands live here so a German variant can
// be added later.
const (
	msgNothingRecorded = "Nothing recorded yet. Open a shell prompt in a git repository to start."
	msgCapturePaused   = "Capture paused (run `ticktack resume` to continue)"
	msgCaptureActive   = "Capture active"
	msgNoPresence      = "Presence never seen"
	msgPaused          = "Capture paused."
	msgResumed         = "Capture resumed."
	msgSwitchCleared   = "Back to automatic ticket detection."
	msgNoBlocks        = "No time recorded for %s."
	// msgPurgePrompt names everything purge deletes; a test checks it against
	// the tables of the schema.
	msgPurgePrompt = "Delete ALL local ticktack data? This empties the database: events, overrides, " +
		"the booking ledger and sync history, review confirmations, cap acknowledgements, " +
		"learned rules and their change log, the review log, digests, the cached YouTrack project list, " +
		"standard tasks and the consent to create them. [y/N] "
	msgPurgeAborted = "Aborted. Nothing was deleted."
	msgPurged       = "All local ticktack data deleted."
	// msgNotInteractive is shared by every command that asks before deleting.
	msgNotInteractive = "cannot ask for confirmation: stdin is not interactive; pass --yes to proceed"
	msgUnassigned     = "(unassigned)"
	// msgStandardTaskNew labels the placeholder of a standard task that is
	// created in YouTrack by the next sync.
	msgStandardTaskNew = "%s · standard task (new)"
	msgNoRepo          = "(none)"
	msgStatusHeading   = "Status"
	msgLabelRepo       = "Repo"
	msgLabelBranch     = "Branch"
	msgLabelTicket     = "Ticket"
	msgLabelTier       = "Tier"
	msgLabelReason     = "Reason"
	msgTotalsHeading   = "Totals"
	msgColBlock        = "#"
	msgColTime         = "Time"
	msgColTicket       = "Ticket"
	msgColTier         = "Tier"
	msgColReason       = "Reason"
	msgColRaw          = "Raw"
	msgColCertainty    = "Certainty"
	msgLabelTotal      = "Total"
)

func msgSwitched(ticket string) string { return "Switched to " + ticket + "." }

func msgInvalidTicket(arg, pattern string) error {
	return fmt.Errorf("%q is not a valid ticket ID (pattern %s)", arg, pattern)
}

func msgPresenceSeen(at, now time.Time, loc *time.Location) string {
	ago := now.Sub(at).Round(time.Second)
	if ago < 0 {
		ago = 0
	}
	return fmt.Sprintf("Presence last seen %s (%s ago)", at.In(loc).Format("2006-01-02 15:04:05"), ago)
}

// formatCertainty is the compact breakdown of an item's minutes by tier, for
// example "rule 40m, suggested 50m (50m carried)". Tiers without minutes are
// left out; carried time is the part of suggested that is stale context.
func formatCertainty(c sessionizer.Certainty) string {
	var parts []string
	add := func(tier string, minutes int) {
		if minutes > 0 {
			parts = append(parts, tier+" "+formatMinutes(minutes))
		}
	}
	add("explicit", c.Explicit)
	add("rule", c.Rule)
	add("learned", c.Learned)
	add("suggested", c.Suggested)
	if c.Suggested > 0 && c.Carried > 0 {
		last := len(parts) - 1
		parts[last] += " (" + formatMinutes(c.Carried) + " carried)"
	}
	add("unknown", c.Unknown)
	return strings.Join(parts, ", ")
}

func formatMinutes(minutes int) string {
	h, m := minutes/60, minutes%60
	switch {
	case h == 0:
		return fmt.Sprintf("%dm", m)
	case m == 0:
		return fmt.Sprintf("%dh", h)
	default:
		return fmt.Sprintf("%dh%02dm", h, m)
	}
}

func msgTodayHeading(day string) string { return "Blocks for " + day }

func msgOverDailyCap(dailyCap time.Duration) string {
	return fmt.Sprintf("OVER DAILY CAP (%s)", dailyCap)
}
