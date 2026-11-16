package cli

import (
	"fmt"
	"strings"

	"github.com/JtheGunner/ticktack/internal/auto"
)

// User-facing strings of the auto and digest commands live here so a German
// variant can be added later.
const (
	msgAutoLong = `Book the safe blocks of a day without a review, once auto mode has earned
trust. It is off by default and unlocks only when "[auto] enabled = true" is
set and the last N consecutive reviewed days (default 10, "[auto] trusted_days")
passed without a single correction. A later correction in "ticktack review"
locks it again.

Only blocks attributed at the explicit or rule tier are booked, and only when
the day total stays within the daily cap. An item with any lower-tier block is
skipped as a whole, and a day above the cap is not booked at all: both need a
human in "ticktack review". Bookings use the same idempotent ledger as
"ticktack sync", so "ticktack undo" reverts the last auto run.

There is no background scheduler: run this command by hand or schedule it
yourself (for example from cron or launchd). After every run a digest lists
every item, and "ticktack digest" shows it again.`

	msgDigestLong = `Show the digest of the most recent auto run: every booked, updated and
skipped item with ticket, date, minutes, tier and reason.`

	msgAutoLockedDisabled = "Auto mode is disabled: set \"enabled = true\" in the [auto] section of %s to opt in."
	msgAutoLockedStreak   = "Auto mode is locked: %d of %d required consecutive reviewed days without a correction."
	msgAutoLockedNothing  = "Nothing was booked."
	msgAutoLockedProceed  = "To build the track record, run `ticktack review` each day and confirm without corrections."
	msgAutoNothingToBook  = "Nothing to book automatically for %s."

	msgDigestNone = "No digest yet: `ticktack auto` has not booked anything."

	msgDigestUndoHint = "Undo: run `ticktack undo` to revert this run."
)

func msgDigestTitle(d auto.Digest) string {
	title := "Auto digest for " + d.Day.String()
	if d.DryRun {
		title += " (dry run, nothing booked)"
	}
	return title
}

func msgDigestLine(e auto.DigestEntry) string {
	tier := string(e.Tier)
	if tier == "" {
		tier = "-"
	}
	line := fmt.Sprintf("%-12s %-10s %-12s %4d min  tier %-9s", e.Status, SanitizeForTerminal(e.Ticket), e.Date, e.Minutes, tier)
	if e.Reason != "" {
		line += "  " + SanitizeForTerminal(e.Reason)
	}
	return strings.TrimRight(line, " ")
}

func msgDigestCap(d auto.Digest) string {
	return fmt.Sprintf("Day total %s exceeds the daily cap of %s: nothing was booked, review the day with `ticktack review`.",
		formatMinutes(d.TotalMinutes), formatMinutes(d.CapMinutes))
}

func msgDigestTotals(d auto.Digest) string {
	return fmt.Sprintf("Totals: created %d, updated %d, recovered %d, skipped %d, failed %d, not sent %d; %s booked.",
		d.Created, d.Updated, d.Recovered, d.Skipped, d.Failed, d.NotSent, formatMinutes(d.BookedMinutes))
}

// renderDigest formats the digest text that is printed and stored. External
// text (ticket keys, reasons, server errors) is sanitised line by line.
func renderDigest(d auto.Digest) string {
	lines := []string{msgDigestTitle(d)}
	for _, e := range d.Entries {
		lines = append(lines, msgDigestLine(e))
	}
	if d.CapExceeded {
		lines = append(lines, msgDigestCap(d))
	}
	lines = append(lines, msgDigestTotals(d))
	if d.Changed() && !d.DryRun {
		lines = append(lines, msgDigestUndoHint)
	}
	return strings.Join(lines, "\n")
}
