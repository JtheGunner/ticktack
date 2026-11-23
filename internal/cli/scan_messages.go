package cli

// User-facing strings of the scan command and the git repositories doctor
// check, kept together so a German variant can be added later.
const (
	msgScanNoRepoPaths = "no repositories configured: set repo_paths or repo_roots in %s, for example repo_roots = [\"~/Projects\"]"
	msgScanPaused      = "Capture is paused, nothing scanned. Run `ticktack resume` first."
	msgScanBadSince    = "--since %q: expected format YYYY-MM-DD"
	msgScanDone        = "Scanned %d of %d repositories since %s: %d new events (%d found, already known events were skipped)."
	msgScanHeading     = "Scan"
	msgScanRepoDone    = "%s: %d events found"
	msgScanSkipped     = "%s: skipped, %s"
	msgScanFailed      = "%d of %d repositories could not be scanned"
	msgScanConfigPath  = "the config file"

	msgRepoMissing      = "does not exist"
	msgRepoNotDir       = "is not a directory"
	msgRepoNotGit       = "is not the root of a git repository (no .git entry)"
	msgRepoUnreadable   = "cannot be read: %v"
	msgRepoBadPath      = "invalid path: %v"
	msgRepoGitMissing   = "git was not found in PATH"
	msgRepoScanTimeout  = "scan timed out"
	msgRepoScanNotRepo  = "git does not recognise it as a repository"
	msgRepoScanFailed   = "scan failed: %v"
	msgRepoLogSkipped   = "git scan: skipped %s: %s"
	msgRepoLogDone      = "git scan: %d of %d repositories scanned, %d events found"
	msgRepoLogStoreFail = "git scan: storing events failed: %v"
	msgRepoLogNoPauses  = "git scan skipped: pauses not readable: %v"

	msgCheckRepos        = "git repositories"
	msgReposNone         = "no repo_paths configured (optional)"
	msgReposConfigBad    = "skipped because the config cannot be read (see the config check)"
	msgReposOK           = "%d repositories readable"
	msgReposFail         = "%d of %d repositories unusable: %s"
	msgReposFixMissing   = "fix or remove the entry in repo_paths of %s, or create the directory"
	msgReposFixNotDir    = "point the repo_paths entry at the repository root directory, not a file"
	msgReposFixNotGit    = "use the directory that contains the .git entry; subdirectories are not searched"
	msgReposFixUnread    = "check the permissions of the directory"
	msgReposFixBadPath   = "write the repo_paths entry as an absolute path or starting with ~/"
	msgReposDetailJoiner = "; "
)

// Help texts and flag descriptions of the scan command.
const (
	msgScanScanShort = "Capture repository and branch context from the git reflog of the configured repositories"
	msgScanScanLong  = "Reads the HEAD reflog of every repository listed in repo_paths and stores context events " +
		"(repository, branch, ticket hint from the branch name, timestamp). Commit messages, file names " +
		"and file contents are never read into events. Scanning again is harmless: known events are skipped."
	msgScanSinceFlag = "scan from this day as YYYY-MM-DD (default: the start of the day 7 days ago)"
)
