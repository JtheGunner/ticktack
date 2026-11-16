package ticketid

import (
	"regexp"
	"testing"
)

func TestExtract(t *testing.T) {
	custom := regexp.MustCompile(`ABC-\d+`)
	tests := []struct {
		name    string
		branch  string
		pattern *regexp.Regexp
		want    string
	}{
		{"upper case", "feature/PROJ-123-foo", nil, "PROJ-123"},
		{"lower case is not a ticket", "fix/proj-12", nil, ""},
		{"lower case branch like utf-8", "fix/utf-8-handling", nil, ""},
		{"upper case after lower case prefix", "feat/ABC-12-x", nil, "ABC-12"},
		{"custom lower case pattern is upper-cased", "x/abc-5", regexp.MustCompile(`abc-\d+`), "ABC-5"},
		{"first of several", "fix/PROJ-1-and-PROJ-2", nil, "PROJ-1"},
		{"no ticket", "main", nil, ""},
		{"empty branch", "", nil, ""},
		{"digits in key", "feat/AB2-7-x", nil, "AB2-7"},
		{"custom pattern", "x/ABC-5", custom, "ABC-5"},
		{"custom pattern ignores default match", "feature/PROJ-123", custom, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Extract(tt.branch, tt.pattern); got != tt.want {
				t.Errorf("Extract(%q) = %q, want %q", tt.branch, got, tt.want)
			}
		})
	}
}
