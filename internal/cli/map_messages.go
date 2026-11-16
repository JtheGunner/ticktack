package cli

import "fmt"

// User-facing strings of `map` live here so a German variant can be added
// later. Callers pass already sanitised arguments.
const (
	msgMapConfigHeader  = "Configured rules (read-only, edit them in the config file %s):"
	msgMapConfigRule    = "  %s -> %s"
	msgMapNoConfigRules = "  (none)"
	msgMapLearnedHeader = "Learned rules (change with `ticktack map add|edit|rm`):"
	msgMapNoLearned     = "  (none)"
	msgMapLearnedRow    = "  #%-3d repo %s  branch %s  -> %s  (created %s)"
	msgMapRederive      = "Blocks are re-derived from the recorded events: the change applies to all days. Confirmations whose minutes change must be confirmed again in `ticktack review`."
	msgMapAdded         = "Added learned rule #%d: repo %s, branch %s -> %s."
	msgMapEdited        = "Updated learned rule #%d: repo %s, branch %s -> %s."
	msgMapRemoved       = "Removed learned rule #%d."
	msgMapNothingToEdit = "nothing to change: pass at least one of --repo, --branch, --ticket"
	msgMapEmptyRule     = "a rule needs a repo or a branch (both are empty and would match everything)"
	msgMapRelativeRepo  = "--repo %q must be an absolute repository path"
)

func msgMapUnknownID(id int64) error {
	return fmt.Errorf("no learned rule with id %d; run `ticktack map` to list the ids (configured rules cannot be edited here)", id)
}

func msgMapBadID(arg string) error {
	return fmt.Errorf("%q is not a rule id (a number, see `ticktack map`)", arg)
}
