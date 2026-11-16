package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/JtheGunner/ticktack/internal/render"
	"github.com/JtheGunner/ticktack/internal/youtrack"
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
			name:   "init_unsupported_shell_plain",
			render: render.Plain(),
			opts:   initOptions{Yes: true, URL: "https://yt.example.com", Shell: "tcsh", SkipToken: true},
		},
		{
			name:   "init_config_path_error_plain",
			render: render.Plain(),
			opts:   initOptions{Yes: true, SkipToken: true},
			setup: func(env *testEnv) {
				env.deps.Doctor.ConfigPath = func() (string, error) { return "", errors.New("no config dir") }
			},
		},
		{
			// config yes + URL, token yes, validate yes (accepted), hook no, agent no
			name:    "init_token_validated_plain",
			answers: "y\nhttps://yt.example.com\ny\ny\nn\nn\n",
			render:  render.Plain(),
			setup: func(env *testEnv) {
				env.deps.Doctor.NewMe = func(string, string) (meClient, error) { return stubMe{login: "jane"}, nil }
			},
		},
		{
			// same answers, but YouTrack rejects the token
			name:    "init_token_rejected_plain",
			answers: "y\nhttps://yt.example.com\ny\ny\nn\nn\n",
			render:  render.Plain(),
			setup: func(env *testEnv) {
				env.deps.Doctor.NewMe = func(string, string) (meClient, error) { return stubMe{err: youtrack.ErrUnauthorized}, nil }
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
	run := func(text string) string {
		env := newTestEnv(t, "", "")
		env.deps.Render = unicodeColorRenderer()
		failure := errors.New(text)
		env.deps.Launchctl = func(context.Context, ...string) (string, error) { return "", failure }
		env.deps.Binary = func() (string, error) { return "", failure }
		var out strings.Builder
		opts := initOptions{Yes: true, SkipToken: true, SkipConfig: true, SkipHook: true}
		if err := runInit(context.Background(), env.deps, opts, &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	got := run("denied\x1b[2J\r\nfake\u202e")
	assertNoStrayControls(t, got)
	if !strings.Contains(stripANSI(got), "denied") {
		t.Errorf("error text lost: %q", got)
	}
	if want := strings.Count(run("deniedfake"), "\n"); strings.Count(got, "\n") != want {
		t.Errorf("hostile text added lines: %d, want %d:\n%q", strings.Count(got, "\n"), want, got)
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

// stubMe answers the token validation without a network call.
type stubMe struct {
	login string
	err   error
}

func (s stubMe) Me(context.Context) (string, error) { return s.login, s.err }
