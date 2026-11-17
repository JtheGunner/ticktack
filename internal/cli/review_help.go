package cli

import (
	"strings"
	"unicode/utf8"

	"github.com/JtheGunner/ticktack/internal/render"
)

const (
	keyBarIndent    = 2
	keyBarSeparator = "   "
	helpGroupIndent = 2
	helpItemIndent  = 4
	helpStackIndent = 6
	helpColumnGap   = 2
)

// styledText is text with its visible width, so styled strings can be padded
// and packed without measuring escape sequences.
type styledText struct {
	text  string
	width int
}

func plainText(s string) styledText { return styledText{s, utf8.RuneCountInString(s)} }

func (s styledText) padded(width int) string {
	return s.text + strings.Repeat(" ", max(width-s.width, 0))
}

// reviewKeyBarLines renders the key bar: a titled rule and the main commands
// as "key verb" pairs, wrapped to the layout width. It holds no placeholders;
// those belong to the help screen.
func reviewKeyBarLines(rd render.Renderer) []string {
	budget := max(rd.LayoutWidth()-keyBarIndent, 1)
	lines := []string{rd.Rule(msgReviewHelpTitle)}
	var row []string
	used := 0
	flush := func() {
		if len(row) > 0 {
			lines = append(lines, strings.Repeat(" ", keyBarIndent)+strings.Join(row, keyBarSeparator))
			row, used = nil, 0
		}
	}
	for _, key := range reviewKeyBarKeys {
		c := reviewCommandByKey(key)
		chip := styledText{
			text:  rd.Colors.Bold(rd.Colors.Accent(c.Key)) + " " + rd.Colors.Dim(c.Verb),
			width: utf8.RuneCountInString(c.Key) + 1 + utf8.RuneCountInString(c.Verb),
		}
		if len(row) > 0 && used+len(keyBarSeparator)+chip.width > budget {
			flush()
		}
		if len(row) > 0 {
			used += len(keyBarSeparator)
		}
		row = append(row, chip.text)
		used += chip.width
	}
	flush()
	return lines
}

// helpLayout says which columns of the help screen fit the layout width.
type helpLayout int

const (
	helpStacked  helpLayout = iota // description on its own line under the command
	helpTwoCols                    // command | description
	helpThreeCol                   // command | description | example
)

// reviewHelpLines renders the help screen: one command per line, grouped,
// with description and example in aligned columns, followed by a legend of the
// placeholders. A layout too narrow for all columns drops the example, then
// moves the description under its command.
func reviewHelpLines(rd render.Renderer) []string {
	width := rd.LayoutWidth()
	var usageW, descW, exampleW int
	for _, g := range reviewHelpGroups {
		for _, c := range g.Commands {
			usageW = max(usageW, utf8.RuneCountInString(reviewUsage(c.Key)))
			descW = max(descW, utf8.RuneCountInString(c.Description))
			exampleW = max(exampleW, utf8.RuneCountInString(c.Example))
		}
	}
	layout := helpStacked
	switch {
	case helpItemIndent+usageW+helpColumnGap+descW+helpColumnGap+exampleW <= width:
		layout = helpThreeCol
	case helpItemIndent+usageW+helpColumnGap+descW <= width:
		layout = helpTwoCols
	}

	lines := []string{rd.Rule(msgReviewHelpScreenTitle)}
	for _, g := range reviewHelpGroups {
		lines = append(lines, strings.Repeat(" ", helpGroupIndent)+rd.Colors.Bold(g.Name))
		for _, c := range g.Commands {
			usage := commandUsage(rd, c)
			item := strings.Repeat(" ", helpItemIndent)
			switch layout {
			case helpThreeCol:
				item += usage.padded(usageW+helpColumnGap) +
					plainText(c.Description).padded(descW+helpColumnGap) + rd.Colors.Dim(c.Example)
			case helpTwoCols:
				item += usage.padded(usageW+helpColumnGap) + c.Description
			default:
				item += usage.text
			}
			lines = append(lines, item)
			if layout == helpStacked {
				lines = append(lines, strings.Repeat(" ", helpStackIndent)+c.Description)
			}
		}
	}
	return append(lines, placeholderLegend(rd, width)...)
}

// commandUsage styles "key args": the key bold and accented, the placeholders
// in their own colour.
func commandUsage(rd render.Renderer, c reviewCommand) styledText {
	text := rd.Colors.Bold(rd.Colors.Accent(c.Key))
	if c.Args != "" {
		text += " " + rd.Colors.Placeholder(c.Args)
	}
	return styledText{text, utf8.RuneCountInString(reviewUsage(c.Key))}
}

// placeholderLegend explains every placeholder, one per line.
func placeholderLegend(rd render.Renderer, width int) []string {
	nameW := 0
	for _, p := range reviewPlaceholders {
		nameW = max(nameW, utf8.RuneCountInString(p.Name))
	}
	lines := []string{"", strings.Repeat(" ", helpGroupIndent) + rd.Colors.Bold(msgReviewHelpPlaceholders)}
	for _, p := range reviewPlaceholders {
		room := max(width-helpItemIndent-nameW-helpColumnGap, 1)
		lines = append(lines, strings.Repeat(" ", helpItemIndent)+
			styledText{rd.Colors.Placeholder(p.Name), utf8.RuneCountInString(p.Name)}.padded(nameW+helpColumnGap)+
			truncateRunes(p.Meaning, room, rd.Glyphs.Ellipsis))
	}
	return lines
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
