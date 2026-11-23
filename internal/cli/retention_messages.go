package cli

import "fmt"

// User-facing strings of the retention cleanup (`ticktack purge --expired`,
// the agent job and the messages for archived days) live here so a German
// variant can be added later.
const (
	msgPurgeExpiredOff  = "retention_days is 0 (off): no events are cleaned. Set retention_days in the configuration to turn the cleanup on."
	msgPurgeExpiredNone = "Nothing to clean: no fully booked day is older than %d days."
	msgPurgeDryRunFlag  = "--dry-run only works together with --expired"

	msgRetentionLogDone   = "retention: %d events of %d fully booked days deleted"
	msgRetentionLogFail   = "retention: cleanup failed: %v"
	msgRetentionLogBusy   = "retention: skipped, another booking run is active"
	msgRetentionLogConfig = "retention: skipped, config not loadable: %v"

	msgUndoArchivedNote = "Note: the last sync touched %d archived days. Archived days are not re-derived; their bookings are reverted from the sync history only."
)

// msgPurgeExpiredPlan states what a cleanup would do and what it costs.
func msgPurgeExpiredPlan(events, days, retentionDays int, dryRun bool) string {
	verb := "will delete"
	if dryRun {
		verb = "would delete"
	}
	return fmt.Sprintf("This %s %d events of %d fully booked days older than %d days. "+
		"Overrides, the ledger and the sync history stay, and nothing changes in YouTrack. "+
		"These days can no longer be re-derived, reviewed, explained or synced.",
		verb, events, days, retentionDays)
}

// msgPurgeExpiredPrompt asks before the cleanup deletes events.
const msgPurgeExpiredPrompt = "Delete these events? This cannot be undone. [y/N] "

// msgPurgeExpiredDone reports a finished cleanup.
func msgPurgeExpiredDone(events, days int) string {
	return fmt.Sprintf("Deleted %d events of %d days. Those days are archived and frozen.", events, days)
}

// msgDayArchived is the error for a command that needs the events of an
// archived day.
func msgDayArchived(day string, deleted int, archivedAt string) string {
	return fmt.Sprintf("%s is archived: its %d raw events were deleted on %s by the retention cleanup (retention_days), "+
		"so the day can no longer be derived, reviewed, explained or synced. "+
		"Its bookings in YouTrack and the ledger are unchanged.", day, deleted, archivedAt)
}
