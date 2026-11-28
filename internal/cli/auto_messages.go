package cli

import (
	"fmt"
	"strings"

	"github.com/JtheGunner/ticktack/internal/auto"
	"github.com/JtheGunner/ticktack/internal/render"
)

// User-facing strings of the auto and digest commands live here so a German
// variant can be added later.
const (
	msgAutoLong = `Book the safe blocks of a day without a review, once auto mode has earned
trust. It is off by default and unlocks only when "[auto] enabled = true" is
set and the last N consecutive reviewed days (default 10, "[auto] trusted_days")
passed without a single correction. A later correction locks it again.

A day counts towards that streak only when every item that has a ticket was
confirmed with the minutes it has now: an item whose minutes changed after you
confirmed it has to be confirmed again. Unassigned time may stay open. The
confirmations must be made on the same calendar day or the next one (in your
configured time zone). Reviews made later with "ticktack review --day" stay
valid for booking but do not count.

A correction ends the streak: reassigning, discarding, splitting or marking as
a break time that already had a ticket, and every "ticktack map add", "edit"
or "rm" (on the day of the change). Giving unassigned time a ticket is not a
correction, and neither is "ticktack switch".

Only blocks attributed at the explicit or rule tier are booked, and only when
the day total stays within the daily cap. An item with any lower-tier block is
skipped as a whole, and a day above the cap is not booked at all: both need a
human in "ticktack review". Bookings use the same idempotent ledger as
"ticktack sync", so "ticktack undo" reverts the last auto run.

A work item booked earlier is deleted when the day no longer has any time for
its ticket. Bookings of items auto mode merely skips are left alone.

In the default gated mode there is no background scheduler: run this command by
hand or schedule it yourself (for example from cron or launchd). After every
run a digest lists every item, and "ticktack digest" shows it again.

Autopilot: with "[auto] enabled = true" and "mode = "autopilot"" the trust gate
does not apply and the agent runs this booking itself every 30 minutes, for
every completed day (before today) of the last "autopilot_lookback_days" days
(default 7), oldest first. Today is never booked. A day you confirmed or
corrected in "ticktack review" is yours and is left alone. Run by hand in
autopilot mode, this command does one such pass; with --day it books that one
completed day. "ticktack undo" reverts the last run and holds its completed
days: the autopilot skips a held day until you book it again with
"ticktack auto --day YYYY-MM-DD", which lifts the hold, or confirm it in review.`

	msgDigestLong = `Show the digest of the most recent auto run: every booked, updated, deleted
and skipped item with ticket, date, minutes, tier and reason.`

	msgAutoLockedDisabled = "Auto mode is disabled: set \"enabled = true\" in the [auto] section of %s to opt in."
	msgAutoLockedStreak   = "Auto mode is locked: %d of %d required consecutive reviewed days without a correction."
	msgAutoLockedNothing  = "Nothing was booked."
	msgAutoLockedProceed  = "To build the track record, run `ticktack review` each day and confirm without corrections."
	msgAutoNothingToBook  = "Nothing to book automatically for %s."

	msgAutopilotOpenDay       = "%s is not a completed day: the autopilot never books today or a future day; use a day before today, or run `ticktack auto` without --day to work through the lookback window"
	msgAutopilotNothingToBook = "Autopilot: nothing to book on the completed days of the lookback window."
	msgAutopilotUndoHeld      = "Autopilot: held %s. It will not book these days again until you run `ticktack auto --day <day>` or confirm the day in `ticktack review`."
	msgAutopilotHeldDays      = "Autopilot: held (undone by you), skipped: %s. Run `ticktack auto --day <day>` to book a day again."
	msgAutopilotLogHeld       = "autopilot: held (undone by you), skipped: %s"
	msgAutopilotItemsFailed   = "%d items failed and %d were not sent (first error: %s); see `ticktack digest`"
	msgAutopilotLogFail       = "autopilot: run failed: %v"
	msgAutopilotLogConfig     = "autopilot: skipped, config not loadable: %v"
	msgAutopilotLogBusy       = "autopilot: skipped, another booking run is active"
	msgAutopilotLogLock       = "autopilot: skipped, booking lock failed: %v"

	msgDigestNone = "No digest yet: `ticktack auto` has not booked anything."

	msgDigestOriginFormat    = "%s %dm"
	msgDigestReasonSeparator = "; "
	msgDigestNeutralPrefix   = "uncertain time on standard task: "
	msgDigestNoTicket        = "no ticket"

	msgDigestUndoHint = "Undo: run `ticktack undo` to revert this run."
)

func msgDigestTitle(d auto.Digest) string {
	title := "Auto digest for " + d.Day.String()
	if d.DryRun {
		title += " (dry run, nothing booked)"
	}
	return title
}

