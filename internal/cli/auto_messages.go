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
passed without a single correction. A later correction in "ticktack review"
locks it again.

A day counts towards that streak only when its review session took place on
the same calendar day or the next one (in your configured time zone). Reviews
made later with "ticktack review --day" stay valid for booking but do not count.

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

func msgDigestTotals(d auto.Digest) string {
	return fmt.Sprintf("Totals: created %d, updated %d, recovered %d, skipped %d, failed %d, not sent %d; %s booked.",
		d.Created, d.Updated, d.Recovered, d.Skipped, d.Failed, d.NotSent, formatMinutes(d.BookedMinutes))
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
// flexible column is never cut: its cell is left empty and the whole reason is
// wrapped onto indented lines under the row.
func digestTable(r render.Renderer, entries []auto.DigestEntry) []string {
	reasons := make([]string, len(entries))
	for i, e := range entries {
		reasons[i] = SanitizeForTerminal(e.Reason)
	}
	build := func(skip map[int]bool) []string {
		rows := make([][]string, 0, len(entries))
		for i, e := range entries {
			tier := "-"
			if e.Tier != "" {
				tier = colorTier(r, e.Tier)
			}
			reason := reasons[i]
			if skip[i] {
				reason = ""
			}
			rows = append(rows, []string{
				colorAction(r, e.Status),
				SanitizeForTerminal(e.Ticket),
				e.Date.String(),
				fmt.Sprintf("%d min", e.Minutes),
				tier,
				reason,
			})
		}
		return r.Table([]render.Column{
			{Header: msgDigestColStatus},
			{Header: msgDigestColTicket},
			{Header: msgDigestColDate},
			{Header: msgDigestColTime, Right: true},
			{Header: msgDigestColTier},
			{Header: msgDigestColReason, Flex: true},
		}, rows)
	}

	// Reasons whose row line lost text to the ellipsis move below their row.
	// The remaining reasons are no wider than the shrunk column, so one extra
	// pass fits them.
	moved := map[int]bool{}
	for i, line := range build(nil)[1:] {
		if reasons[i] != "" && !strings.Contains(line, reasons[i]) {
			moved[i] = true
		}
	}
	table := build(moved)
	if len(moved) == 0 {
		return table
	}
	out := []string{table[0]}
	for i, line := range table[1:] {
		out = append(out, line)
		if moved[i] {
			for _, part := range wrapWords(reasons[i], max(r.LayoutWidth()-digestReasonIndent, 1)) {
				out = append(out, strings.Repeat(" ", digestReasonIndent)+r.Colors.Dim(part))
			}
		}
	}
	return out
}

// digestReasonIndent is the indent of a reason wrapped below its row.
const digestReasonIndent = 4

// wrapWords breaks text at spaces into lines of at most width runes. A word
// longer than width is split.
func wrapWords(text string, width int) []string {
	var lines []string
	var line []rune
	flush := func() {
		if len(line) > 0 {
			lines = append(lines, string(line))
			line = nil
		}
	}
	for _, word := range strings.Fields(text) {
		w := []rune(word)
		for len(w) > width {
			flush()
			lines = append(lines, string(w[:width]))
			w = w[width:]
		}
		if len(line) > 0 && len(line)+1+len(w) > width {
			flush()
		}
		if len(line) > 0 {
			line = append(line, ' ')
		}
		line = append(line, w...)
	}
	flush()
	return lines
}
