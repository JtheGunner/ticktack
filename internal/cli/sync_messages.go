package cli

// User-facing strings of the sync report, shared with the auto digest, kept
// together so a German variant can be added later.
const (
	msgNoTimeToBook = "No time to book."
	// msgSyncItem is the per-item line: key, minutes, action.
	msgSyncItem         = "%-28s %4d min  %s"
	msgSyncSummaryTitle = "Summary"
	msgSyncPlanTitle    = "Plan"
	msgSyncPlan         = "Plan: %d to change, %d skipped, %d failed."
	// The totals are two box rows so they fit narrow terminals.
	msgSyncTotalsBooked  = "Created %d, updated %d, recovered %d."
	msgSyncTotalsOutcome = "Skipped %d, failed %d, not sent %d."
)
