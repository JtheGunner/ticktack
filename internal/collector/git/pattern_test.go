package git

import (
	"regexp"
	"testing"
	"time"
)

func TestBuildEventsHonoursTicketPattern(t *testing.T) {
	isBranch := func(string) bool { return true }
	entries := []reflogEntry{{
		At: time.Unix(10, 0).UTC(), Hash: "h", Subject: "checkout: moving from main to feat/ABC_7-login",
	}}
	custom := regexp.MustCompile(`[A-Z]+_\d+`)

	defaulted := buildEvents("/r", entries, "main", isBranch, nil)
	configured := buildEvents("/r", entries, "main", isBranch, custom)
	if len(defaulted) != 1 || len(configured) != 1 {
		t.Fatalf("events = %d and %d, want 1 each", len(defaulted), len(configured))
	}
	if defaulted[0].Ticket != "" {
		t.Errorf("default pattern ticket = %q, want none", defaulted[0].Ticket)
	}
	if configured[0].Ticket != "ABC_7" {
		t.Errorf("custom pattern ticket = %q, want ABC_7", configured[0].Ticket)
	}
	if defaulted[0].Key != configured[0].Key {
		t.Errorf("key depends on the pattern: %q vs %q", defaulted[0].Key, configured[0].Key)
	}
}

func TestBuildEventsDefaultPatternIsCaseInsensitive(t *testing.T) {
	isBranch := func(string) bool { return true }
	entries := []reflogEntry{{
		At: time.Unix(10, 0).UTC(), Hash: "h", Subject: "checkout: moving from main to feat/abc-12-login",
	}}
	got := buildEvents("/r", entries, "main", isBranch, nil)
	if len(got) != 1 || got[0].Ticket != "ABC-12" {
		t.Fatalf("events = %+v, want ticket ABC-12", got)
	}
}
