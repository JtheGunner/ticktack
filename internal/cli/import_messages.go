package cli

// User-facing strings of the import command and the ActivityWatch doctor
// check, kept together so a German variant can be added later.
const (
	msgImportDisabled   = "the ActivityWatch import is off: set enabled = true in the [activitywatch] section of %s"
	msgImportPaused     = "Capture is paused, nothing imported. Run `ticktack resume` first."
	msgImportBadSince   = "--since %q: expected format YYYY-MM-DD"
	msgImportDone       = "Imported %d ActivityWatch events since %s (%d presence, %d gap markers, %d context hints). Already known events were skipped."
	msgImportPartial    = "ActivityWatch import incomplete: %v"
	msgImportConfigPath = "the config file"

	msgCheckActivityWatch = "activitywatch"
	msgAWDisabled         = "import disabled (optional)"
	msgAWOK               = "reachable at %s, %d AFK and window buckets found"
	msgAWUnreachable      = "server at %s not reachable: %v"
	msgAWUnreachableFix   = "start ActivityWatch, check url in [activitywatch], or set enabled = false"
	msgAWNoBuckets        = "server at %s reachable, but no aw-watcher-afk or aw-watcher-window bucket found"
	msgAWNoBucketsFix     = "enable the AFK and window watchers in ActivityWatch, or set hostname in [activitywatch] to your machine name"
	msgAWBadURL           = "cannot use the configured server: %v"
	msgAWBadURLFix        = "set url in [activitywatch] to a loopback address such as http://localhost:5600"
)
