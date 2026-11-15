// Package suggest ranks the user's open YouTrack issues as proposals for time
// that has no ticket. A suggestion is only a proposal: nothing here books or
// attributes time. Rank is pure; fetching the issues is the caller's job.
package suggest

import (
	"sort"
	"strings"

	"github.com/JtheGunner/ticktack/internal/youtrack"
)

// MaxCandidates is the most candidates offered for one entry.
const MaxCandidates = 5

// Candidate is one proposed ticket.
type Candidate struct {
	Key     string
	Summary string
}

// Rank orders issues as candidates for an entry. The ranking is
// deterministic:
//
//  1. Issues whose key appears in hint (the repository path and branch of the
//     entry, matched case-insensitively on key boundaries) come first.
//  2. Then the most recently updated issue first.
//  3. Ties are broken by key.
//
// Issues without a key are dropped, duplicates are collapsed, and at most
// MaxCandidates are returned.
func Rank(issues []youtrack.Issue, hint string) []Candidate {
	hint = strings.ToUpper(hint)
	type scored struct {
		issue youtrack.Issue
		hit   bool
	}
	seen := map[string]bool{}
	var list []scored
	for _, is := range issues {
		key := strings.ToUpper(strings.TrimSpace(is.ID))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		is.ID = key
		list = append(list, scored{issue: is, hit: containsKey(hint, key)})
	}
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if a.hit != b.hit {
			return a.hit
		}
		if a.issue.Updated != b.issue.Updated {
			return a.issue.Updated > b.issue.Updated
		}
		return a.issue.ID < b.issue.ID
	})
	if len(list) > MaxCandidates {
		list = list[:MaxCandidates]
	}
	out := make([]Candidate, 0, len(list))
	for _, s := range list {
		out = append(out, Candidate{Key: s.issue.ID, Summary: s.issue.Summary})
	}
	return out
}

// containsKey reports whether key occurs in text not embedded in a longer
// key, so ABC-1 does not match ABC-12 or XABC-1.
func containsKey(text, key string) bool {
	for from := 0; ; {
		i := strings.Index(text[from:], key)
		if i < 0 {
			return false
		}
		start, end := from+i, from+i+len(key)
		if !isKeyChar(text, start-1) && !isKeyChar(text, end) {
			return true
		}
		from = start + 1
	}
}

func isKeyChar(text string, i int) bool {
	if i < 0 || i >= len(text) {
		return false
	}
	c := text[i]
	return c >= '0' && c <= '9' || c >= 'A' && c <= 'Z'
}
