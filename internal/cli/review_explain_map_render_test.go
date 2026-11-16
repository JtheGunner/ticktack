package cli

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/render"
	"github.com/JtheGunner/ticktack/internal/review"
	"github.com/JtheGunner/ticktack/internal/store"
	"github.com/JtheGunner/ticktack/internal/youtrack"
)

const hostileText = "\x1b[31mred\x1b]0;pwned\x07\nFAKE LINE\r\u202eevil"

func narrowPlain(width int) render.Renderer {
	return render.Renderer{Glyphs: render.Plain().Glyphs, Width: width}
}

// reviewBranchDeps seeds a day with a repo and branch but no ticket, so the
// day is a single unassigned gap.
func reviewBranchDeps(t *testing.T, input, repo, branch string) (reviewDeps, *bytes.Buffer) {
	t.Helper()
	deps, st, out := reviewTestDeps(t, input)
	if err := st.Purge(context.Background()); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	ctx := context.Background()
	if err := st.Append(ctx, event.Event{At: start, Source: "test", Kind: event.KindContext, Repo: repo, Branch: branch}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= 60; i++ {
		if err := st.Append(ctx, event.Event{At: start.Add(time.Duration(i) * time.Minute), Source: "test", Kind: event.KindPresence}); err != nil {
			t.Fatal(err)
		}
	}
	return deps, out
}

func assertColourIffEnabled(t *testing.T, got string, r render.Renderer) {
	t.Helper()
	if has := strings.Contains(got, "\x1b"); has != r.Colors.Enabled {
		t.Errorf("ESC present = %v, colour enabled = %v", has, r.Colors.Enabled)
	}
}

// assertTableFits checks the first table of got (the lines after the heading
// and its blank line, up to the next blank line).
func assertTableFits(t *testing.T, got string, limit int) {
	t.Helper()
	lines := strings.Split(got, "\n")
	if len(lines) < 3 {
		t.Fatalf("no table in output:\n%s", got)
	}
	for _, line := range lines[2:] {
		if line == "" {
			return
		}
		if n := utf8.RuneCountInString(stripANSI(line)); n > limit {
			t.Errorf("table line is %d columns, limit %d: %q", n, limit, line)
		}
	}
}

func TestReviewGolden(t *testing.T) {
	suggestions := func(config.Config) (review.IssueSource, error) {
		return fakeIssueSource{issues: []youtrack.Issue{{ID: "ABC-1", Summary: "first", Updated: 9}, {ID: "DEF-2", Summary: "second", Updated: 5}}}, nil
	}
	tests := []struct {
		name   string
		input  string
		gap    bool
		render render.Renderer
	}{
		{"review_confirm_plain", "c 1\nd 7\nq\n", false, render.Plain()},
		{"review_gap_unicode_color", "t 1 abc-7\nu 1 2\nq\n", true, func() render.Renderer { r := unicodeColorRenderer(); r.Width = 78; return r }()},
		{"review_gap_narrow_plain", "q\n", true, narrowPlain(70)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var deps reviewDeps
			var out *bytes.Buffer
			if tc.gap {
				deps, out = reviewBranchDeps(t, tc.input, "/work/app", "spike")
				deps.NewIssueSource = suggestions
			} else {
				deps, _, out = reviewTestDeps(t, tc.input)
			}
			deps.Render = tc.render
			if err := runReview(context.Background(), deps, "2026-01-05"); err != nil {
				t.Fatal(err)
			}
			got := out.String()
			assertGolden(t, tc.name, got)
			assertColourIffEnabled(t, got, tc.render)
			if strings.Contains(tc.name, "narrow") {
				assertTableFits(t, got, tc.render.Width)
			}
		})
	}
}

func TestReviewKeepsPromptAndBlockNumbers(t *testing.T) {
	deps, _, out := reviewTestDeps(t, "c 1\nq\n")
	deps.Render = unicodeColorRenderer()
	if err := runReview(context.Background(), deps, "2026-01-05"); err != nil {
		t.Fatal(err)
	}
	got := stripANSI(out.String())
	if !strings.Contains(got, msgReviewPrompt) {
		t.Errorf("prompt %q missing:\n%s", msgReviewPrompt, got)
	}
	if !strings.Contains(got, "1  09:00-10:00") {
		t.Errorf("block number 1 missing:\n%s", got)
	}
}