// Column headers of the digest table.
const (
	msgDigestColStatus = "Status"
	msgDigestColTicket = "Ticket"
	msgDigestColDate   = "Date"
	msgDigestColTime   = "Time"
	msgDigestColTier   = "Tier"
	msgDigestColReason = "Reason"
)

func msgDigestCap(d auto.Digest) string {
	return fmt.Sprintf("Day total %s exceeds the daily cap of %s: nothing was booked, review the day with `ticktack review`.",
		formatMinutes(d.TotalMinutes), formatMinutes(d.CapMinutes))
}

// msgDigestTotalsFormat is the whole totals line of the digest. `ticktack
// doctor` builds its parser from the same string, so the two cannot drift
// apart. The verbs, in order: created, updated, recovered, repaired, deleted,
// skipped, failed, not sent (all %d) and the booked time (%s).
const msgDigestTotalsFormat = "Totals: created %d, updated %d, recovered %d, repaired %d, deleted %d, skipped %d, failed %d, not sent %d; %s booked."

func msgDigestTotals(d auto.Digest) string {
	return fmt.Sprintf(msgDigestTotalsFormat,
		d.Created, d.Updated, d.Recovered, d.Repaired, d.Deleted, d.Skipped, d.Failed, d.NotSent, formatMinutes(d.BookedMinutes))
}

// renderDigest formats the digest text that is printed (with the run's
// renderer) and stored (with render.Plain). External text (ticket keys,
// reasons, server errors) is sanitised before it is styled or padded.
func renderDigest(d auto.Digest, r render.Renderer) string {
	lines := []string{r.Heading(msgDigestTitle(d))}
	if len(d.Entries) > 0 {
		lines = append(lines, "")
		lines = append(lines, digestTable(r, d.Entries)...)
		lines = append(lines, "")
	}
	if d.CapExceeded {
		lines = append(lines, r.Gutter(render.StatusWarn, msgDigestCap(d)))
	}
	lines = append(lines, r.Gutter(render.StatusInfo, msgDigestTotals(d)))
	if d.Changed() && !d.DryRun {
		lines = append(lines, r.Gutter(render.StatusGo, msgDigestUndoHint))
	}
	return strings.Join(lines, "\n")
}

// digestTable lays out the entries as a table. A reason that does not fit the
// flexible column is never cut: the table wraps it onto dimmed lines under its
// row.
func digestTable(r render.Renderer, entries []auto.DigestEntry) []string {
	rows := make([][]string, 0, len(entries))
	for _, e := range entries {
		tier := "-"
		if e.Tier != "" {
			tier = colorTier(r, e.Tier)
		}
		rows = append(rows, []string{
			colorAction(r, e.Status),
			SanitizeForTerminal(e.Ticket),
			e.Date.String(),
			fmt.Sprintf("%d min", e.Minutes),
			tier,
			digestReason(e),
		})
	}
	return r.Table([]render.Column{
		{Header: msgDigestColStatus},
		{Header: msgDigestColTicket},
		{Header: msgDigestColDate},
		{Header: msgDigestColTime, Right: true},
		{Header: msgDigestColTier},
		{Header: msgDigestColReason, Flex: true, Wrap: true},
	}, rows)
}

// digestReason is the reason cell of an entry: its reason, followed by the
// origins of the time when the entry is the standard-task item.
func digestReason(e auto.DigestEntry) string {
	reason := SanitizeForTerminal(e.Reason)
	if !e.Neutral || len(e.From) == 0 {
		return reason
	}
	origins := make([]string, 0, len(e.From))
	for _, o := range e.From {
		origins = append(origins, fmt.Sprintf(msgDigestOriginFormat, digestOriginName(o), o.Minutes))
	}
	text := msgDigestNeutralPrefix + strings.Join(origins, ", ")
	if reason == "" {
		return text
	}
	return reason + msgDigestReasonSeparator + text
}

func digestOriginName(o auto.Origin) string {
	if o.Ticket == "" {
		return msgDigestNoTicket
	}
	return SanitizeForTerminal(o.Ticket)
}

// Help texts and flag descriptions of the auto command.
const (
	msgAutoAutoShort   = "Book the safe blocks of a day without a review (opt-in: gated by a track record, or autopilot)"
	msgAutoAutoLong    = msgAutoLong + msgBookingLockHelpDryRun
	msgAutoDayFlag     = "local day to book as YYYY-MM-DD (default: today)"
	msgAutoDryRunFlag  = "print the plan without booking anything"
	msgAutoDigestShort = "Show the digest of the last auto run"
	msgAutoDigestLong  = msgDigestLong
)
