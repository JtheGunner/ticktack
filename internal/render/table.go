package render

import "strings"

const (
	tableIndent = 2
	tableGap    = 2
)

// Column describes one table column.
type Column struct {
	Header string
	// Right aligns the column's cells to the right.
	Right bool
	// Flex marks the column that shrinks, with an ellipsis, when the table is
	// wider than the layout. Only the first Flex column is used.
	Flex bool
}

// Table renders a dim header row and the rows below it. Columns are as wide as
// their widest visible cell. When the table would exceed the layout width the
// Flex column shrinks and its cells are cut with an ellipsis; if the fixed
// columns alone are too wide, lines are cut instead of wrapped. Cells are
// written as given, so callers sanitise them first. Missing cells are empty.
func (r Renderer) Table(cols []Column, rows [][]string) []string {
	if len(cols) == 0 {
		return nil
	}
	g, c, limit := r.glyphs(), r.Colors, r.layoutWidth()
	widths := make([]int, len(cols))
	for i, col := range cols {
		widths[i] = visibleWidth(col.Header)
	}
	for _, row := range rows {
		for i := range cols {
			widths[i] = max(widths[i], visibleWidth(cell(row, i)))
		}
	}
	if flex := firstFlex(cols); flex >= 0 {
		others := tableIndent + tableGap*(len(cols)-1)
		for i, w := range widths {
			if i != flex {
				others += w
			}
		}
		widths[flex] = min(widths[flex], max(limit-others, 1))
	}

	lines := make([]string, 0, len(rows)+1)
	header := make([]string, len(cols))
	for i, col := range cols {
		header[i] = col.Header
	}
	lines = append(lines, r.tableLine(cols, widths, header, limit, g, func(s string) string { return c.Dim(s) }))
	for _, row := range rows {
		lines = append(lines, r.tableLine(cols, widths, row, limit, g, func(s string) string { return s }))
	}
	return lines
}

// tableLine lays out one row. style is applied to each cell after it is cut
// and before it is padded.
func (r Renderer) tableLine(cols []Column, widths []int, row []string, limit int, g Glyphs, style func(string) string) string {
	var b strings.Builder
	b.WriteString(strings.Repeat(" ", tableIndent))
	for i, col := range cols {
		if i > 0 {
			b.WriteString(strings.Repeat(" ", tableGap))
		}
		text := style(fit(cell(row, i), widths[i], g.Ellipsis))
		switch {
		case col.Right:
			b.WriteString(padLeftVisible(text, widths[i]))
		case i == len(cols)-1:
			b.WriteString(text)
		default:
			b.WriteString(padVisible(text, widths[i]))
		}
	}
	return strings.TrimRight(fit(b.String(), limit, g.Ellipsis), " ")
}

func cell(row []string, i int) string {
	if i < len(row) {
		return row[i]
	}
	return ""
}

func firstFlex(cols []Column) int {
	for i, col := range cols {
		if col.Flex {
			return i
		}
	}
	return -1
}
