package cli

// User-facing strings of `uninstall`, kept together so a German variant can
// be added later.
const (
	msgUninstallPromptIntro = "Uninstall ticktack from this machine? This removes:"
	msgUninstallPromptAgent = "  - the launchd agent: %s"
	msgUninstallPromptHook  = "  - the shell hook block in: %s"
	msgUninstallPromptToken = "  - the YouTrack token in the OS keychain"
	msgUninstallPromptLog   = "  - the log file: %s"
	msgUninstallPromptKept  = "The config file, the database and the ticktack binary stay; `ticktack purge` deletes the data."
	msgUninstallPromptAsk   = "Proceed? [y/N] "
	msgUninstallAborted     = "Aborted. Nothing was removed."

	msgUninstallAgentUnloaded     = "agent unloaded"
	msgUninstallAgentUnloadFailed = "agent not unloaded: %v"
	msgUninstallPlistRemoved      = "plist removed: %s"
	msgUninstallPlistMissing      = "no plist at %s"
	msgUninstallPlistFailed       = "plist not removed: %v"
	msgUninstallHookRemoved       = "shell hook removed from %s"
	msgUninstallHookMissing       = "no shell hook block found"
	msgUninstallHookFailed        = "shell hook not removed: %v"
	msgUninstallTokenRemoved      = "token removed from the keychain"
	msgUninstallTokenMissing      = "no token in the keychain"
	msgUninstallTokenFailed       = "token not removed: %v"
	msgUninstallLogRemoved        = "log file removed: %s"
	msgUninstallLogMissing        = "no log file at %s"
	msgUninstallLogFailed         = "log file not removed: %v"

	msgUninstallFinished = "Uninstalled. Open a new shell to drop the hook there too. " +
		"Config, database and binary are left; `ticktack purge` deletes the data."
	msgUninstallIncomplete = "Uninstall incomplete. Fix the problems above and run `ticktack uninstall` again."
)
