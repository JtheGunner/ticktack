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
	// Wrap keeps a Flex column's cells whole: a cell wider than the space the
	// Flex column has is left empty in its row and written in full, wrapped at
	// spaces, on dimmed lines indented under the row. Cells that fit stay
	// inline. Ignored without Flex.
	Wrap bool
}

// wrappedIndent is the indent of a wrapped cell below its row.
const wrappedIndent = 4

// Table renders a dim header row and the rows below it. Columns are as wide as
// their widest visible cell. When the table would exceed the layout width the
// Flex column shrinks and its cells are cut with an ellipsis; if the fixed
// columns alone are too wide, lines are cut instead of wrapped. Cells are
// written as given, so callers sanitise them first. Missing cells are empty.
// A Flex column with Wrap never cuts a cell; see Column.Wrap.
func (r Renderer) Table(cols []Column, rows [][]string) []string {
	if len(cols) == 0 {
		return nil
	}
	g, c, limit := r.glyphs(), r.Colors, r.layoutWidth()
	widths := make([]int, len(cols))
	for i, col := range cols {
		widths[i] = visibleWidth(col.Header)
	}
	flex := firstFlex(cols)
	for _, row := range rows {
		for i := range cols {
			if i != flex {
				widths[i] = max(widths[i], visibleWidth(cell(row, i)))
			}
		}
	}
	// wrapped marks the rows whose Flex cell does not fit and moves below the
	// row. Measured against the space left by the fixed columns, never by
	// searching rendered text.
	wrapped := make([]bool, len(rows))
	if flex >= 0 {
		others := tableIndent + tableGap*(len(cols)-1)
		for i, w := range widths {
			if i != flex {
				others += w
			}
		}
		avail := max(limit-others, 1)
		for i, row := range rows {
			w := visibleWidth(cell(row, flex))
			if cols[flex].Wrap && w > avail {
				wrapped[i] = true
				continue
			}
			widths[flex] = max(widths[flex], w)
		}
		widths[flex] = min(widths[flex], avail)
	}

	lines := make([]string, 0, len(rows)+1)
	header := make([]string, len(cols))
	for i, col := range cols {
		header[i] = col.Header
	}
	lines = append(lines, r.tableLine(cols, widths, header, limit, g, func(s string) string { return c.Dim(s) }))
	for i, row := range rows {
		if !wrapped[i] {
			lines = append(lines, r.tableLine(cols, widths, row, limit, g, func(s string) string { return s }))
			continue
		}
		cut := append([]string(nil), row...)
		cut[flex] = ""
		lines = append(lines, r.tableLine(cols, widths, cut, limit, g, func(s string) string { return s }))
		for _, part := range wrapVisible(row[flex], max(limit-wrappedIndent, 1)) {
			lines = append(lines, strings.Repeat(" ", wrappedIndent)+c.Dim(part))
		}
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
