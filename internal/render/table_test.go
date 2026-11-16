package render

import (
	"strings"
	"testing"
)

var sampleCols = []Column{
	{Header: "#", Right: true},
	{Header: "Time"},
	{Header: "Ticket"},
	{Header: "Tier"},
	{Header: "Reason", Flex: true},
}

func sampleRows(r Renderer) [][]string {
	return [][]string{
		{"1", "09:02-10:15", "TICKT-47", r.Colors.OK("explicit"), "branch name"},
		{"12", "10:15-11:00", "TICKT-9", r.Colors.Accent("learned"), "learned from earlier days of the same repository and branch"},
		{"3", "11:00-11:30", "-", r.Colors.Dim("unknown"), ""},
	}
}

func TestTableGolden(t *testing.T) {
	for _, v := range variants() {
		t.Run(v.name, func(t *testing.T) {
			lines := v.r.Table(sampleCols, sampleRows(v.r))
			assertGolden(t, "table_"+v.name, strings.Join(lines, "\n")+"\n")
		})
	}
}

func TestTableLinesNeverExceedWidth(t *testing.T) {
	for _, v := range variants() {
		for width := 8; width <= 100; width += 3 {
			r := v.r
			r.Width = width
			for _, line := range r.Table(sampleCols, sampleRows(r)) {
				if n := visibleWidth(line); n > width {
					t.Fatalf("%s width %d: line %q has %d visible runes", v.name, width, line, n)
				}
			}
		}
	}
}

func TestTableFlexColumnTruncatesWithEllipsis(t *testing.T) {
	cols := []Column{{Header: "Key"}, {Header: "Summary", Flex: true}, {Header: "N", Right: true}}
	rows := [][]string{{"TICKT-1", strings.Repeat("long summary ", 10), "7"}}
	tests := []struct {
		name string
		r    Renderer
		ell  string
	}{
		{"unicode", Renderer{Glyphs: unicodeGlyphs, Width: 40}, "…"},
		{"ascii", Renderer{Glyphs: asciiGlyphs, Width: 40}, "..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := tt.r.Table(cols, rows)
			if len(lines) != 2 {
				t.Fatalf("got %d lines, want header + 1 row: %q", len(lines), lines)
			}
			row := lines[1]
			if !strings.Contains(row, tt.ell) {
				t.Errorf("row %q lacks ellipsis %q", row, tt.ell)
			}
			if n := visibleWidth(row); n != 40 {
				t.Errorf("row is %d visible runes, want exactly 40 (flex fills the line): %q", n, row)
			}
			if !strings.HasSuffix(row, "  7") {
				t.Errorf("right column lost its alignment: %q", row)
			}
		})
	}
}

func TestTableShortContentIsNotPaddedToWidth(t *testing.T) {
	r := Renderer{Glyphs: asciiGlyphs, Width: 78}
	lines := r.Table([]Column{{Header: "A"}, {Header: "B", Flex: true}}, [][]string{{"x", "y"}})
	want := []string{"  A  B", "  x  y"}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Errorf("got %q, want %q", lines, want)
	}
}

func TestTableFixedColumnsOverflowAreTruncatedNotWrapped(t *testing.T) {
	cols := []Column{{Header: "Alpha"}, {Header: "Bravo"}, {Header: "Charlie"}}
	rows := [][]string{{"aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc"}}
	r := Renderer{Glyphs: asciiGlyphs, Width: 20}
	lines := r.Table(cols, rows)
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2 (no wrapping): %q", len(lines), lines)
	}
	for _, l := range lines {
		if n := visibleWidth(l); n > 20 {
			t.Errorf("line %q is %d wide", l, n)
		}
	}
	if !strings.HasSuffix(lines[1], "...") {
		t.Errorf("truncated row should end in ellipsis: %q", lines[1])
	}
}

func TestTableEdgeCases(t *testing.T) {
	r := Plain()
	if got := r.Table(nil, nil); len(got) != 0 {
		t.Errorf("no columns: got %q, want empty", got)
	}
	got := r.Table([]Column{{Header: "A"}}, nil)
	if len(got) != 1 || strings.TrimSpace(got[0]) != "A" {
		t.Errorf("no rows: got %q, want header only", got)
	}
	short := r.Table([]Column{{Header: "A"}, {Header: "B"}}, [][]string{{"only"}})
	if len(short) != 2 || strings.TrimSpace(short[1]) != "only" {
		t.Errorf("short row: got %q", short)
	}
}

func TestTableColouredCellsAlignByVisibleWidth(t *testing.T) {
	r := Renderer{Colors: Colors{Enabled: true}, Glyphs: asciiGlyphs, Width: 78}
	lines := r.Table([]Column{{Header: "Tier"}, {Header: "N", Right: true}}, [][]string{
		{r.Colors.OK("explicit"), "1"},
		{"rule", "22"},
	})
	if a, b := visibleWidth(lines[1]), visibleWidth(lines[2]); a != b {
		t.Errorf("rows have different visible widths %d vs %d", a, b)
	}
}
