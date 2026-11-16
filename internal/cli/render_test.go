package cli

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/JtheGunner/ticktack/internal/render"
	"github.com/JtheGunner/ticktack/internal/resolver"
)

func TestRootCommandHasNoColorFlag(t *testing.T) {
	root := NewRootCommand()
	flag := root.PersistentFlags().Lookup(noColorFlag)
	if flag == nil {
		t.Fatal("root command lacks the --no-color flag")
	}
	for _, sub := range []string{"status", "today"} {
		cmd, _, err := root.Find([]string{sub})
		if err != nil {
			t.Fatal(err)
		}
		if cmd.InheritedFlags().Lookup(noColorFlag) == nil {
			t.Errorf("%s does not inherit --no-color", sub)
		}
	}
}

func TestApplyNoColorFlag(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		initial bool
		want    bool
	}{
		{"flag absent", nil, false, false},
		{"flag set", []string{"--no-color"}, false, true},
		{"option already set stays set", nil, true, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := NewRootCommand()
			root.SetArgs(append([]string{"version"}, tc.args...))
			var seen render.Options
			root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
				seen = applyNoColorFlag(cmd, render.Options{NoColor: tc.initial})
				return nil
			}
			root.SetOut(&bytes.Buffer{})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if seen.NoColor != tc.want {
				t.Errorf("NoColor = %v, want %v", seen.NoColor, tc.want)
			}
		})
	}
}

func TestApplyNoColorFlagWithoutFlagDefined(t *testing.T) {
	got := applyNoColorFlag(&cobra.Command{Use: "bare"}, render.Options{})
	if got.NoColor {
		t.Error("a command without the flag must not disable colour")
	}
}

// forcedTerminal makes render.New treat any writer as a terminal.
func forcedTerminal(io.Writer) (int, bool) { return 1, true }

// TestNoColorOnForcedTerminal drives a real command tree through rendererWith
// with a forced terminal, so only the flag and the injected env decide colour.
func TestNoColorOnForcedTerminal(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		env        map[string]string
		wantColour bool
	}{
		{"colour by default", nil, nil, true},
		{"--no-color", []string{"--no-color"}, nil, false},
		{"NO_COLOR", nil, map[string]string{"NO_COLOR": "1"}, false},
		{"TERM=dumb", nil, map[string]string{"TERM": "dumb"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			root := &cobra.Command{Use: "ticktack", SilenceUsage: true, SilenceErrors: true}
			root.PersistentFlags().Bool(noColorFlag, false, msgNoColorUsage)
			root.AddCommand(&cobra.Command{
				Use: "probe",
				RunE: func(cmd *cobra.Command, _ []string) error {
					r := rendererWith(cmd, &bytes.Buffer{}, render.Options{
						Getenv:     func(k string) string { return tc.env[k] },
						IsTerminal: forcedTerminal,
						TermSize:   func(int) (int, int, error) { return 100, 40, nil },
					})
					got = r.Heading("x") + colorTier(r, resolver.TierExplicit)
					return nil
				},
			})
			root.SetArgs(append([]string{"probe"}, tc.args...))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if hasESC := strings.Contains(got, "\x1b"); hasESC != tc.wantColour {
				t.Errorf("output %q: ESC present = %v, want %v", got, hasESC, tc.wantColour)
			}
		})
	}
}

func TestColorTier(t *testing.T) {
	colored := render.Renderer{Colors: render.Colors{Enabled: true}}
	tests := []struct {
		tier resolver.Tier
		want string
	}{
		{resolver.TierExplicit, "\x1b[32mexplicit\x1b[0m"},
		{resolver.TierRule, "\x1b[32mrule\x1b[0m"},
		{resolver.TierLearned, "\x1b[36mlearned\x1b[0m"},
		{resolver.TierSuggested, "\x1b[33msuggested\x1b[0m"},
		{resolver.TierUnknown, "\x1b[2munknown\x1b[0m"},
	}
	for _, tc := range tests {
		t.Run(string(tc.tier), func(t *testing.T) {
			if got := colorTier(colored, tc.tier); got != tc.want {
				t.Errorf("colorTier = %q, want %q", got, tc.want)
			}
			if got := colorTier(render.Plain(), tc.tier); got != string(tc.tier) {
				t.Errorf("plain colorTier = %q, want %q", got, tc.tier)
			}
		})
	}
	if strings.Contains(colorTier(render.Plain(), resolver.TierRule), "\x1b") {
		t.Error("plain output contains ESC")
	}
}
