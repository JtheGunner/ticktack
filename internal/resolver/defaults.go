package resolver

import (
	"fmt"
	"strings"
)

// defaultRefSuffix marks the placeholder for a standard task that does not
// exist in YouTrack yet. "PROJECT:default" is never a valid issue key, so it
// can never be booked by accident.
const defaultRefSuffix = ":default"

// ProjectInfo is a YouTrack project the resolver may match a folder name to.
type ProjectInfo struct {
	Key  string
	Name string
}

// DefaultRef returns the placeholder ticket for the standard task of project.
func DefaultRef(project string) string { return project + defaultRefSuffix }

// ParseDefaultRef reports the project of a placeholder created by DefaultRef.
func ParseDefaultRef(ticket string) (project string, ok bool) {
	project, found := strings.CutSuffix(ticket, defaultRefSuffix)
	if !found || project == "" {
		return "", false
	}
	return project, true
}

// NormalizeName lower-cases s and drops "-", "_" and white space, so
// "Home-Lab_AI Chat" and "homelabaichat" compare equal.
func NormalizeName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch r {
		case '-', '_', ' ', '\t':
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// matchingProjects returns every project whose normalised name equals the
// normalised folder name.
func matchingProjects(folder string, projects []ProjectInfo) []ProjectInfo {
	want := NormalizeName(folder)
	if want == "" {
		return nil
	}
	var found []ProjectInfo
	for _, p := range projects {
		if NormalizeName(p.Name) == want {
			found = append(found, p)
		}
	}
	return found
}

// defaultTicket returns the real issue key of a project's standard task when
// it is known, else the placeholder.
func (r Rules) defaultTicket(project string) string {
	if key := r.DefaultTasks[project]; key != "" {
		return key
	}
	return DefaultRef(project)
}

// decideDefault applies the standard-task steps: the project whose name
// matches the repo's folder, then the global standard task. It reports false
// when neither applies.
func decideDefault(repo string, rules Rules) (Decision, bool) {
	why := ""
	if rules.FolderOf != nil {
		folder, inRoots := rules.FolderOf(repo)
		switch {
		case !inRoots:
			why = "repo outside repo_roots"
		default:
			found := matchingProjects(folder, rules.Projects)
			switch len(found) {
			case 0:
				why = fmt.Sprintf("no project named %q", folder)
			case 1:
				project := found[0]
				return Decision{
					Attribution: Attribution{
						Ticket: rules.defaultTicket(project.Key),
						Tier:   TierRule,
						Reason: fmt.Sprintf("standard task of project %s (folder %q)", project.Key, folder),
					},
					Rule: RuleProjectDefault,
				}, true
			default:
				keys := make([]string, len(found))
				for i, p := range found {
					keys[i] = p.Key
				}
				why = "ambiguous: " + strings.Join(keys, ", ")
			}
		}
	}
	if rules.GlobalProject == "" {
		return Decision{}, false
	}
	reason := "global standard task of " + rules.GlobalProject
	if why != "" {
		reason += " (" + why + ")"
	}
	return Decision{
		Attribution: Attribution{Ticket: rules.defaultTicket(rules.GlobalProject), Tier: TierRule, Reason: reason},
		Rule:        RuleGlobalDefault,
	}, true
}
