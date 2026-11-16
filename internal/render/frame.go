package render

import "strings"

// titleMargin is the space a rule or box title leaves for line characters and
// its own padding: two corner or edge cells on each side plus a space on each
// side of the title.
const titleMargin = 8

// Status selects the symbol and colour of a gutter line.
type Status int

// The statuses a gutter line can show. Each has its own glyph, so the status
// is readable without colour.
const (
	StatusInfo Status = iota
	StatusOK
	StatusWarn
	StatusFail
	StatusSkip
	StatusGo
)

// LabelRow is one "Label  value" line of a detail block.
type LabelRow struct {
	Label string
	Value string
}

// Rule draws a horizontal line with title centred in it. A title that does
// not fit is shortened; an empty title gives a plain line.
func (r Renderer) Rule(title string) string {
	g, c, width := r.glyphs(), r.Colors, r.layoutWidth()
	if title == "" {
		return c.Accent(strings.Repeat(g.H, width))
	}
	padded := " " + fit(title, max(width-titleMargin, 1), g.Ellipsis) + " "
	left, right := splitPadding(width - visibleWidth(padded))
	return c.Accent(strings.Repeat(g.H, left)) + c.Bold(padded) + c.Accent(strings.Repeat(g.H, right))
}

// Box draws a rounded box the width of the layout with title centred in the
// top border. Rows are padded to the inner width; a row wider than the box is
// shortened with an ellipsis. The border uses the accent colour.
func (r Renderer) Box(title string, rows []string) string {
	return r.BoxStatus(StatusInfo, title, rows)
}

// BoxStatus is Box with a border coloured by status: green for StatusOK,
// yellow for StatusWarn, red for StatusFail, dim for StatusSkip and the accent
// colour otherwise. The border shape is the same for every status, so the
// outcome must also be stated in the rows.
func (r Renderer) BoxStatus(s Status, title string, rows []string) string {
	g, c, width := r.glyphs(), r.Colors, r.layoutWidth()
	border := c.forStatus(s)
	inner := max(width-2, 0)
	top := border(g.TL + strings.Repeat(g.H, inner) + g.TR)
	if title != "" {
		padded := " " + fit(title, max(width-titleMargin, 1), g.Ellipsis) + " "
		left, right := splitPadding(inner - visibleWidth(padded))
		top = border(g.TL+strings.Repeat(g.H, left)) + c.Bold(padded) + border(strings.Repeat(g.H, right)+g.TR)
	}
	lines := make([]string, 0, len(rows)+2)
	lines = append(lines, top)
	for _, row := range rows {
		cell := padVisible(fit(row, max(inner-2, 1), g.Ellipsis), max(inner-2, 0))
		lines = append(lines, border(g.V)+" "+cell+" "+border(g.V))
	}
	lines = append(lines, border(g.BL+strings.Repeat(g.H, inner)+g.BR))
	return strings.Join(lines, "\n")
}

// splitPadding divides total padding into a left and a right part; the odd
// cell goes to the right. Negative totals give no padding.
func splitPadding(total int) (left, right int) {
	if total <= 0 {
		return 0, 0
	}
	left = total / 2
	return left, total - left
}

// Gutter prefixes text with a coloured status symbol in a fixed column.
func (r Renderer) Gutter(s Status, text string) string {
	g, c := r.glyphs(), r.Colors
	var symbol string
	switch s {
	case StatusOK:
		symbol = c.OK(g.OK)
	case StatusWarn:
		symbol = c.Warn(g.Warn)
	case StatusFail:
		symbol = c.Fail(g.Fail)
	case StatusSkip:
		symbol = c.Dim(g.Skip)
	case StatusGo:
		symbol = c.Accent(g.Go)
	default:
		symbol = c.Accent(g.Info)
	}
	return "  " + symbol + "  " + text
}

// Labels renders rows with dim labels padded to a common width, indented by
// indent spaces. Values are written as given.
func (r Renderer) Labels(rows []LabelRow, indent int) []string {
	longest := 0
	for _, row := range rows {
		longest = max(longest, visibleWidth(row.Label))
	}
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, strings.Repeat(" ", max(indent, 0))+padVisible(r.Colors.Dim(row.Label), longest+2)+row.Value)
	}
	return lines
}

// Heading returns a bold title line used before a section.
func (r Renderer) Heading(text string) string {
	return "  " + r.Colors.Bold(text)
}
