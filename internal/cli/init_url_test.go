package cli

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/JtheGunner/ticktack/internal/config"
)

// TestInitAppliesLoaderURLRule checks that init writes only a config the
// loader accepts: the URL from --url or from the prompt must be https, with
// plain http accepted for a loopback host only.
func TestInitAppliesLoaderURLRule(t *testing.T) {
	tests := []struct {
		name    string
		flagURL string
		// answers feeds the prompts; used when flagURL is empty.
		answers string
		written bool
	}{
		{name: "https flag", flagURL: "https://yt.example.com", written: true},
		{name: "http loopback name flag", flagURL: "http://localhost:8080", written: true},
		{name: "http loopback address flag", flagURL: "http://127.0.0.1:8080", written: true},
		{name: "http loopback prompt", answers: "y\nhttp://[::1]:8080\n", written: true},
		{name: "empty prompt", answers: "y\n\n", written: true},
		{name: "http non-loopback flag", flagURL: "http://yt.example.com"},
		{name: "http non-loopback prompt", answers: "y\nhttp://yt.example.com\n"},
		{name: "http private address flag", flagURL: "http://192.168.1.2:8080"},
		{name: "http loopback lookalike flag", flagURL: "http://localhost.evil.example"},
		{name: "ftp flag", flagURL: "ftp://yt.example.com"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t, "", tc.answers)
			opts := initOptions{
				Yes:       tc.flagURL != "",
				URL:       tc.flagURL,
				SkipToken: true,
				SkipHook:  true,
				SkipAgent: true,
			}
			cfgPath, err := config.DefaultPath()
			if err != nil {
				t.Fatal(err)
			}

			var out bytes.Buffer
			if err := runInit(context.Background(), env.deps, opts, &out); err != nil {
				t.Fatal(err)
			}

			if tc.written {
				if !strings.Contains(out.String(), "config written") {
					t.Errorf("config must be reported as written:\n%s", out.String())
				}
				if _, err := config.Load(cfgPath); err != nil {
					t.Errorf("written config does not load: %v", err)
				}
				return
			}
			if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
				t.Errorf("config file must not exist, stat error = %v", err)
			}
			if strings.Contains(out.String(), "config written") {
				t.Errorf("config must not be reported as written:\n%s", out.String())
			}
			if !strings.Contains(out.String(), "config not written") || !strings.Contains(out.String(), "https://") {
				t.Errorf("output must name the https fix:\n%s", out.String())
			}
		})
	}
}

// TestInitURLRejectionDoesNotEchoCredentials checks that a rejected URL with
// embedded credentials is not printed back.
func TestInitURLRejectionDoesNotEchoCredentials(t *testing.T) {
	env := newTestEnv(t, "", "")
	opts := initOptions{
		Yes:       true,
		URL:       "http://user:sekrit@yt.example.com",
		SkipToken: true,
		SkipHook:  true,
		SkipAgent: true,
	}

	var out bytes.Buffer
	if err := runInit(context.Background(), env.deps, opts, &out); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(out.String(), "sekrit") {
		t.Errorf("output echoes the URL credentials:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "config not written") {
		t.Errorf("output must report the rejected URL:\n%s", out.String())
	}
}
