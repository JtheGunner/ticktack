package cli

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"unicode/utf8"

	"cloud.google.com/go/civil"
	"github.com/spf13/cobra"

	"github.com/JtheGunner/ticktack/internal/auto"
	"github.com/JtheGunner/ticktack/internal/ledger"
	"github.com/JtheGunner/ticktack/internal/render"
	"github.com/JtheGunner/ticktack/internal/resolver"
)

// hostileLine is external text that tries to clear the screen, ring the bell,
// start a new line, return the cursor and flip the text direction.
const hostileLine = "bad\x1b[2Jtext\nFAKE LINE\x07\r\u202eend"

// hostileShown is how hostileLine reads once it is sanitised.
const hostileShown = "bad[2Jtext FAKE LINE end"

func countESC(s string) int { return strings.Count(s, "\x1b") }

// assertNoHostileEscape checks a rendering of hostile text against the same
// rendering of benign text: the ESC count must be identical (only our own
// styling), the hostile text must have reached the output as inert text, and
// no other control character may survive.
func assertNoHostileEscape(t *testing.T, hostile, benign string) {
	t.Helper()
	if got, want := countESC(hostile), countESC(benign); got != want {
		t.Errorf("hostile output has %d ESC bytes, benign has %d:\n%q", got, want, hostile)
	}
	if !strings.Contains(hostile, hostileShown) {
		t.Errorf("hostile text did not reach the output (test would be vacuous):\n%q", hostile)
	}
	for _, r := range strings.ReplaceAll(strings.ReplaceAll(hostile, "\n", ""), "\x1b", "") {
		if r == '\r' || r == '\a' || r == '\u202e' {
			t.Errorf("control character %q survives:\n%q", r, hostile)
		}
	}
	if got, want := strings.Count(hostile, "\n"), strings.Count(benign, "\n"); got != want {
		t.Errorf("hostile output has %d lines, benign has %d:\n%q", got, want, hostile)
	}
}

// assertNoStrayControls fails when out holds anything but our own SGR styling
// and line feeds: no ESC, CR, BEL, other C0/C1 control, DEL or bidi override.
// Line feeds are checked separately, by comparing line counts with a benign
// rendering, because they are legitimate between lines.
func assertNoStrayControls(t *testing.T, out string) {
	t.Helper()
	stripped := stripANSI(out)
	if hasControlRune(stripped) {
		t.Errorf("control characters survive in %q", out)
	}
	for _, r := range stripped {
		if r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 {
			t.Errorf("bidi control %U survives in %q", r, out)
		}
	}
}

func sampleReport() ledger.Report {
	return ledger.Report{
		Items: []ledger.ItemResult{
			{Key: "ABC-1:2026-01-05:Development", Action: ledger.ActionCreated, Minutes: 60},
			{Key: "ABC-2:2026-01-05:Development", Action: ledger.ActionUpdated, Minutes: 30},
			{Key: "ABC-3:2026-01-05:Development", Action: ledger.ActionSkipped, Minutes: 15, Reason: "unchanged"},
			{Key: "ABC-4:2026-01-05:Development", Action: ledger.ActionFailed, Minutes: 45, Error: "HTTP 500"},
			{Key: "ABC-5:2026-01-05:Development", Action: ledger.ActionNotSent, Minutes: 15, Reason: "run stopped"},
		},
		Created: 1, Updated: 1, Skipped: 1, Failed: 1, NotSent: 1,
	}
}

func samplePlan() ledger.Report {
	return ledger.Report{
		Items: []ledger.ItemResult{
			{Key: "ABC-1:2026-01-05:Development", Action: ledger.ActionWouldCreate, Minutes: 60},
			{Key: "ABC-3:2026-01-05:Development", Action: ledger.ActionSkipped, Minutes: 15, Reason: "unchanged"},
		},
		Skipped: 1, Planned: 1,
	}
}

func TestSyncReportGolden(t *testing.T) {
	clean := ledger.Report{
		Items:   []ledger.ItemResult{{Key: "ABC-1:2026-01-05:Development", Action: ledger.ActionCreated, Minutes: 60}},
		Created: 1,
	}
	tests := []struct {
		name     string
		report   ledger.Report
		plan     bool
		renderer render.Renderer
		wantSGR  string
	}{
		{"sync_report_plain", sampleReport(), false, render.Plain(), ""},
		{"sync_plan_plain", samplePlan(), true, render.Plain(), ""},
		{"sync_report_unicode_color", sampleReport(), false, unicodeColorRenderer(), "\x1b[31mSkipped 1, failed 1"},
		{"sync_clean_unicode_color", clean, false, unicodeColorRenderer(), "\x1b[32mSkipped 0, failed 0"},
		{"sync_plan_unicode_color", samplePlan(), true, unicodeColorRenderer(), "\x1b[32mPlan: 1 to change"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			printReport(&out, tc.renderer, tc.report, tc.plan)
			assertGolden(t, tc.name, out.String())
			assertColourIffEnabled(t, out.String(), tc.renderer)
			if tc.wantSGR != "" && !strings.Contains(out.String(), tc.wantSGR) {
				t.Errorf("summary is not styled as %q:\n%q", tc.wantSGR, out.String())
			}
		})
	}
}

