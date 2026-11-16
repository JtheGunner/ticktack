// Package ticketid extracts YouTrack ticket IDs from free text such as branch names.
package ticketid

import (
	"regexp"
	"strings"

	"github.com/JtheGunner/ticktack/internal/config"
)

// DefaultPattern matches ticket IDs like PROJ-123. It is case-sensitive and
// identical to the configuration default, so a nil pattern behaves exactly
// like an unconfigured ticket_pattern. A case-insensitive match would read
// branches such as "fix/utf-8-handling" as ticket UTF-8.
var DefaultPattern = regexp.MustCompile(config.DefaultTicketPattern)

// Extract returns the first match of pattern in text, upper-cased, or "" when
// there is none. The pattern is applied exactly as given (the configured
// ticket_pattern), so every caller agrees on what a ticket is. A nil pattern
// means DefaultPattern.
func Extract(text string, pattern *regexp.Regexp) string {
	if pattern == nil {
		pattern = DefaultPattern
	}
	return strings.ToUpper(pattern.FindString(text))
}
