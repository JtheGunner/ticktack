package cli

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/JtheGunner/ticktack/internal/render"
)

const (
	escAccent      = "\x1b[36m"
	escPlaceholder = "\x1b[35m"
	escDim         = "\x1b[2m"
)

// reviewKeys are the actions the review prompt accepts, plus the help.
var reviewKeys = []string{"c", "t", "u", "r", "d", "b", "s", "a", "y", "q", "?"}

func helpRenderer(width int, colour bool) render.Renderer {
	r := render.Plain()
	r.Width = width
	r.Colors = render.Colors{Enabled: colour}
	return r
}

func joined(lines []string) string { return strings.Join(lines, "\n") }

func assertLinesFit(t *testing.T, lines []string, width int) {
	t.Helper()
	for _, line := range lines {
		if n := utf8.RuneCountInString(stripANSI(line)); n > width {
			t.Errorf("width %d: line is %d columns: %q", width, n, stripANSI(line))
		}
	}
}

func TestReviewCommandDataDocumentsEveryKey(t *testing.T) {
	seen := map[string]bool{}
	for _, g := range reviewHelpGroups {
		for _, c := range g.Commands {
			seen[c.Key] = true
			if c.Description == "" {
				t.Errorf("command %q has no description", c.Key)
			}
			if c.Example == "" {
				t.Errorf("command %q has no example", c.Key)
			}
			if strings.Contains(c.Args, " N") || strings.HasPrefix(c.Args, "N") {
				t.Errorf("command %q uses the unexplained placeholder N: %q", c.Key, c.Args)
			}
		}
	}
	for _, k := range reviewKeys {
		if !seen[k] {
			t.Errorf("help does not document %q", k)
		}
	}
	if len(seen) != len(reviewKeys) {
		t.Errorf("help documents %d keys, want %d", len(seen), len(reviewKeys))
	}
}

func TestReviewKeyBarShowsKeysWithVerbsOnly(t *testing.T) {
	text := joined(reviewKeyBarLines(helpRenderer(78, false)))
	for _, want := range []string{"c confirm", "t assign", "s split", "d discard", "b break", "y confirm all", "? help", "q quit", msgReviewHelpTitle} {
		if !strings.Contains(text, want) {
			t.Errorf("key bar misses %q:\n%s", want, text)
		}
	}
	if strings.ContainsAny(text, "<>[]") {
		t.Errorf("key bar shows placeholders:\n%s", text)
	}
	if n := len(strings.Split(text, "\n")); n > 4 {
		t.Errorf("key bar is %d lines, want a compact bar", n)
	}
}

func TestReviewKeyBarFitsWidth(t *testing.T) {
	for _, width := range []int{40, 56, 78, 100} {
		assertLinesFit(t, reviewKeyBarLines(helpRenderer(width, true)), width)
	}
}

func TestReviewKeyBarColourRoles(t *testing.T) {
	plain := joined(reviewKeyBarLines(helpRenderer(78, false)))
	coloured := joined(reviewKeyBarLines(helpRenderer(78, true)))
	if strings.Contains(plain, "\x1b") {
		t.Errorf("key bar without colour contains escape sequences: %q", plain)
	}
	if !strings.Contains(coloured, escAccent) || !strings.Contains(coloured, escDim) {
		t.Errorf("key bar misses the key and verb styles: %q", coloured)
	}
	if strings.Contains(coloured, escPlaceholder) {
		t.Errorf("key bar uses the placeholder colour: %q", coloured)
	}
	if stripANSI(coloured) != plain {
		t.Errorf("colour changes the text\n%s\n---\n%s", stripANSI(coloured), plain)
	}
}

func TestReviewHelpListsEveryCommandOnItsOwnLine(t *testing.T) {
	lines := reviewHelpLines(helpRenderer(78, false))
	for _, g := range reviewHelpGroups {
		if !strings.Contains(joined(lines), g.Name) {
			t.Errorf("help misses group %q", g.Name)
		}
		for _, c := range g.Commands {
			usage := reviewUsage(c.Key)
			count := 0
			for _, line := range lines {
				if strings.HasPrefix(strings.TrimSpace(line), usage+" ") {
					count++
				}
			}
			if count != 1 {
				t.Errorf("command %q is on %d lines, want 1:\n%s", usage, count, joined(lines))
			}
		}
	}
}

