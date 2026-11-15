// Package ticketid extracts YouTrack ticket IDs from free text such as branch names.
package ticketid

import (
	"regexp"
	"strings"

	"github.com/JtheGunner/ticktack/internal/config"
)

// DefaultPattern matches ticket IDs like PROJ-123, ignoring case. It is built
// from the configuration default so there is a single source of truth.
var DefaultPattern = regexp.MustCompile(`(?i)` + config.DefaultTicketPattern)

// Extract returns the first ticket ID found in text, upper-cased, or "" when
// there is none. A nil pattern means DefaultPattern.
func Extract(text string, pattern *regexp.Regexp) string {
	if pattern == nil {
		pattern = DefaultPattern
	}
	return strings.ToUpper(pattern.FindString(text))
}
