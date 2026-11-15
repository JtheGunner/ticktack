package cli

import (
	"fmt"
	"time"
)

// User-facing strings of the daily commands live here so a German variant can
// be added later.
const (
	msgNothingRecorded = "Nothing recorded yet. Open a shell prompt in a git repository to start."
	msgCapturePaused   = "Capture:   paused (run `ticktack resume` to continue)"
	msgCaptureActive   = "Capture:   active"
	msgNoPresence      = "Presence:  never seen"
	msgPaused          = "Capture paused."
	msgResumed         = "Capture resumed."
	msgSwitchCleared   = "Back to automatic ticket detection."
	msgNoBlocks        = "No time recorded for %s."
	msgPurgePrompt     = "Delete ALL local ticktack data (events, overrides, ledger)? [y/N] "
	msgPurgeAborted    = "Aborted. Nothing was deleted."
	msgPurged          = "All local ticktack data deleted."
	msgNotInteractive  = "cannot ask for confirmation: stdin is not interactive; pass --yes to purge"
	msgUnassigned      = "(unassigned)"
	msgNoRepo          = "(none)"
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
	return fmt.Sprintf("Presence:  last seen %s (%s ago)", at.In(loc).Format("2006-01-02 15:04:05"), ago)
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
