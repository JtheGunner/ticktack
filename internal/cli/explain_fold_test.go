package cli

import (
	"strings"
	"testing"
)

func TestExplainFoldedMessageNamesTheRuleAndTheTicket(t *testing.T) {
	got := msgExplainFolded("09:20", "09:28", "BBB-2")
	for _, want := range []string{"09:20-09:28", "BBB-2", "between blocks of this ticket", "folded"} {
		if !strings.Contains(got, want) {
			t.Errorf("message %q lacks %q", got, want)
		}
	}
}
