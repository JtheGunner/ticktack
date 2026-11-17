package cli

import (
	"strings"
	"unicode/utf8"

	"github.com/JtheGunner/ticktack/internal/render"
)

const (
	reviewHelpIndent    = 2
	reviewHelpLabelGap  = 2
	reviewHelpSeparator = "   "
	reviewHelpKeyGap    = "  "
)

// reviewHelpLines renders the action overview: a titled rule followed by one
// block per group. The commands of a group share lines and wrap under the
// group label when the layout is too narrow for them.
func reviewHelpLines(rd render.Renderer) []string {
	labelWidth := 0
	for _, g := range reviewHelpGroups {
		labelWidth = max(labelWidth, utf8.RuneCountInString(g.Name))
	}
	prefixWidth := reviewHelpIndent + labelWidth + reviewHelpLabelGap
	budget := max(rd.LayoutWidth()-prefixWidth, 1)

	lines := []string{rd.Rule(msgReviewHelpTitle)}
	for _, g := range reviewHelpGroups {
		for i, row := range packHelpChips(rd, g.Commands, budget) {
			label := ""
			if i == 0 {
				label = g.Name
			}
			pad := strings.Repeat(" ", labelWidth-utf8.RuneCountInString(label)+reviewHelpLabelGap)
			lines = append(lines, strings.Repeat(" ", reviewHelpIndent)+rd.Colors.Dim(label)+pad+strings.Join(row, reviewHelpSeparator))
		}
	}
	return lines
}

// packHelpChips fills lines of at most budget columns with the styled chips of
// the commands, keeping their order.
func packHelpChips(rd render.Renderer, commands []reviewCommand, budget int) [][]string {
	var rows [][]string
	var row []string
	used := 0
	for _, c := range commands {
		chip, width := helpChip(rd, c, budget)
		if len(row) > 0 && used+len(reviewHelpSeparator)+width > budget {
			rows = append(rows, row)
			row, used = nil, 0
		}
		if len(row) > 0 {
			used += len(reviewHelpSeparator)
		}
		row = append(row, chip)
		used += width
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	return rows
}

// helpChip returns the styled "key args  description" text of one command and
// its visible width. A description that cannot fit a line on its own is cut
// with an ellipsis.
func helpChip(rd render.Renderer, c reviewCommand, budget int) (string, int) {
	head := c.Key
	if c.Args != "" {
		head += " " + c.Args
	}
	desc := c.Description
	if room := budget - utf8.RuneCountInString(head) - len(reviewHelpKeyGap); utf8.RuneCountInString(desc) > room {
		desc = truncateRunes(desc, room, rd.Glyphs.Ellipsis)
	}
	styled := rd.Colors.Bold(rd.Colors.Accent(c.Key))
	if c.Args != "" {
		styled += " " + rd.Colors.Dim(c.Args)
	}
	styled += reviewHelpKeyGap + desc
	return styled, utf8.RuneCountInString(head) + len(reviewHelpKeyGap) + utf8.RuneCountInString(desc)
}

// truncateRunes shortens s to at most n runes, ending in ellipsis when it was
// cut. A budget too small for the ellipsis gives an empty string.
func truncateRunes(s string, n int, ellipsis string) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	keep := n - utf8.RuneCountInString(ellipsis)
	if keep <= 0 {
		return ""
	}
	return string([]rune(s)[:keep]) + ellipsis
}
