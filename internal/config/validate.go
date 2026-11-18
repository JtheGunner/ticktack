package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/JtheGunner/ticktack/internal/loopback"
)

// Validation messages. Each one names the key and how to fix the value.
const (
	msgZeroDuration       = "%s must be greater than 0: %s"
	msgZeroFixRemove      = "remove the line to use the default"
	msgZeroFixRounding    = "use \"1m\" to book exact minutes"
	msgZeroFixDailyCap    = "set daily_cap_enabled = false to turn the cap off"
	msgRoundingNotMinutes = "rounding must be a multiple of whole minutes such as \"5m\" or \"15m\", got %q"
	msgURLNotAbsolute     = "youtrack_url %q is not an absolute URL"
	msgURLNotHTTPS        = "youtrack_url must start with \"https://\" so the token is never sent in clear text; " +
		"plain http is accepted only for localhost, 127.0.0.0/8 and ::1"
	msgRuleIncomplete = "rules[%d]: repo and ticket are both required"
	msgRuleRepoSyntax = "rules[%d].repo must be an absolute path or start with \"~/\""
	msgRuleRepoHome   = "rules[%d].repo starts with \"~\" but the home directory is not available: use an absolute path"
	msgRuleTicket     = "rules[%d].ticket %q does not match ticket_pattern %q: use a ticket id such as \"ABC-1\" or adjust ticket_pattern"
)

// checkYouTrackURL verifies that the YouTrack URL is absolute and uses https.
// Plain http is accepted for a loopback host only, because the bearer token
// would otherwise travel in clear text. An empty URL means "not configured".
func checkYouTrackURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf(msgURLNotAbsolute, raw)
	}
	if strings.EqualFold(u.Scheme, "https") {
		return nil
	}
	if !strings.EqualFold(u.Scheme, "http") {
		return errors.New(msgURLNotHTTPS)
	}
	if _, err := loopback.CheckURL(raw); err != nil {
		return errors.New(msgURLNotHTTPS)
	}
	return nil
}

// buildRules validates the [[rules]] tables. Repositories are expanded like
// repo_paths entries and normalised; every ticket must match the ticket
// pattern as a whole.
func buildRules(raw []Rule, pattern string) ([]Rule, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	wholeTicket, err := regexp.Compile(`^(?:` + pattern + `)$`)
	if err != nil {
		return nil, fmt.Errorf("ticket_pattern: %w", err)
	}
	rules := make([]Rule, 0, len(raw))
	for i, rule := range raw {
		if rule.Repo == "" || rule.Ticket == "" {
			return nil, fmt.Errorf(msgRuleIncomplete, i)
		}
		repo, err := resolveRuleRepo(i, rule.Repo)
		if err != nil {
			return nil, err
		}
		if !wholeTicket.MatchString(rule.Ticket) {
			return nil, fmt.Errorf(msgRuleTicket, i, rule.Ticket, pattern)
		}
		rules = append(rules, Rule{Repo: repo, Ticket: rule.Ticket})
	}
	return rules, nil
}

// resolveRuleRepo expands and normalises the repository of rule i. The home
// directory is looked up only for a path that starts with "~".
func resolveRuleRepo(i int, repo string) (string, error) {
	if !validRepoPathSyntax(repo) {
		return "", fmt.Errorf(msgRuleRepoSyntax, i)
	}
	home := ""
	if !filepath.IsAbs(repo) {
		dir, err := os.UserHomeDir()
		if err != nil || !filepath.IsAbs(dir) {
			return "", fmt.Errorf(msgRuleRepoHome, i)
		}
		home = dir
	}
	expanded, err := ExpandRepoPath(repo, home)
	if err != nil {
		return "", fmt.Errorf("rules[%d].repo: %w", i, err)
	}
	return resolvePath(expanded), nil
}

// resolvePath returns the clean form of an absolute path with symlinks
// resolved, which is how git reports a repository root. A path that cannot be
// resolved, for example because it does not exist, stays cleaned only.
func resolvePath(path string) string {
	cleaned := filepath.Clean(path)
	resolved, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		return cleaned
	}
	return resolved
}
