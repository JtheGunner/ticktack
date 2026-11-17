package cli

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/JtheGunner/ticktack/internal/render"
)

// reviewHelpCommands lists every action the review accepts, in the form the
// help shows it.
var reviewHelpCommands = []string{
	"c N", "t N TICKET", "u N K", "r [repo]", "d N", "b N", "s N HH:MM [TICKET]", "a", "y", "q",
}

func helpRenderer(width int, colour bool) render.Renderer {
	r := render.Plain()
	r.Width = width
	r.Colors = render.Colors{Enabled: colour}
	return r
}

func TestReviewHelpListsEveryCommandGrouped(t *testing.T) {
	text := strings.Join(reviewHelpLines(helpRenderer(78, false)), "\n")
	if !strings.Contains(text, msgReviewHelpTitle) {
		t.Errorf("help has no %q title:\n%s", msgReviewHelpTitle, text)
	}
	for _, want := range reviewHelpCommands {
		if !strings.Contains(text, want) {
			t.Errorf("help misses command %q:\n%s", want, text)
		}
	}
	for _, g := range reviewHelpGroups {
		if !strings.Contains(text, g.Name) {
			t.Errorf("help misses group %q:\n%s", g.Name, text)
		}
	}
	if n := len(reviewHelpLines(helpRenderer(78, false))); n < 5 {
		t.Errorf("help is %d lines, want a grouped block of at least 5", n)
	}
}

func TestReviewHelpColourOnlyWhenEnabled(t *testing.T) {
	plain := strings.Join(reviewHelpLines(helpRenderer(78, false)), "\n")
	if strings.Contains(plain, "\x1b") {
		t.Errorf("help without colour contains escape sequences: %q", plain)
	}
	coloured := strings.Join(reviewHelpLines(helpRenderer(78, true)), "\n")
	if !strings.Contains(coloured, "\x1b[") {
		t.Errorf("help with colour contains no escape sequences: %q", coloured)
	}
	if stripANSI(coloured) != plain {
		t.Errorf("colour changes the text\n--- coloured, stripped ---\n%s\n--- plain ---\n%s", stripANSI(coloured), plain)
	}
}

func TestReviewHelpFitsWidth(t *testing.T) {
	for _, width := range []int{40, 56, 78, 100} {
		for _, line := range reviewHelpLines(helpRenderer(width, true)) {
			if n := utf8.RuneCountInString(stripANSI(line)); n > width {
				t.Errorf("width %d: help line is %d columns: %q", width, n, stripANSI(line))
			}
		}
	}
}

func TestReviewHelpKeepsEveryCommandOnNarrowTerminals(t *testing.T) {
	text := strings.Join(reviewHelpLines(helpRenderer(40, false)), "\n")
	for _, want := range reviewHelpCommands {
		if !strings.Contains(text, want) {
			t.Errorf("narrow help misses command %q:\n%s", want, text)
		}
	}
}

func TestReviewUnknownCommandShowsHelp(t *testing.T) {
	deps, _, out := reviewTestDeps(t, "x\nq\n")
	if err := runReview(context.Background(), deps, "2026-01-05"); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, `Unknown command "x".`) {
		t.Errorf("no unknown-command message:\n%s", got)
	}
	if n := strings.Count(got, msgReviewHelpTitle); n != 2 {
		t.Errorf("help shown %d times, want 2 (initial and after the unknown command):\n%s", n, got)
	}
	if strings.Contains(got, "Actions: c N confirm |") {
		t.Errorf("old single-line help is still printed:\n%s", got)
	}
}
