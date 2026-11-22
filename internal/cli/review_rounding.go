package cli

import (
	"github.com/JtheGunner/ticktack/internal/render"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
)

// roundingLines returns the review lines that make rounding visible: the
// reconciliation of the tracked day total with the rounded total, every
// ticket whose booked minutes differ from its tracked minutes (up or down),
// and every ticket whose time rounded away. A ticket appears on exactly one
// line. It returns nothing when rounding changed nothing, so exact days stay
// quiet.
func roundingLines(r render.Renderer, s sessionizer.DaySummary, items []sessionizer.DayItem) []string {
	var itemLines []string
	for _, it := range items {
		if it.RawMinutes == it.Minutes {
			continue
		}
		label := SanitizeForTerminal(ticketLabel(it.Ticket))
		itemLines = append(itemLines, msgReviewRoundingShifted(label, it.RawMinutes, it.Minutes))
	}
	if s.Difference() == 0 && len(s.Dropped) == 0 && len(itemLines) == 0 {
		return nil
	}
	lines := []string{r.Gutter(render.StatusInfo, msgReviewRoundingSummary(s))}
	lines = append(lines, itemLines...)
	for _, d := range s.Dropped {
		lines = append(lines, msgReviewRoundingDropped(SanitizeForTerminal(ticketLabel(d.Ticket)), d.Raw))
	}
	return lines
}
