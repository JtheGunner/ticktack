package resolver

import (
	"regexp"
	"strings"
)

// RepoRule maps a repository path to a fixed ticket. It has the same shape as
// config.Rule, so a config rule converts directly: RepoRule(cfgRule).
type RepoRule struct {
	Repo   string
	Ticket string
}

// LearnedRule maps a repository (path prefix) and optionally a branch to a
// ticket. An empty Repo or Branch matches anything, but a rule with both empty
// is ignored. Branch is compared for equality, like config rules compare repo
// paths exactly on a path boundary. It is filled by learning from corrections.
type LearnedRule struct {
	// ID is the store identifier, used only to name the rule in explanations;
	// zero when unknown. It never influences matching.
	ID     int64
	Repo   string
	Branch string
	Ticket string
}

// Rules is everything the resolver needs besides the signal. The zero value
// has no rules.
type Rules struct {
	Repos   []RepoRule
	Learned []LearnedRule
	// TicketPattern extracts ticket IDs from branch names; nil means ticketid.DefaultPattern.
	TicketPattern *regexp.Regexp

	// FolderOf returns the folder name of a repository below the configured
	// repo roots; false when the repository is outside every root. Nil turns
	// the per-project standard task off.
	FolderOf func(repo string) (folder string, ok bool)
	// Projects are the YouTrack projects a folder name is matched against.
	Projects []ProjectInfo
	// GlobalProject is the project of the global standard task; empty turns
	// the global standard task off.
	GlobalProject string
	// DefaultTasks maps a project key to the issue key of its standard task,
	// for the projects whose task already exists.
	DefaultTasks map[string]string
}

func trimRepo(repo string) string {
	return strings.TrimRight(repo, "/")
}

// repoMatches reports whether path equals repo or lies below it, on a path
// boundary. Trailing slashes on repo are ignored.
func repoMatches(repo, path string) bool {
	repo = trimRepo(repo)
	if repo == "" {
		return false
	}
	return path == repo || strings.HasPrefix(path, repo+"/")
}

// matchRepoRule returns the rule with the longest matching repo path; ties are
// broken by path, then ticket, so the outcome never depends on slice order.
func matchRepoRule(rules []RepoRule, repo string) (RepoRule, bool) {
	var best RepoRule
	found := false
	for _, r := range rules {
		if !repoMatches(r.Repo, repo) {
			continue
		}
		if !found || betterRepoRule(r, best) {
			best, found = r, true
		}
	}
	return best, found
}

func betterRepoRule(a, b RepoRule) bool {
	la, lb := len(trimRepo(a.Repo)), len(trimRepo(b.Repo))
	if la != lb {
		return la > lb
	}
	if a.Repo != b.Repo {
		return a.Repo < b.Repo
	}
	return a.Ticket < b.Ticket
}

// matchLearned prefers rules naming a branch, then the longest repo prefix,
// then path, branch and ticket order.
func matchLearned(rules []LearnedRule, repo, branch string) (LearnedRule, bool) {
	var best LearnedRule
	found := false
	for _, r := range rules {
		if r.Repo == "" && r.Branch == "" {
			continue // would match everything
		}
		if r.Repo != "" && !repoMatches(r.Repo, repo) {
			continue
		}
		if r.Branch != "" && r.Branch != branch {
			continue
		}
		if !found || betterLearned(r, best) {
			best, found = r, true
		}
	}
	return best, found
}

func betterLearned(a, b LearnedRule) bool {
	if (a.Branch != "") != (b.Branch != "") {
		return a.Branch != ""
	}
	la, lb := len(trimRepo(a.Repo)), len(trimRepo(b.Repo))
	if la != lb {
		return la > lb
	}
	if a.Repo != b.Repo {
		return a.Repo < b.Repo
	}
	if a.Branch != b.Branch {
		return a.Branch < b.Branch
	}
	return a.Ticket < b.Ticket
}