func TestSyncReportEmptyAndZeroValueRenderer(t *testing.T) {
	var out bytes.Buffer
	printReport(&out, render.Renderer{}, ledger.Report{}, false)
	if got, want := out.String(), "  i  No time to book.\n"; got != want {
		t.Errorf("empty report = %q, want %q", got, want)
	}
}

func TestSyncReportSanitisesBeforeStyling(t *testing.T) {
	build := func(text string) ledger.Report {
		return ledger.Report{Items: []ledger.ItemResult{
			{Key: "K-1:" + text, Action: ledger.ActionSkipped, Minutes: 15, Reason: text},
			{Key: "K-2", Action: ledger.ActionFailed, Minutes: 15, Error: text},
		}, Skipped: 1, Failed: 1}
	}
	for name, r := range map[string]render.Renderer{"plain": render.Plain(), "colour": unicodeColorRenderer()} {
		t.Run(name, func(t *testing.T) {
			var hostile, benign bytes.Buffer
			printReport(&hostile, r, build(hostileLine), false)
			printReport(&benign, r, build("benign"), false)
			assertNoHostileEscape(t, hostile.String(), benign.String())
			if r.Colors.Enabled != (countESC(hostile.String()) > 0) {
				t.Errorf("ESC present must follow colour enabled = %v", r.Colors.Enabled)
			}
		})
	}
}

// TestSyncCommandColourEndToEnd runs a real sync with a colour renderer in the
// deps and checks the styled report while the booking is unchanged.
func TestSyncCommandColourEndToEnd(t *testing.T) {
	deps, rec := syncTestDeps(t)
	confirmDay(t, deps)
	deps.Render = unicodeColorRenderer()
	var out bytes.Buffer
	if err := runSync(context.Background(), &out, deps, "2026-01-05", false); err != nil {
		t.Fatal(err)
	}
	if len(rec.items) != 1 || !strings.Contains(stripANSI(out.String()), "Created 1") || countESC(out.String()) == 0 {
		t.Fatalf("items=%d output:\n%q", len(rec.items), out.String())
	}
}

func TestSyncForCommandResolvesRenderer(t *testing.T) {
	deps := defaultSyncDeps()
	if deps.RenderFor == nil {
		t.Fatal("default deps must resolve their renderer from the command")
	}
	called := false
	deps.RenderFor = func(*cobra.Command, io.Writer) render.Renderer {
		called = true
		return unicodeColorRenderer()
	}
	got := deps.forCommand(newSyncCommand())
	if !called || !got.Render.Colors.Enabled {
		t.Errorf("forCommand did not apply RenderFor: %+v", got.Render)
	}
	hand := syncDeps{Render: unicodeColorRenderer()}
	if !hand.forCommand(newSyncCommand()).Render.Colors.Enabled {
		t.Error("hand-built deps without RenderFor must keep their Render")
	}
}

func sampleDigest(entries ...auto.DigestEntry) auto.Digest {
	day := civil.Date{Year: 2026, Month: 1, Day: 5}
	return auto.Digest{
		Day: day, Entries: entries,
		Created: 1, Skipped: 1, Failed: 1, BookedMinutes: 60,
	}
}

func digestEntries() []auto.DigestEntry {
	day := civil.Date{Year: 2026, Month: 1, Day: 5}
	return []auto.DigestEntry{
		{Ticket: "ABC-1", Date: day, Minutes: 60, Tier: resolver.TierExplicit, Status: "created"},
		{Ticket: "ABC-2", Date: day, Minutes: 90, Tier: resolver.TierLearned, Status: "skipped", Reason: "tier learned is below rule: needs review"},
		{Ticket: "ABC-3", Date: day, Minutes: 30, Tier: resolver.TierRule, Status: "failed", Reason: "HTTP 500"},
	}
}

func TestDigestGolden(t *testing.T) {
	changed := sampleDigest(digestEntries()...)
	dry := changed
	dry.DryRun = true
	capped := auto.Digest{Day: civil.Date{Year: 2026, Month: 1, Day: 5}, CapExceeded: true, TotalMinutes: 600, CapMinutes: 480}
	tests := []struct {
		name     string
		digest   auto.Digest
		renderer render.Renderer
	}{
		{"digest_plain", changed, narrowPlain(100)},
		{"digest_dry_run_plain", dry, narrowPlain(100)},
		{"digest_capped_plain", capped, render.Plain()},
		{"digest_unicode_color", changed, unicodeColorRenderer()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := renderDigest(tc.digest, tc.renderer)
			assertGolden(t, tc.name, got+"\n")
			assertColourIffEnabled(t, got, tc.renderer)
		})
	}
}

func TestDigestTableFitsNarrowWidth(t *testing.T) {
	got := renderDigest(sampleDigest(digestEntries()...), narrowPlain(60))
	assertGolden(t, "digest_narrow_plain", got+"\n")
}

