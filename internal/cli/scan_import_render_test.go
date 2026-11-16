package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/paths"
	"github.com/JtheGunner/ticktack/internal/render"
)

// fakeScanner returns two deterministic git events for repositories whose
// base name is not "broken"; "broken" fails with hostile server-like text.
func fakeScanner(_ context.Context, repo string, _, _ time.Time, _ *regexp.Regexp) ([]event.Event, error) {
	if filepath.Base(repo) == "broken" {
		return nil, errors.New(hostileLine)
	}
	at := time.Date(2026, 2, 25, 9, 0, 0, 0, time.UTC)
	var events []event.Event
	for i, branch := range []string{"main", "feature/ABC-1-x"} {
		events = append(events, event.Event{
			At: at.Add(time.Duration(i) * time.Hour), Source: "git", Kind: event.KindContext,
			Repo: repo, Branch: branch, Key: repo + "#" + branch,
		})
	}
	return events, nil
}

// scanRenderEnv makes a scan environment whose home holds one git directory
// per name in repos; the configuration lists them as ~/<name>.
func scanRenderEnv(t *testing.T, r render.Renderer, repos ...string) *scanEnv {
	t.Helper()
	var configured []string
	home := t.TempDir()
	for _, name := range repos {
		if err := os.MkdirAll(filepath.Join(home, name, ".git"), 0o700); err != nil {
			t.Fatal(err)
		}
		configured = append(configured, "~/"+name)
	}
	env := newScanEnv(t, configured...)
	env.home = home
	env.deps.Render = r
	return env
}

func (e *scanEnv) scanWith(scan repoScanner, since string) error {
	e.out.Reset()
	return runScan(context.Background(), e.deps, e.home, scan, since)
}

func TestScanGolden(t *testing.T) {
	tests := []struct {
		name     string
		repos    []string
		renderer render.Renderer
		wantErr  bool
	}{
		{"scan_ok_plain", []string{"alpha", "beta"}, render.Plain(), false},
		{"scan_partial_plain", []string{"alpha", "broken"}, render.Plain(), true},
		{"scan_partial_unicode_color", []string{"alpha", "broken"}, func() render.Renderer {
			r := unicodeColorRenderer()
			r.Width = 100
			return r
		}(), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := scanRenderEnv(t, tc.renderer, tc.repos...)
			err := env.scanWith(fakeScanner, "2026-02-23")
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, want error %v", err, tc.wantErr)
			}
			got := env.out.String()
			assertGolden(t, tc.name, got)
			assertColourIffEnabled(t, got, tc.renderer)
		})
	}
}

func TestScanPausedIsAWarningLine(t *testing.T) {
	env := scanRenderEnv(t, unicodeColorRenderer(), "alpha")
	pausePath, _ := env.deps.PausePath()
	if err := os.MkdirAll(filepath.Dir(pausePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pausePath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := env.scanWith(fakeScanner, ""); err != nil {
		t.Fatal(err)
	}
	if got := stripANSI(env.out.String()); got != "  ⚠  "+msgScanPaused+"\n" || countESC(env.out.String()) == 0 {
		t.Errorf("paused output = %q", env.out.String())
	}
}

func TestScanSanitisesBeforeStyling(t *testing.T) {
	// The scanner error text becomes the reason of the "broken" repository,
	// and the repository path itself carries the hostile text.
	for name, r := range map[string]render.Renderer{"plain": narrowPlain(200), "colour": func() render.Renderer {
		c := unicodeColorRenderer()
		c.Width = 200
		return c
	}()} {
		t.Run(name, func(t *testing.T) {
			run := func(pathName string) string {
				env := scanRenderEnv(t, r, "broken", pathName)
				// A configured path that does not exist is listed under its raw text.
				load := env.deps.LoadConfig
				env.deps.LoadConfig = func() (config.Config, error) {
					cfg, err := load()
					cfg.RepoPaths = append(cfg.RepoPaths, "~/missing-"+pathName)
					return cfg, err
				}
				if err := env.scanWith(fakeScanner, "2026-02-23"); err == nil {
					t.Fatal("expected the failed repository to fail the scan")
				}
				return env.out.String()
			}
			hostile, benign := run("ok-"+hostileLine), run("ok-benign")
			assertNoHostileEscape(t, hostile, benign)
		})
	}
}

func TestScanSummaryTextKept(t *testing.T) {
	env := scanRenderEnv(t, render.Plain(), "alpha", "beta")
	if err := env.scanWith(fakeScanner, "2026-02-23"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Scanned 2 of 2 repositories since 2026-02-23: 4 new events (4 found", "~/alpha: 2 events found", "~/beta: 2 events found"} {
		if !strings.Contains(env.out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, env.out.String())
		}
	}
}

func TestImportGolden(t *testing.T) {
	colour := unicodeColorRenderer()
	tests := []struct {
		name     string
		renderer render.Renderer
	}{
		{"import_plain", render.Plain()},
		{"import_unicode_color", colour},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			deps, _, out := newDailyEnv(t, "")
			enableActivityWatch(&deps)
			deps.Render = tc.renderer
			srv, _ := awTestServer(t)
			var called bool
			if err := runImportActivityWatch(context.Background(), deps, awSourceFor(srv, &called), "2026-03-02"); err != nil {
				t.Fatal(err)
			}
			assertGolden(t, tc.name, out.String())
			assertColourIffEnabled(t, out.String(), tc.renderer)
		})
	}
}

func TestImportPausedIsAWarningLine(t *testing.T) {
	deps, _, out := newDailyEnv(t, "")
	enableActivityWatch(&deps)
	deps.Render = unicodeColorRenderer()
	pausePath, _ := paths.PausePath()
	if err := os.MkdirAll(filepath.Dir(pausePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pausePath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	srv, _ := awTestServer(t)
	var called bool
	if err := runImportActivityWatch(context.Background(), deps, awSourceFor(srv, &called), ""); err != nil {
		t.Fatal(err)
	}
	if got := stripANSI(out.String()); got != "  ⚠  "+msgImportPaused+"\n" || countESC(out.String()) == 0 {
		t.Errorf("paused output = %q", out.String())
	}
}

// The import lines hold only counts and a timestamp; the one place where
// server text reaches the terminal is the partial-failure error, which is
// sanitised before it is wrapped.
func TestImportPartialErrorIsSanitised(t *testing.T) {
	deps, _, out := newDailyEnv(t, "")
	enableActivityWatch(&deps)
	deps.Render = unicodeColorRenderer()
	srv := maliciousAWServer(t)
	var called bool
	err := runImportActivityWatch(context.Background(), deps, awSourceFor(srv, &called), "")
	if err == nil || countESC(err.Error()) != 0 || strings.Contains(err.Error(), "\n") {
		t.Fatalf("error text is not sanitised: %v", err)
	}
	if strings.Contains(stripANSI(out.String()), "\x1b") {
		t.Errorf("hostile ESC reached the output: %q", out.String())
	}
}