func TestReviewSanitisesBeforeStyling(t *testing.T) {
	for _, r := range []render.Renderer{render.Plain(), unicodeColorRenderer()} {
		run := func(repo, branch string, issue youtrack.Issue) string {
			deps, out := reviewBranchDeps(t, "t 1 abc-7\nq\n", repo, branch)
			deps.Render = r
			deps.NewIssueSource = func(config.Config) (review.IssueSource, error) {
				return fakeIssueSource{issues: []youtrack.Issue{issue}}, nil
			}
			if err := runReview(context.Background(), deps, "2026-01-05"); err != nil {
				t.Fatal(err)
			}
			return out.String()
		}
		benign := run("/work/app", "spike", youtrack.Issue{ID: "ABC-1", Summary: "plain", Updated: 1})
		hostile := run("/work/"+hostileText, hostileText, youtrack.Issue{ID: "ABC-1" + hostileText, Summary: hostileText, Updated: 1})
		assertColourIffEnabled(t, hostile, r)
		plain := stripANSI(hostile)
		if strings.ContainsAny(plain, "\x1b\x07\r\u202e") {
			t.Errorf("control characters survive: %q", plain)
		}
		if got, want := strings.Count(hostile, "\n"), strings.Count(benign, "\n"); got != want {
			t.Errorf("hostile text changed the line count: %d, want %d\n%s", got, want, hostile)
		}
	}
}

func explainEnv(t *testing.T, render render.Renderer, repo string) (dailyDeps, *bytes.Buffer) {
	t.Helper()
	deps, st, out := newDailyEnv(t, "")
	deps.Render = render
	day := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)
	if _, err := st.AddLearnedRule(context.Background(), store.LearnedRule{Repo: "/work/api", BranchPattern: "dev", Ticket: "LRN-5", CreatedAt: day}); err != nil {
		t.Fatal(err)
	}
	mustAppend(t, st, shellContext(day, repo, "ABC-7-fix"), shellContext(day.Add(time.Hour), "/work/api", "dev"))
	seedPresence(t, st, day, 30)
	seedPresence(t, st, day.Add(time.Hour), 30)
	return deps, out
}

func TestExplainGolden(t *testing.T) {
	tests := []struct {
		name   string
		target string
		render render.Renderer
	}{
		{"explain_branch_plain", "1", render.Plain()},
		{"explain_learned_unicode_color", "2", unicodeColorRenderer()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			deps, out := explainEnv(t, tc.render, "/work/api")
			if err := runExplain(context.Background(), deps, "", tc.target); err != nil {
				t.Fatal(err)
			}
			assertGolden(t, tc.name, out.String())
			assertColourIffEnabled(t, out.String(), tc.render)
		})
	}
}

func TestExplainSanitisesBeforeStyling(t *testing.T) {
	for _, r := range []render.Renderer{render.Plain(), unicodeColorRenderer()} {
		run := func(repo string) string {
			deps, out := explainEnv(t, r, repo)
			if err := runExplain(context.Background(), deps, "", "1"); err != nil {
				t.Fatal(err)
			}
			return out.String()
		}
		benign := run("/work/api")
		hostile := run("/work/" + hostileText)
		assertColourIffEnabled(t, hostile, r)
		if strings.ContainsAny(stripANSI(hostile), "\x1b\x07\r\u202e") {
			t.Errorf("control characters survive: %q", hostile)
		}
		if got, want := strings.Count(hostile, "\n"), strings.Count(benign, "\n"); got != want {
			t.Errorf("hostile text changed the line count: %d, want %d\n%s", got, want, hostile)
		}
	}
}

func TestMapGolden(t *testing.T) {
	tests := []struct {
		name   string
		render render.Renderer
	}{
		{"map_list_plain", render.Plain()},
		{"map_list_unicode_color", unicodeColorRenderer()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			deps, _, out := mapEnv(t)
			deps.Render = tc.render
			if err := runMapAdd(ctx, deps, "/work/api", "dev", "abc-1"); err != nil {
				t.Fatal(err)
			}
			if err := runMapAdd(ctx, deps, "/work/web", "", "abc-2"); err != nil {
				t.Fatal(err)
			}
			if err := runMapEdit(ctx, deps, "2", mapChanges{ticket: ptrIf(true, "abc-3")}); err != nil {
				t.Fatal(err)
			}
			if err := runMapRemove(ctx, deps, "1"); err != nil {
				t.Fatal(err)
			}
			if err := runMapList(ctx, deps); err != nil {
				t.Fatal(err)
			}
			assertGolden(t, tc.name, out.String())
			assertColourIffEnabled(t, out.String(), tc.render)
		})
	}
}

