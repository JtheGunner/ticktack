package cli

// User-facing strings of `init` and `doctor`, kept together so a German
// variant can be added later.
const (
	msgInitConfigIntro   = "Step 1/4 - config: create the config file with commented defaults at %s"
	msgInitConfigKept    = "  config exists, kept: %s"
	msgInitConfigWritten = "  config written: %s"
	msgInitConfigURL     = "YouTrack base URL (empty to set it later): "
	msgInitConfigBadURL  = "  config not written: %q is not an absolute URL"
	msgInitConfigFailed  = "  config failed: %v"

	msgInitTokenIntro    = "Step 2/4 - token: store the YouTrack permanent token in the OS keychain"
	msgInitTokenPrompt   = "Token (input hidden, empty to skip): "
	msgInitTokenSkipped  = "  no token entered, skipped"
	msgInitTokenStored   = "  token stored in the keychain"
	msgInitTokenFailed   = "  token not stored: %v"
	msgInitTokenValidate = "Validate the token against YouTrack now?"

	msgInitHookIntro       = "Step 3/4 - shell hook: add a managed block to %s"
	msgInitHookUnsupported = "  shell %q is not supported (use --shell zsh|bash|fish); skipped"
	msgInitHookUnchanged   = "  shell hook already up to date: %s"
	msgInitHookWritten     = "  shell hook written: %s (open a new shell to activate it)"
	msgInitHookFailed      = "  shell hook failed: %v"

	msgInitAgentIntro   = "Step 4/4 - agent: install and load the launchd agent at %s"
	msgInitAgentNotMac  = "  launchd agent is macOS-only; skipped"
	msgInitAgentWritten = "  plist written: %s"
	msgInitAgentKept    = "  plist unchanged: %s"
	msgInitAgentLoaded  = "  agent loaded"
	msgInitAgentFailed  = "  agent step failed: %v"
	msgInitAgentHint    = "  hint: run `launchctl bootstrap gui/%d %s` manually, then `ticktack doctor`"

	msgInitSkipped  = "  skipped"
	msgInitConfirm  = "Proceed?"
	msgInitFinished = "Done. Run `ticktack doctor` to verify the setup."

	msgDoctorSummaryOK     = "All %d checks passed."
	msgDoctorSummaryFailed = "%d of %d checks failed."
)
