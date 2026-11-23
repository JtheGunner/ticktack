package cli

import "fmt"

// User-facing strings of `map` live here so a German variant can be added
// later. Callers pass already sanitised arguments.
const (
	msgMapConfigHeading  = "Configured rules"
	msgMapConfigNote     = "read-only, edit them in the config file %s"
	msgMapUnknownPath    = "(unknown)"
	msgMapNoConfigRules  = "no configured rules"
	msgMapLearnedHeading = "Learned rules"
	msgMapLearnedNote    = "change with `ticktack map add|edit|rm`"
	msgMapNoLearned      = "no learned rules"
	msgColRepo           = "Repo"
	msgColBranch         = "Branch"
	msgColID             = "ID"
	msgColCreated        = "Created"
	msgMapRederive       = "Blocks are re-derived from the recorded events: the change applies to all days. Confirmations whose minutes change must be confirmed again in `ticktack review`."
	msgMapAdded          = "Added learned rule #%d: repo %s, branch %s -> %s."
	msgMapEdited         = "Updated learned rule #%d: repo %s, branch %s -> %s."
	msgMapRemoved        = "Removed learned rule #%d."
	msgMapNothingToEdit  = "nothing to change: pass at least one of --repo, --branch, --ticket"
	msgMapEmptyRule      = "a rule needs a repo or a branch (both are empty and would match everything)"
	msgMapRelativeRepo   = "--repo %q must be an absolute repository path"
)

func msgMapUnknownID(id int64) error {
	return fmt.Errorf("no learned rule with id %d; run `ticktack map` to list the ids (configured rules cannot be edited here)", id)
}

func msgMapBadID(arg string) error {
	return fmt.Errorf("%q is not a rule id (a number, see `ticktack map`)", arg)
}

// Help texts and flag descriptions of the map command.
const (
	msgMapMapShort = "List and edit ticket assignment rules"
	msgMapMapLong  = "List the configured (read-only) and learned rules. Learned rules can be added, edited and removed;\n" +
		"configured rules live in the config file.\n\n" +
		"Matching is deliberately simple: a rule's branch must equal the branch name exactly (no wildcards or\n" +
		"globs), and its repository matches that path and everything below it. Add one rule per branch, or\n" +
		"omit --branch to cover every branch of a repository."
	msgMapListShort      = "List configured and learned rules"
	msgMapAddShort       = "Add a learned rule"
	msgMapAddRepoFlag    = "absolute repository path (prefix)"
	msgMapAddBranchFlag  = "branch name (empty matches every branch)"
	msgMapEditShort      = "Change a learned rule"
	msgMapEditRepoFlag   = "new repository path (empty clears it)"
	msgMapEditBranchFlag = "new branch (empty clears it)"
	msgMapEditTicketFlag = "new ticket"
	msgMapRmShort        = "Remove a learned rule"
)
