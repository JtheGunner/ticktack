package cli

import (
	"strings"
	"testing"

	"github.com/JtheGunner/ticktack/internal/render"
)

func unicodeColorRenderer() render.Renderer {
	return render.Renderer{
		Colors: render.Colors{Enabled: true},
		Glyphs: render.GlyphsFromEnv(func(k string) string {
			if k == "LANG" {
				return "en_US.UTF-8"
			}
			return ""
		}),
		Width: 70,
	}
}

func TestDoctorReportGolden(t *testing.T) {
	allPass := []checkResult{
		okResult("config", "valid: /home/u/.config/ticktack/config.toml"),
		okResult("token", "valid (user jane)"),
		okResult("database", "writable: /home/u/.local/share/ticktack/ticktack.db"),
	}
	partial := []checkResult{
		okResult("config", "valid: /home/u/.config/ticktack/config.toml"),
		failResult("token", "no token in the keychain", "run `ticktack init` and enter the token"),
		skipResult("agent", "launchd is macOS-only"),
		failResult("youtrack", "https://yt.example.com is not reachable: timeout", "check network, VPN and youtrack_url"),
	}
	skips := []checkResult{
		okResult("config", "valid: /home/u/.config/ticktack/config.toml"),
		skipResult("activitywatch", "disabled"),
		skipResult("window title hints", "disabled (set enabled = true under [titlehint] to turn on)"),
	}
	warns := []checkResult{
		okResult("config", "valid: /home/u/.config/ticktack/config.toml"),
		warnResult("shell hook", "installed in /home/u/.zshrc, but no shell event in the last 24h", "open a new shell, cd into a git repository and press enter"),
		skipResult("activitywatch", "disabled"),
	}
	tests := []struct {
		name       string
		results    []checkResult
		render     render.Renderer
		wantFailed int
		wantColour string
	}{
		{"doctor_all_pass_plain", allPass, render.Plain(), 0, ""},
		{"doctor_partial_fail_plain", partial, render.Plain(), 2, ""},
		{"doctor_skips_plain", skips, render.Plain(), 0, ""},
		{"doctor_warnings_plain", warns, render.Plain(), 0, ""},
		{"doctor_all_pass_unicode_color", allPass, unicodeColorRenderer(), 0, "\x1b[32mAll 3 checks passed."},
		{"doctor_partial_fail_unicode_color", partial, unicodeColorRenderer(), 2, "\x1b[31m2 of 4 checks failed."},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out strings.Builder
			if got := writeDoctorReport(&out, tc.render, tc.results); got != tc.wantFailed {
				t.Errorf("failed = %d, want %d", got, tc.wantFailed)
			}
			got := out.String()
			assertGolden(t, tc.name, got)
			if hasESC := strings.Contains(got, "\x1b"); hasESC != tc.render.Colors.Enabled {
				t.Errorf("ESC present = %v, colour enabled = %v", hasESC, tc.render.Colors.Enabled)
			}
			if tc.wantColour != "" && !strings.Contains(got, tc.wantColour) {
				t.Errorf("summary is not coloured as %q:\n%q", tc.wantColour, got)
			}
		})
	}
}

func TestDoctorReportSanitisesBeforeStyling(t *testing.T) {
	hostile := "boom\x1b[2J\r\nfake line\x07\u202e"
	benign := "boomfake line"
	render1 := func(r render.Renderer, text string) string {
		var out strings.Builder
		writeDoctorReport(&out, r, []checkResult{failResult("youtrack", "server said: "+text, "fix it"+text)})
		return out.String()
	}
	for _, r := range []render.Renderer{render.Plain(), unicodeColorRenderer()} {
		got := render1(r, hostile)
		assertNoStrayControls(t, got)
		if !strings.Contains(stripANSI(got), "server said: boom") {
			t.Errorf("detail lost:\n%q", got)
		}
		if want := strings.Count(render1(r, benign), "\n"); strings.Count(got, "\n") != want {
			t.Errorf("hostile text added lines: %d, want %d:\n%q", strings.Count(got, "\n"), want, got)
		}
	}
}