// TestDigestKeepsLongReasonsWhole fails when a reason is cut: the reason must
// appear in full in the screen output, on its own indented lines when it does
// not fit the table, and no line of the table part may exceed the width.
func TestDigestKeepsLongReasonsWhole(t *testing.T) {
	day := civil.Date{Year: 2026, Month: 1, Day: 5}
	reasons := []string{
		"tier learned is below rule: needs review before this item can be booked automatically",
		"HTTP 500 from the server while updating the work item, will be retried on the next run",
		"short",
	}
	var entries []auto.DigestEntry
	for i, reason := range reasons {
		entries = append(entries, auto.DigestEntry{
			Ticket: "ABC-" + string(rune('1'+i)), Date: day, Minutes: 15, Tier: resolver.TierLearned,
			Status: "skipped", Reason: reason,
		})
	}
	colour := func(width int) render.Renderer {
		r := unicodeColorRenderer()
		r.Width = width
		return r
	}
	for name, r := range map[string]render.Renderer{
		"plain 78 (default)": render.Plain(),
		"plain 60":           narrowPlain(60),
		"colour 78":          colour(78),
		"colour 60":          colour(60),
	} {
		t.Run(name, func(t *testing.T) {
			got := renderDigest(sampleDigest(entries...), r)
			// Words may be wrapped onto several lines, so compare the text
			// with all whitespace runs collapsed.
			flat := strings.Join(strings.Fields(stripANSI(got)), " ")
			for _, reason := range reasons {
				if !strings.Contains(flat, reason) {
					t.Errorf("reason %q was cut:\n%s", reason, got)
				}
			}
			if strings.Contains(got, "...") || strings.Contains(got, "\u2026") {
				t.Errorf("output contains an ellipsis:\n%s", got)
			}
			for _, line := range strings.Split(got, "\n") {
				if strings.Contains(line, "Totals") || strings.Contains(line, "Undo") {
					continue
				}
				if n := utf8.RuneCountInString(stripANSI(line)); n > r.LayoutWidth() {
					t.Errorf("line is %d columns, limit %d: %q", n, r.LayoutWidth(), line)
				}
			}
		})
	}
}

func TestDigestStoredTextIsPlainAndWhole(t *testing.T) {
	long := strings.Repeat("reason ", 30)
	d := sampleDigest(auto.DigestEntry{Ticket: "ABC-1", Date: civil.Date{Year: 2026, Month: 1, Day: 5}, Minutes: 15, Status: "skipped", Reason: long})
	text := renderDigest(d, storedDigestRenderer())
	if strings.Contains(text, "\x1b") || strings.Contains(text, "...") || !strings.Contains(text, strings.TrimSpace(long)) {
		t.Errorf("stored digest must be plain ASCII and keep long reasons whole:\n%q", text)
	}
}

func TestDigestSanitisesBeforeStyling(t *testing.T) {
	build := func(text string) auto.Digest {
		return sampleDigest(auto.DigestEntry{
			Ticket: "ABC-1" + text, Date: civil.Date{Year: 2026, Month: 1, Day: 5}, Minutes: 15,
			Tier: resolver.TierRule, Status: "failed", Reason: text,
		})
	}
	for name, r := range map[string]render.Renderer{"plain": narrowPlain(200), "colour": func() render.Renderer {
		c := unicodeColorRenderer()
		c.Width = 200
		return c
	}()} {
		t.Run(name, func(t *testing.T) {
			assertNoHostileEscape(t, renderDigest(build(hostileLine), r), renderDigest(build("benign"), r))
		})
	}
}

func TestAutoLockedGolden(t *testing.T) {
	tests := []struct {
		name     string
		lock     auto.LockReason
		renderer render.Renderer
	}{
		{"auto_locked_disabled_plain", auto.LockedDisabled, render.Plain()},
		{"auto_locked_streak_plain", auto.LockedTrackRecord, render.Plain()},
		{"auto_locked_disabled_unicode_color", auto.LockedDisabled, unicodeColorRenderer()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			printAutoLocked(&out, tc.renderer, tc.lock, "/cfg/ticktack/config.toml", 2, 10)
			assertGolden(t, tc.name, out.String())
			assertColourIffEnabled(t, out.String(), tc.renderer)
		})
	}
}

func TestAutoLockedSanitisesConfigPath(t *testing.T) {
	for name, r := range map[string]render.Renderer{"plain": render.Plain(), "colour": unicodeColorRenderer()} {
		t.Run(name, func(t *testing.T) {
			var hostile, benign bytes.Buffer
			printAutoLocked(&hostile, r, auto.LockedDisabled, "/cfg/"+hostileLine, 1, 10)
			printAutoLocked(&benign, r, auto.LockedDisabled, "/cfg/benign", 1, 10)
			assertNoHostileEscape(t, hostile.String(), benign.String())
		})
	}
}

func TestDigestNoneUsesGutter(t *testing.T) {
	deps, _ := autoTestDeps(t, true, 1)
	seedCleanDays(t, deps, 1)
	deps.Render = unicodeColorRenderer()
	var out bytes.Buffer
	if err := runDigest(context.Background(), &out, deps); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "No digest yet") || countESC(out.String()) == 0 {
		t.Errorf("digest-none line is not a styled gutter line:\n%q", out.String())
	}
}
