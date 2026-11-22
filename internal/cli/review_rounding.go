package cli

import (
	"github.com/JtheGunner/ticktack/internal/render"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
)

// roundingLines returns the review lines that make rounding visible: the
// reconciliation of the tracked day total with the rounded total, and every
// ticket whose time rounded away. It returns nothing when rounding changed
// nothing, so exact days stay quiet.
func roundingLines(r render.Renderer, s sessionizer.DaySummary) []string {
	if s.Difference() == 0 && len(s.Dropped) == 0 {
		return nil
	}
	lines := []string{r.Gutter(render.StatusInfo, msgReviewRoundingSummary(s))}
	for _, d := range s.Dropped {
		lines = append(lines, msgReviewRoundingDropped(SanitizeForTerminal(ticketLabel(d.Ticket)), d.Raw))
	}
	return lines
}
