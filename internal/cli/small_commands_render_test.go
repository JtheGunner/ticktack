package cli

import (
	"bytes"
	"context"
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/render"
)

// smallRenderers are the two modes every small command is checked in.
func smallRenderers() []struct {
	name   string
	r      render.Renderer
	colour bool
} {
	return []struct {
		name   string
		r      render.Renderer
		colour bool
	}{
		{"plain", render.Plain(), false},
		{"colour", unicodeColorRenderer(), true},
	}
}

// wantLine builds the expected gutter line for status s in renderer r with
// its styling removed.
func wantLine(r render.Renderer, s render.Status, text string) string {
	plain := r
	plain.Colors = render.Colors{}
	return plain.Gutter(s, text) + "\n"
}

// assertSmallOutput checks that out reads as want once styling is removed and
// that ESC bytes are present exactly when colour is on.
func assertSmallOutput(t *testing.T, out string, want string, colour bool) {
	t.Helper()
	if got := stripANSI(out); got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
	if has := countESC(out) > 0; has != colour {
		t.Errorf("ESC present = %v, want %v: %q", has, colour, out)
	}
}

func TestPauseResumeOutput(t *testing.T) {
	for _, m := range smallRenderers() {
		t.Run(m.name, func(t *testing.T) {
			deps, _, out := newDailyEnv(t, "")
			deps.Render = m.r
			if err := runPause(deps); err != nil {
				t.Fatal(err)
			}
			assertSmallOutput(t, out.String(), wantLine(m.r, render.StatusWarn, msgPaused), m.colour)
			out.Reset()
			if err := runResume(deps); err != nil {
				t.Fatal(err)
			}
			assertSmallOutput(t, out.String(), wantLine(m.r, render.StatusOK, msgResumed), m.colour)
		})
	}
}

func TestPurgeOutput(t *testing.T) {
	ctx := context.Background()
	for _, m := range smallRenderers() {
		t.Run(m.name+"/done", func(t *testing.T) {
			deps, _, out := newDailyEnv(t, "")
			deps.Render = m.r
			if err := runPurge(ctx, deps, true); err != nil {
				t.Fatal(err)
			}
			assertSmallOutput(t, out.String(), wantLine(m.r, render.StatusOK, msgPurged), m.colour)
		})
		t.Run(m.name+"/aborted keeps the prompt text", func(t *testing.T) {
			deps, _, out := newDailyEnv(t, "n\n")
			deps.Render = m.r
			if err := runPurge(ctx, deps, false); err != nil {
				t.Fatal(err)
			}
			want := msgPurgePrompt + wantLine(m.r, render.StatusInfo, msgPurgeAborted)
			assertSmallOutput(t, out.String(), want, m.colour)
			if !strings.HasPrefix(out.String(), msgPurgePrompt) {
				t.Errorf("prompt is not printed verbatim and unstyled: %q", out)
			}
		})
	}
}

func TestPurgePromptTextIsFixed(t *testing.T) {
	const want = "Delete ALL local ticktack data (events, overrides, ledger)? [y/N] "
	if msgPurgePrompt != want {
		t.Errorf("prompt = %q, want %q", msgPurgePrompt, want)
	}
}

func switchDeps(t *testing.T, pattern string) (dailyDeps, *bytes.Buffer) {
	t.Helper()
	deps, _, out := newDailyEnv(t, "")
	inner := deps.LoadConfig
	deps.LoadConfig = func() (config.Config, error) {
		cfg, err := inner()
		cfg.TicketPattern = regexp.MustCompile(pattern)
		return cfg, err
	}
	return deps, out
}

func TestSwitchOutput(t *testing.T) {
	ctx := context.Background()
	for _, m := range smallRenderers() {
		t.Run(m.name+"/ticket", func(t *testing.T) {
			deps, _, out := newDailyEnv(t, "")
			deps.Render = m.r
			if err := runSwitch(ctx, deps, []string{"proj-12"}, false); err != nil {
				t.Fatal(err)
			}
			assertSmallOutput(t, out.String(), wantLine(m.r, render.StatusOK, msgSwitched("PROJ-12")), m.colour)
		})
		t.Run(m.name+"/cleared", func(t *testing.T) {
			deps, _, out := newDailyEnv(t, "")
			deps.Render = m.r
			if err := runSwitch(ctx, deps, []string{"auto"}, false); err != nil {
				t.Fatal(err)
			}
			assertSmallOutput(t, out.String(), wantLine(m.r, render.StatusInfo, msgSwitchCleared), m.colour)
		})
	}
}

func TestSwitchSanitisesHostileTicket(t *testing.T) {
	ctx := context.Background()
	// A permissive ticket pattern lets hostile text pass validation, so the
	// echo must neutralise it itself.
	const hostile = "bad\x1b[2Jtext\nFAKE\x07\r\u202eend"
	for _, m := range smallRenderers() {
		t.Run(m.name, func(t *testing.T) {
			run := func(arg string) string {
				deps, out := switchDeps(t, `(?s).+`)
				deps.Render = m.r
				if err := runSwitch(ctx, deps, []string{arg}, false); err != nil {
					t.Fatal(err)
				}
				return out.String()
			}
			got, benign := run(hostile), run("benign")
			if countESC(got) != countESC(benign) {
				t.Errorf("hostile output has %d ESC bytes, benign %d: %q", countESC(got), countESC(benign), got)
			}
			if !strings.Contains(got, "BAD[2JTEXT FAKE") {
				t.Errorf("hostile text did not reach the output: %q", got)
			}
			if strings.Count(got, "\n") != strings.Count(benign, "\n") {
				t.Errorf("hostile output has %d lines, benign %d: %q", strings.Count(got, "\n"), strings.Count(benign, "\n"), got)
			}
			for _, c := range []string{"\r", "\a", "\u202e"} {
				if strings.Contains(got, c) {
					t.Errorf("control character %q survives: %q", c, got)
				}
			}
		})
	}
}

func TestUndoOutput(t *testing.T) {
	ctx := context.Background()
	for _, m := range smallRenderers() {
		t.Run(m.name+"/nothing", func(t *testing.T) {
			deps, _ := syncTestDeps(t)
			deps.Render = m.r
			var out bytes.Buffer
			if err := runUndo(ctx, &out, deps); err != nil {
				t.Fatal(err)
			}
			assertSmallOutput(t, out.String(), wantLine(m.r, render.StatusInfo, msgNothingToUndo), m.colour)
		})
		t.Run(m.name+"/counts", func(t *testing.T) {
			deps, rec := syncTestDeps(t)
			confirmDay(t, deps)
			var syncOut bytes.Buffer
			if err := runSync(ctx, &syncOut, deps, "2026-01-05", false); err != nil {
				t.Fatal(err)
			}
			if len(rec.items) != 1 {
				t.Fatalf("items = %d", len(rec.items))
			}
			deps.Render = m.r
			var out bytes.Buffer
			if err := runUndo(ctx, &out, deps); err != nil {
				t.Fatal(err)
			}
			assertSmallOutput(t, out.String(), wantLine(m.r, render.StatusOK, msgUndoResult(1, 0, 0)), m.colour)
		})
	}
}

func TestUndoCommandResolvesRenderer(t *testing.T) {
	deps, _ := syncTestDeps(t)
	deps.RenderFor = func(_ *cobra.Command, _ io.Writer) render.Renderer { return unicodeColorRenderer() }
	out, _, err := execCommand(t, deps, "undo")
	if err != nil {
		t.Fatal(err)
	}
	assertSmallOutput(t, out, wantLine(unicodeColorRenderer(), render.StatusInfo, msgNothingToUndo), true)
}
