package cli

import (
	"testing"

	"github.com/JtheGunner/ticktack/internal/sessionizer"
)

func TestFormatCertainty(t *testing.T) {
	tests := []struct {
		name string
		in   sessionizer.Certainty
		want string
	}{
		{"single tier", sessionizer.Certainty{Explicit: 60}, "explicit 1h"},
		{"sure and carried", sessionizer.Certainty{Rule: 40, Suggested: 50, Carried: 50}, "rule 40m, suggested 50m (50m carried)"},
		{"fresh suggestion only", sessionizer.Certainty{Suggested: 20}, "suggested 20m"},
		{"part of suggested carried", sessionizer.Certainty{Suggested: 45, Carried: 15, Unknown: 5}, "suggested 45m (15m carried), unknown 5m"},
		{"all tiers", sessionizer.Certainty{Explicit: 1, Rule: 2, Learned: 3, Suggested: 4, Unknown: 5}, "explicit 1m, rule 2m, learned 3m, suggested 4m, unknown 5m"},
		{"empty", sessionizer.Certainty{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatCertainty(tt.in); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
