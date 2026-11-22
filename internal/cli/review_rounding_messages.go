package cli

import (
	"fmt"
	"time"

	"github.com/JtheGunner/ticktack/internal/sessionizer"
)

// User-facing text of the rounding section of the review.

// msgReviewRoundingSummary reconciles the exact day total with the rounded
// total (which includes unassigned time). The difference is always stated.
func msgReviewRoundingSummary(s sessionizer.DaySummary) string {
	diff := s.Difference()
	effect := "rounding removed " + formatExactDuration(diff)
	if diff < 0 {
		effect = "rounding added " + formatExactDuration(-diff)
	}
	return fmt.Sprintf("Rounding: tracked %s, rounded total %s (%s).",
		formatExactDuration(s.Raw), formatExactDuration(s.Booked), effect)
}

// msgReviewRoundingDropped names one ticket whose time rounded to zero and is
// therefore not in the rounded total. The ticket is sanitised by the caller.
func msgReviewRoundingDropped(ticket string, raw time.Duration) string {
	return fmt.Sprintf("  %s: %s rounds to zero and is not booked.", ticket, formatExactDuration(raw))
}

// formatExactDuration formats d in whole minutes (nearest), but never shows
// time that exists as "0m".
func formatExactDuration(d time.Duration) string {
	minutes := int((d + 30*time.Second) / time.Minute)
	if minutes == 0 && d != 0 {
		return "<1m"
	}
	return formatMinutes(minutes)
}
