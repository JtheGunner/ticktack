package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/JtheGunner/ticktack/internal/render"
)

// scrubHome replaces the temp home so goldens do not depend on it.
func scrubHome(env *testEnv, s string) string {
	return strings.ReplaceAll(s, env.home, "<HOME>")
}

func TestInitGolden(t *testing.T) {
	tests := []struct {
		name    string
		answers string
		render  render.Renderer
		setup   func(env *testEnv)
		opts    initOptions
	}{
		{
			// config yes + URL, token yes, skip validation, hook yes, agent yes
			name:    "init_happy_plain",
			answers: "y\nhttps://yt.example.com\ny\nn\ny\ny\n",
			render:  render.Plain(),
		},
		{
			name:    "init_happy_unicode_color",
			answers: "y\nhttps://yt.example.com\ny\nn\ny\ny\n",
			render:  unicodeColorRenderer(),
		},
		{
			name:   "init_failures_plain",
			render: render.Plain(),
			opts:   initOptions{Yes: true, URL: "not a url"},
			setup: func(env *testEnv) {
				env.deps.SetToken = func(string) error { return errors.New("keychain locked") }
				env.deps.Launchctl = func(context.Context, ...string) (string, error) {
					return "boom", errors.New("exit status 5")
				}
			},
		},
		{
			name:    "init_skipped_plain",
			answers: "n\nn\nn\nn\n",
			render:  render.Plain(),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t, testSecret, tc.answers)
			env.deps.Render = tc.render
			if tc.setup != nil {
				tc.setup(env)
			}
			var out strings.Builder
			if err := runInit(context.Background(), env.deps, tc.opts, &out); err != nil {
				t.Fatal(err)
			}
			got := scrubHome(env, out.String())
			assertGolden(t, tc.name, got)
			if hasESC := strings.Contains(got, "\x1b"); hasESC != tc.render.Colors.Enabled {
				t.Errorf("ESC present = %v, colour enabled = %v", hasESC, tc.render.Colors.Enabled)
			}
			if strings.Contains(got, testSecret) {
				t.Errorf("token echoed:\n%s", got)
			}
		})
	}
}

func TestInitPromptsKeepTheirExactText(t *testing.T) {
	env := newTestEnv(t, testSecret, "y\nhttps://yt.example.com\ny\nn\nn\nn\n")
	env.deps.Render = unicodeColorRenderer()
	var out strings.Builder
	if err := runInit(context.Background(), env.deps, initOptions{}, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Proceed? [Y/n] ",
		"YouTrack base URL (empty to set it later): ",
		"Token (input hidden, empty to skip): ",
		"Validate the token against YouTrack now? [Y/n] ",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("prompt %q missing or restyled:\n%q", want, out.String())
		}
	}
}

func TestInitSanitisesBeforeStyling(t *testing.T) {
	env := newTestEnv(t, "", "")
	env.deps.Render = unicodeColorRenderer()
	hostile := errors.New("denied\x1b[2J\r\nfake")
	env.deps.Launchctl = func(context.Context, ...string) (string, error) { return "", hostile }
	env.deps.Binary = func() (string, error) { return "", hostile }
	var out strings.Builder
	opts := initOptions{Yes: true, SkipToken: true, SkipConfig: true, SkipHook: true}
	if err := runInit(context.Background(), env.deps, opts, &out); err != nil {
		t.Fatal(err)
	}
	stripped := stripANSI(out.String())
	if !strings.Contains(stripped, "denied") || strings.ContainsAny(strings.ReplaceAll(stripped, "\n", ""), "\x1b\r") {
		t.Errorf("unsafe or missing text: %q", out.String())
	}
}

func TestInitFinalLineGlyphReflectsFailures(t *testing.T) {
	tests := []struct {
		name    string
		answers string
		opts    initOptions
		setup   func(env *testEnv)
		want    string
	}{
		{
			name:    "nothing failed",
			answers: "n\nn\nn\nn\n",
			want:    "  +  " + msgInitFinished,
		},
		{
			name: "a step failed",
			opts: initOptions{Yes: true, URL: "https://yt.example.com"},
			setup: func(env *testEnv) {
				env.deps.SetToken = func(string) error { return errors.New("keychain locked") }
			},
			want: "  !  " + msgInitFinishedWithFailures,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t, testSecret, tc.answers)
			env.deps.Render = render.Plain()
			if tc.setup != nil {
				tc.setup(env)
			}
			var out strings.Builder
			if err := runInit(context.Background(), env.deps, tc.opts, &out); err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
			if got := lines[len(lines)-1]; got != tc.want {
				t.Errorf("final line %q, want %q", got, tc.want)
			}
		})
	}
}
