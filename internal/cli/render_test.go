package cli

import (
	"bytes"
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

func TestRendererWithBufferIsPlain(t *testing.T) {
	env := map[string]string{"LANG": "en_US.UTF-8", "TERM": "xterm-256color"}
	opts := render.Options{Getenv: func(k string) string { return env[k] }}
	r := rendererWith(&cobra.Command{Use: "bare"}, &bytes.Buffer{}, opts)
	if r.Colors.Enabled {
		t.Error("colour must be off for a non-terminal writer")
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
