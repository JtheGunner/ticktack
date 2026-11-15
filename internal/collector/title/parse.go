// Package title derives a context hint (IDE project name, YouTrack ticket)
// from the title of the frontmost window.
//
// Titles are processed in memory only. The parser reduces a title to a Hint
// that has no field able to hold raw title text, and the collector turns a
// Hint into an event carrying nothing but a ticket ID and a configured
// repository path. No title ever reaches the store, a log line or an error
// message.
package title

import (
	"regexp"
	"strings"

	"github.com/JtheGunner/ticktack/internal/ticketid"
)

// Hint is everything the parser extracts from a window title. It
// deliberately has no field for free text: Project is an IDE project name
// that is only used as a lookup key into the configured project mapping and
// is never persisted.
type Hint struct {
	// Ticket is a ticket ID matching the configured pattern, upper-cased.
	Ticket string
	// Project is the IDE project name, for known IDE bundle IDs only.
	Project string
}

// IsZero reports whether nothing was recognised.
func (h Hint) IsZero() bool { return h == Hint{} }

// appRule describes how titles of one family of applications are parsed.
// To support another application, add a rule to appRules.
type appRule struct {
	// exact and prefixes select the rule by bundle ID.
	exact    []string
	prefixes []string
	// project extracts the project name from an IDE title. Nil for apps that
	// are not IDEs.
	project func(title string) string
	// requireMarker, when set, is a lower-case substring the title must
	// contain before a ticket ID is accepted (for example "youtrack" for
	// browser tabs). The ticket is searched in the whole title.
	requireMarker string
}

var appRules = []appRule{
	{prefixes: []string{"com.jetbrains."}, exact: []string{"com.google.android.studio"}, project: jetBrainsProject},
	{
		exact: []string{
			"com.microsoft.VSCode", "com.microsoft.VSCodeInsiders", "com.vscodium",
			"com.visualstudio.code.oss", "com.todesktop.230313mzl4w4u92",
		},
		project: vsCodeProject,
	},
	{exact: []string{"com.apple.dt.Xcode"}, project: xcodeProject},
	{
		exact: []string{
			"com.apple.Safari", "org.mozilla.firefox", "com.microsoft.edgemac",
			"company.thebrowser.Browser", "com.brave.Browser", "com.vivaldi.Vivaldi",
		},
		prefixes:      []string{"com.google.Chrome"},
		requireMarker: "youtrack",
	},
}

func ruleFor(app string) (appRule, bool) {
	for _, r := range appRules {
		for _, id := range r.exact {
			if app == id {
				return r, true
			}
		}
		for _, p := range r.prefixes {
			if strings.HasPrefix(app, p) {
				return r, true
			}
		}
	}
	return appRule{}, false
}

// Parse extracts a Hint from the frontmost app's bundle ID and window title.
// It is a pure function. A nil pattern means the default ticket pattern.
//
// Rules:
//   - Unknown apps yield nothing.
//   - IDEs yield the project name and, if the project name itself contains a
//     ticket ID, that ticket. File names are never searched for tickets.
//   - Browsers yield a ticket only for YouTrack tabs (title mentions
//     "YouTrack"). Text inside URLs is ignored.
func Parse(app, title string, pattern *regexp.Regexp) Hint {
	rule, ok := ruleFor(app)
	if !ok || strings.TrimSpace(title) == "" {
		return Hint{}
	}
	if rule.project != nil {
		project := rule.project(title)
		return Hint{Project: project, Ticket: ticketid.Extract(stripURLs(project), pattern)}
	}
	if rule.requireMarker != "" && !strings.Contains(strings.ToLower(title), rule.requireMarker) {
		return Hint{}
	}
	return Hint{Ticket: ticketid.Extract(stripURLs(title), pattern)}
}

// stripURLs drops every whitespace-separated field that looks like a URL.
func stripURLs(text string) string {
	fields := strings.Fields(text)
	kept := fields[:0]
	for _, f := range fields {
		if !strings.Contains(f, "://") {
			kept = append(kept, f)
		}
	}
	return strings.Join(kept, " ")
}

var (
	jetBrainsSeparator = regexp.MustCompile(` [–—-] `)
	trailingBracket    = regexp.MustCompile(`\s*\[[^\]]*\]$`)
)

// jetBrainsProject handles "project – file" and "project [path] – file".
func jetBrainsProject(title string) string {
	first := jetBrainsSeparator.Split(title, 2)[0]
	return strings.TrimSpace(trailingBracket.ReplaceAllString(first, ""))
}

// genericVSCodeTitles are window titles of VS Code that do not name a project.
var genericVSCodeTitles = map[string]bool{
	"welcome": true, "get started": true, "settings": true, "untitled": true,
}

// vsCodeProject handles "file — project — app", "project — app" and the
// dirty marker prefix. The project is the second to last segment.
func vsCodeProject(title string) string {
	segments := strings.Split(title, " — ")
	if len(segments) < 2 {
		return ""
	}
	project := strings.TrimSpace(segments[len(segments)-2])
	if len(segments) == 2 {
		project = strings.TrimSpace(strings.TrimPrefix(project, "● "))
	}
	project = strings.TrimPrefix(project, "[Extension Development Host] ")
	project = strings.TrimSpace(strings.TrimSuffix(project, " (Workspace)"))
	if genericVSCodeTitles[strings.ToLower(project)] || strings.HasPrefix(strings.ToLower(project), "untitled-") {
		return ""
	}
	return project
}

// xcodeProject handles "Project — File.swift".
func xcodeProject(title string) string {
	return strings.TrimSpace(strings.SplitN(title, " — ", 2)[0])
}
