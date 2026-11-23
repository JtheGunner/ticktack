package cli

// All user-facing CLI texts (output lines, prompts, user-visible errors, help
// and flag descriptions) live in this file group, so a German variant can be
// added later: messages.go (init, doctor) and one <command>_messages.go per
// command or command group. Texts with parameters are format-string constants
// or small functions; callers must not define text of their own. Sentinel
// errors compared with errors.Is and wrapped %w errors stay with their code.
//
// User-facing strings of `init` and `doctor`.
const (
	msgInitConfigIntro   = "Step 1/4 - config: create the config file with commented defaults at %s"
	msgInitConfigKept    = "config exists, kept: %s"
	msgInitConfigWritten = "config written: %s"
	msgInitConfigURL     = "YouTrack base URL (empty to set it later): "
	msgInitConfigBadURL  = "config not written: %q is not an absolute URL"
	msgInitConfigNoHTTPS = "config not written: %v"
	msgInitConfigFailed  = "config failed: %v"

	msgInitTokenIntro    = "Step 2/4 - token: store the YouTrack permanent token in the OS keychain"
	msgInitTokenPrompt   = "Token (input hidden, empty to skip): "
	msgInitTokenSkipped  = "no token entered, skipped"
	msgInitTokenStored   = "token stored in the keychain"
	msgInitTokenFailed   = "token not stored: %v"
	msgInitTokenValidate = "Validate the token against YouTrack now?"

	// msgInitConfigIntroNoPath and msgInitHookIntroNoPath head a step whose
	// target file could not be determined.
	msgInitConfigIntroNoPath = "Step 1/4 - config: create the config file with commented defaults"
	msgInitHookIntroNoPath   = "Step 3/4 - shell hook: add a managed block to your shell startup file"
	msgInitHookIntro         = "Step 3/4 - shell hook: add a managed block to %s"
	msgInitHookUnsupported   = "shell %q is not supported (use --shell zsh|bash|fish); skipped"
	msgInitHookUnchanged     = "shell hook already up to date: %s"
	msgInitHookWritten       = "shell hook written: %s (open a new shell to activate it)"
	msgInitHookFailed        = "shell hook failed: %v"

	// The tt alias lives in the same managed block as the hook.
	msgInitAliasExplain  = "The block also gets an optional alias, tt for ticktack (skip it with --skip-alias)"
	msgInitAliasSet      = "alias tt set (open a new shell to use it)"
	msgInitAliasSkipped  = "alias tt skipped (--skip-alias)"
	msgInitAliasConflict = "alias tt not set: tt already exists (%s)"

	// msgAliasConflictCommand and msgAliasConflictRC say what already provides
	// tt. Only a command on PATH and the rc file init edits are inspected.
	msgAliasConflictCommand = "command %s"
	msgAliasConflictRC      = "defined in your shell startup file"

	msgInitAgentIntro   = "Step 4/4 - agent: install and load the launchd agent at %s"
	msgInitAgentNotMac  = "launchd agent is macOS-only; skipped"
	msgInitAgentWritten = "plist written: %s"
	msgInitAgentKept    = "plist unchanged: %s"
	msgInitAgentLoaded  = "agent loaded"
	msgInitAgentFailed  = "agent step failed: %v"
	msgInitAgentHint    = "hint: run `launchctl bootstrap gui/%d %s` manually, then `ticktack doctor`"

	msgInitSkipped  = "skipped"
	msgInitConfirm  = "Proceed?"
	msgInitFinished = "Done. Run `ticktack doctor` to verify the setup."
	// msgInitFinishedWithFailures closes init when at least one step failed.
	msgInitFinishedWithFailures = "Finished with errors. Run `ticktack doctor` to see what still needs fixing."

	msgDoctorSummaryOK     = "All %d checks passed."
	msgDoctorSummaryFailed = "%d of %d checks failed."
	// msgDoctorSummaryWarned closes a report without failures but with warnings.
	msgDoctorSummaryWarned = "No check failed; %d of %d with a warning."
	msgDoctorSummaryTitle  = "Summary"

	msgNoColorUsage = "disable coloured output; NO_COLOR and TERM=dumb are honoured too"
)

// Help texts, flag descriptions and the confirmation suffix of `init`.
const (
	msgInitShort = "Set up config, token, shell hook and launchd agent"
	msgInitLong  = "Walks through four steps: config file, YouTrack token (hidden input), " +
		"shell hook and launchd agent. Every step is explained and can be skipped; " +
		"rerunning is safe. The shell hook block also defines the optional alias " +
		"tt for ticktack unless tt already exists or --skip-alias is given."
	msgInitYesFlag          = "accept every step without asking"
	msgInitURLFlag          = "YouTrack base URL for the new config file"
	msgInitShellFlag        = "shell to hook (zsh, bash or fish; default: from $SHELL)"
	msgInitSkipConfigFlag   = "skip the config step"
	msgInitSkipTokenFlag    = "skip the token step"
	msgInitSkipHookFlag     = "skip the shell hook step"
	msgInitSkipAgentFlag    = "skip the launchd agent step"
	msgInitSkipAliasFlag    = "do not add the tt alias to the shell hook block"
	msgInitPrintSnippetFlag = "print the hook script for a shell and exit"
	msgInitConfirmSuffix    = "%s [Y/n] "
)
