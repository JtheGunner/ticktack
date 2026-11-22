package cli

// User-facing strings of the sync report, shared with the auto digest, kept
// together so a German variant can be added later.
const (
	msgNoTimeToBook = "No time to book."
	// The standard task of a project is created in YouTrack on demand.
	msgSyncCreatePrompt     = "Create standard task in: %s — continue? [y/N] "
	msgSyncTaskCreated      = "Created standard task %s in %s."
	msgSyncTaskFound        = "Using the existing standard task %s in %s."
	msgSyncTaskWouldCreate  = "Would create the standard task in: %s."
	msgSyncTaskWouldBook    = "%s: %s would be booked once the standard task exists."
	msgSyncTaskDeclined     = "Standard task not created in: %s. That time was not booked; run `ticktack sync` again to create it."
	msgSyncSinkCannotCreate = "%d item(s) skipped: the booking target cannot create standard tasks."
	// msgSyncItem is the per-item line: key, minutes, action.
	msgSyncItem         = "%-28s %4d min  %s"
	msgSyncSummaryTitle = "Summary"
	msgSyncPlanTitle    = "Plan"
	msgSyncPlan         = "Plan: %d to change, %d skipped, %d failed."
	// The totals are two box rows so they fit narrow terminals.
	msgSyncTotalsBooked  = "Created %d, updated %d, recovered %d, repaired %d, deleted %d."
	msgSyncTotalsOutcome = "Skipped %d, failed %d, not sent %d."

	// The booking lock is shared by sync, auto and undo.
	msgBookingRunActive  = "another ticktack booking run (sync, auto or undo) is active: try again when it has finished"
	msgBookingLockFailed = "cannot take the booking lock: %w"
	// msgBookingLockHelp closes the help text of the commands that take the lock.
	msgBookingLockHelp = `

Only one booking run (sync, auto or undo) is active at a time: while another
one runs, this command changes nothing and exits with code 75.`
	msgBookingLockHelpDryRun = msgBookingLockHelp + " --dry-run takes no lock."
)
