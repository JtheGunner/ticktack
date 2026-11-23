package cli

// User-facing strings of `config update`, kept together so a German variant
// can be added later.
const (
	msgConfigUpdateNoConfig    = "no config at %s; run ticktack init"
	msgConfigUpdatePlanFailed  = "cannot update %s: %w"
	msgConfigUpdateUpToDate    = "Config is up to date: %s"
	msgConfigUpdateAdded       = "+ %s"
	msgConfigUpdateAddedLine   = "    %s"
	msgConfigUpdateRemoved     = "- %s (was: %s)"
	msgConfigUpdateRemovedNone = "- %s"
	msgConfigUpdateSummary     = "%d to add, %d to remove."
	msgConfigUpdateAsk         = "Apply these changes?"
	msgConfigUpdateConfirm     = "%s [Y/n] "
	msgConfigUpdateDone        = "Updated %s (backup: %s)"
	msgConfigUpdateAborted     = "Nothing changed."
)

// Help texts and flag descriptions of the config command.
const (
	msgConfigShort       = "Inspect and maintain the config file"
	msgConfigUpdateShort = "Add new options to the config and drop removed ones"
	msgConfigUpdateLong  = "Compares the config file with the options this version knows. Options the file " +
		"lacks are added as commented blocks with their description; active keys this version no longer " +
		"knows are removed. Everything else, including your values and comments, stays as it is. " +
		"The previous file is saved next to it as config.toml.bak."
	msgConfigUpdateYesFlag    = "apply without asking for confirmation"
	msgConfigUpdateDryRunFlag = "show the changes without writing anything"
)