func TestMapSanitisesBeforeStyling(t *testing.T) {
	for _, r := range []render.Renderer{render.Plain(), unicodeColorRenderer()} {
		run := func(repo, branch string) string {
			ctx := context.Background()
			deps, dbPath, out := mapEnv(t)
			deps.Render = r
			st, err := store.Open(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.AddLearnedRule(ctx, store.LearnedRule{Repo: repo, BranchPattern: branch, Ticket: "ABC-1", CreatedAt: dailyNow}); err != nil {
				t.Fatal(err)
			}
			_ = st.Close()
			if err := runMapList(ctx, deps); err != nil {
				t.Fatal(err)
			}
			if err := runMapEdit(ctx, deps, "1", mapChanges{ticket: ptrIf(true, "abc-2")}); err != nil {
				t.Fatal(err)
			}
			return out.String()
		}
		benign := run("/w/app", "dev")
		hostile := run("/w/"+hostileText, hostileText)
		assertColourIffEnabled(t, hostile, r)
		if strings.ContainsAny(stripANSI(hostile), "\x1b\x07\r\u202e") {
			t.Errorf("control characters survive: %q", hostile)
		}
		if got, want := strings.Count(hostile, "\n"), strings.Count(benign, "\n"); got != want {
			t.Errorf("hostile text changed the line count: %d, want %d\n%s", got, want, hostile)
		}
	}
}

// TestReviewReasonCellIsSanitised reaches the Reason cell with external text:
// a learned rule's reason embeds the repository path of the event. The
// renderer is wide so the hostile part is not truncated away.
func TestReviewReasonCellIsSanitised(t *testing.T) {
	wide := func(r render.Renderer) render.Renderer { r.Width = 200; return r }
	for _, r := range []render.Renderer{wide(render.Plain()), wide(unicodeColorRenderer())} {
		run := func(repo string) string {
			deps, st, out := reviewTestDeps(t, "q\n")
			if err := st.Purge(context.Background()); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			start := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
			if _, err := st.AddLearnedRule(ctx, store.LearnedRule{Repo: "/work", Ticket: "LRN-5", CreatedAt: start}); err != nil {
				t.Fatal(err)
			}
			if err := st.Append(ctx, event.Event{At: start, Source: "test", Kind: event.KindContext, Repo: repo, Branch: "dev"}); err != nil {
				t.Fatal(err)
			}
			for i := 0; i <= 60; i++ {
				if err := st.Append(ctx, event.Event{At: start.Add(time.Duration(i) * time.Minute), Source: "test", Kind: event.KindPresence}); err != nil {
					t.Fatal(err)
				}
			}
			deps.Render = r
			if err := runReview(ctx, deps, "2026-01-05"); err != nil {
				t.Fatal(err)
			}
			return out.String()
		}
		benign := run("/work/app")
		hostile := run("/work/\x1b[2Jred\x07\nFAKE LINE\r\u202eevil")
		if !strings.Contains(stripANSI(benign), "learned rule for /work/app") {
			t.Fatalf("benign reason not shown, test would be vacuous:\n%s", benign)
		}
		if !strings.Contains(stripANSI(hostile), "learned rule for /work/") || !strings.Contains(stripANSI(hostile), "FAKE LINE") {
			t.Fatalf("hostile reason does not reach the table:\n%s", hostile)
		}
		if strings.Contains(hostile, "\x1b[2J") || strings.ContainsAny(stripANSI(hostile), "\x1b\x07\r\u202e") {
			t.Errorf("control characters survive: %q", hostile)
		}
		if !r.Colors.Enabled && strings.Contains(hostile, "\x1b") {
			t.Errorf("plain output contains ESC: %q", hostile)
		}
		if got, want := strings.Count(hostile, "\n"), strings.Count(benign, "\n"); got != want {
			t.Errorf("hostile reason changed the line count: %d, want %d\n%s", got, want, hostile)
		}
	}
}

// TestReviewReasonIsNeverCut proves the whole reason reaches the screen at
// ordinary terminal widths, wrapped under its row when the row has no room.
func TestReviewReasonIsNeverCut(t *testing.T) {
	tests := []struct {
		name   string
		gap    bool
		reason string
	}{
		{"gap", true, "no ticket in branch and no rule for repo"},
		{"explicit", false, "explicit ticket ABC-1 on event"},
	}
	for _, tt := range tests {
		for _, width := range []int{78, 60} {
			t.Run(fmt.Sprintf("%s_%d", tt.name, width), func(t *testing.T) {
				var deps reviewDeps
				var out *bytes.Buffer
				if tt.gap {
					deps, out = reviewBranchDeps(t, "q\n", "/work/app", "spike")
				} else {
					deps, _, out = reviewTestDeps(t, "q\n")
				}
				deps.Render = render.Renderer{Glyphs: render.Plain().Glyphs, Width: width}
				if err := runReview(context.Background(), deps, "2026-01-05"); err != nil {
					t.Fatal(err)
				}
				got := out.String()
				table := strings.SplitN(got, "\n\n", 3)[1]
				if !strings.Contains(strings.Join(strings.Fields(table), " "), tt.reason) {
					t.Errorf("reason %q was cut:\n%s", tt.reason, got)
				}
			})
		}
	}
}