func TestReviewHelpShowsDescriptionAndExampleInColumns(t *testing.T) {
	lines := reviewHelpLines(helpRenderer(78, false))
	byUsage := map[string]string{}
	for _, line := range lines {
		for _, g := range reviewHelpGroups {
			for _, c := range g.Commands {
				if strings.HasPrefix(strings.TrimSpace(line), reviewUsage(c.Key)+" ") {
					byUsage[c.Key] = line
				}
			}
		}
	}
	for _, g := range reviewHelpGroups {
		for _, c := range g.Commands {
			line := byUsage[c.Key]
			if !strings.Contains(line, c.Description) || !strings.Contains(line, c.Example) {
				t.Errorf("line of %q misses description or example: %q", c.Key, line)
			}
		}
	}
	descAt := -1
	for key, line := range byUsage {
		at := strings.Index(line, reviewCommandByKey(key).Description)
		if descAt == -1 {
			descAt = at
		}
		if at != descAt {
			t.Errorf("description of %q starts at column %d, others at %d", key, at, descAt)
		}
	}
}

func TestReviewHelpExplainsEveryPlaceholder(t *testing.T) {
	text := joined(reviewHelpLines(helpRenderer(78, false)))
	for _, want := range []string{"<block>", "<ticket>", "<time>", "<n>", "[repo]", "# column"} {
		if !strings.Contains(text, want) {
			t.Errorf("help legend misses %q:\n%s", want, text)
		}
	}
}

func TestReviewHelpColourRoles(t *testing.T) {
	plain := joined(reviewHelpLines(helpRenderer(78, false)))
	coloured := joined(reviewHelpLines(helpRenderer(78, true)))
	if strings.Contains(plain, "\x1b") {
		t.Errorf("help without colour contains escape sequences")
	}
	for name, esc := range map[string]string{"key": escAccent, "placeholder": escPlaceholder, "example": escDim} {
		if !strings.Contains(coloured, esc) {
			t.Errorf("help misses the %s style", name)
		}
	}
	if stripANSI(coloured) != plain {
		t.Errorf("colour changes the text\n%s\n---\n%s", stripANSI(coloured), plain)
	}
}

func TestReviewHelpFitsNarrowTerminals(t *testing.T) {
	for _, width := range []int{40, 56, 78, 100} {
		lines := reviewHelpLines(helpRenderer(width, true))
		assertLinesFit(t, lines, width)
		text := stripANSI(joined(lines))
		for _, k := range reviewKeys {
			if !strings.Contains(text, reviewUsage(k)) {
				t.Errorf("width %d: help misses command %q", width, reviewUsage(k))
			}
		}
	}
}

func TestReviewHelpGolden(t *testing.T) {
	for _, tc := range []struct {
		name   string
		width  int
		colour bool
	}{
		{"review_help_plain_78", 78, false},
		{"review_help_plain_56", 56, false},
		{"review_help_plain_40", 40, false},
		{"review_help_color_78", 78, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rd := helpRenderer(tc.width, tc.colour)
			got := joined(reviewKeyBarLines(rd)) + "\n\n" + joined(reviewHelpLines(rd)) + "\n"
			assertGolden(t, tc.name, got)
		})
	}
}

func TestReviewQuestionMarkAndHelpShowTheHelp(t *testing.T) {
	deps, _, out := reviewTestDeps(t, "?\nhelp\nq\n")
	if err := runReview(context.Background(), deps, "2026-01-05"); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if n := strings.Count(got, "Placeholders"); n != 2 {
		t.Errorf("help shown %d times, want 2:\n%s", n, got)
	}
	if n := strings.Count(got, "Review for"); n != 1 {
		t.Errorf("table printed %d times, want 1 (help must not redraw it):\n%s", n, got)
	}
	if strings.Contains(got, "Unknown command") {
		t.Errorf("help counted as unknown command:\n%s", got)
	}
}

func TestReviewUnknownCommandShowsKeyBarOnly(t *testing.T) {
	deps, _, out := reviewTestDeps(t, "x\nq\n")
	if err := runReview(context.Background(), deps, "2026-01-05"); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, `Unknown command "x".`) {
		t.Errorf("no unknown-command message:\n%s", got)
	}
	if n := strings.Count(got, "? help"); n != 2 {
		t.Errorf("key bar shown %d times, want 2 (initial and after the unknown command):\n%s", n, got)
	}
	if strings.Contains(got, "Placeholders") {
		t.Errorf("unknown command shows the full help:\n%s", got)
	}
}

func TestReviewUsageMessagesUsePlaceholders(t *testing.T) {
	deps, _, out := reviewTestDeps(t, "c\nt 1\ns\nu 1\nq\n")
	if err := runReview(context.Background(), deps, "2026-01-05"); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"Usage: c <block>", "Usage: t <block> <ticket>", "Usage: s <block> <time> [<ticket>]", "Usage: u <block> <n>"} {
		if !strings.Contains(got, want) {
			t.Errorf("output misses %q:\n%s", want, got)
		}
	}
}
