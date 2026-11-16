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

var wrapCols = []Column{
	{Header: "#", Right: true},
	{Header: "Ticket"},
	{Header: "Reason", Flex: true, Wrap: true},
	{Header: "State"},
}

func wrapRows(r Renderer) [][]string {
	return [][]string{
		{"1", "TICKT-47", "branch name", r.Colors.OK("ok")},
		{"2", "-", "learned from earlier days of the same repository and branch", ""},
		{"3", "TICKT-9", "", r.Colors.Warn("needs ticket")},
		{"4", "TICKT-1", "https://example.invalid/" + strings.Repeat("x", 90), ""},
		{"5", "TICKT-2", "Ünïcödé reasön wïth ümlauts that is rather long and has to wrap somewhere", ""},
	}
}

func TestTableWrapGolden(t *testing.T) {
	for _, v := range variants() {
		t.Run(v.name, func(t *testing.T) {
			lines := v.r.Table(wrapCols, wrapRows(v.r))
			assertGolden(t, "table_wrap_"+v.name, strings.Join(lines, "\n")+"\n")
		})
	}
}

func TestTableWrapNarrowWidths(t *testing.T) {
	for _, v := range variants() {
		for _, width := range []int{40, 50, 60, 78, 100} {
			r := v.r
			r.Width = width
			for _, line := range r.Table(wrapCols, wrapRows(r)) {
				if n := visibleWidth(line); n > width {
					t.Fatalf("%s width %d: line %q has %d visible runes", v.name, width, line, n)
				}
			}
		}
	}
}

func TestTableWrapKeepsEveryReasonWhole(t *testing.T) {
	reasons := []string{
		"learned from earlier days of the same repository and branch",
		"https://example.invalid/" + strings.Repeat("x", 90),
		"a b c d e f g h i j k l m n o p q r s t u v w x y z",
	}
	for _, width := range []int{40, 60, 78} {
		r := Renderer{Glyphs: asciiGlyphs, Width: width}
		rows := make([][]string, 0, len(reasons))
		for i, reason := range reasons {
			rows = append(rows, []string{"1", "TICKT-" + string(rune('1'+i)), reason, ""})
		}
		out := strings.Join(r.Table(wrapCols, rows), "\n")
		squashed := strings.Join(strings.Fields(out), "")
		for _, reason := range reasons {
			if !strings.Contains(squashed, strings.Join(strings.Fields(reason), "")) {
				t.Errorf("width %d: reason %q lost text in\n%s", width, reason, out)
			}
		}
		if strings.Contains(out, "...") {
			t.Errorf("width %d: wrapped column was cut with an ellipsis:\n%s", width, out)
		}
	}
}

func TestTableWrapFittingReasonStaysInline(t *testing.T) {
	r := Renderer{Glyphs: asciiGlyphs, Width: 78}
	lines := r.Table(wrapCols, [][]string{{"1", "TICKT-1", "branch name", "ok"}})
	if len(lines) != 2 || !strings.Contains(lines[1], "branch name") {
		t.Errorf("got %q, want header and one inline row", lines)
	}
}

func TestTableWrapDecidesByMeasuringNotSearching(t *testing.T) {
	// The short reason "ok" also appears in another cell; a cut must still be
	// detected for the long one and never confused with the short one.
	r := Renderer{Glyphs: asciiGlyphs, Width: 40}
	cols := []Column{{Header: "A"}, {Header: "Reason", Flex: true, Wrap: true}}
	rows := [][]string{{"ok", "ok"}, {"x", strings.Repeat("word ", 12)}}
	lines := r.Table(cols, rows)
	if lines[1] != "  ok  ok" {
		t.Errorf("short reason must stay inline, got %q", lines[1])
	}
	if lines[2] != "  x" {
		t.Errorf("long reason's cell must be emptied, got %q", lines[2])
	}
}

func TestTableWrapContinuationLinesAreDimAndIndented(t *testing.T) {
	r := Renderer{Colors: Colors{Enabled: true}, Glyphs: asciiGlyphs, Width: 40}
	cols := []Column{{Header: "A"}, {Header: "Reason", Flex: true, Wrap: true}}
	lines := r.Table(cols, [][]string{{"x", strings.Repeat("word ", 12)}})
	for _, l := range lines[2:] {
		if want := "    " + r.Colors.Dim(strings.TrimSpace(stripANSI(l))); l != want {
			t.Errorf("continuation %q, want dim text indented by 4: %q", l, want)
		}
	}
}

func TestTableWrapWithoutFlexOrWrapIsUnchanged(t *testing.T) {
	r := Renderer{Glyphs: asciiGlyphs, Width: 40}
	long := strings.Repeat("word ", 12)
	if got := r.Table([]Column{{Header: "A"}, {Header: "R", Wrap: true}}, [][]string{{"x", long}}); len(got) != 2 {
		t.Errorf("Wrap without Flex must not wrap, got %q", got)
	}
}

func TestWrapVisible(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		width int
		want  []string
	}{
		{"empty", "", 10, nil},
		{"fits", "a b c", 10, []string{"a b c"}},
		{"breaks at spaces", "aaa bbb ccc", 7, []string{"aaa bbb", "ccc"}},
		{"long word split without loss", "abcdefghij", 4, []string{"abcd", "efgh", "ij"}},
		{"long word after short", "a bcdefgh", 4, []string{"a", "bcde", "fgh"}},
		{"runes not bytes", "äöüäöü", 4, []string{"äöüä", "öü"}},
		{"zero width treated as one", "ab", 0, []string{"a", "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wrapVisible(tt.in, tt.width)
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Errorf("wrapVisible(%q, %d) = %q, want %q", tt.in, tt.width, got, tt.want)
			}
		})
	}
}

func TestWrapVisibleAnsiDoesNotCount(t *testing.T) {
	c := Colors{Enabled: true}
	got := wrapVisible(c.OK("abcdef"), 4)
	if len(got) != 2 || visibleWidth(got[0]) != 4 || visibleWidth(got[1]) != 2 {
		t.Fatalf("got %q", got)
	}
	if !strings.HasSuffix(got[0], ansiReset) {
		t.Errorf("cut coloured line must end in a reset: %q", got[0])
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if l := ansiLen(s[i:]); l > 0 {
			i += l
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func TestTableStacksColumnsThatDoNotFit(t *testing.T) {
	cols := []Column{
		{Header: "#", Right: true},
		{Header: "Time"},
		{Header: "Reason", Flex: true, Wrap: true},
		{Header: "State", Stack: true},
	}
	rows := [][]string{
		{"1", "09:00-10:00", "because", "needs ticket assignment"},
		{"2", "10:00-11:00", "x", ""},
	}
	t.Run("narrow layout moves the stacked cell below its row", func(t *testing.T) {
		got := Renderer{Glyphs: asciiGlyphs, Width: 40}.Table(cols, rows)
		want := []string{
			"  #  Time         Reason",
			"  1  09:00-10:00  because",
			"    needs ticket assignment",
			"  2  10:00-11:00  x",
		}
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	})
	t.Run("wide layout keeps it inline", func(t *testing.T) {
		got := Renderer{Glyphs: asciiGlyphs, Width: 78}.Table(cols, rows)
		want := []string{
			"  #  Time         Reason   State",
			"  1  09:00-10:00  because  needs ticket assignment",
			"  2  10:00-11:00  x",
		}
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	})
}

func TestTableStacksSeveralColumnsOnOneLine(t *testing.T) {
	cols := []Column{
		{Header: "Name"},
		{Header: "Tier", Stack: true},
		{Header: "State", Stack: true},
	}
	rows := [][]string{
		{"alpha", "rule", "confirmed"},
		{"beta", "", "needs ticket"},
		{"gamma", "", ""},
	}
	got := Renderer{Glyphs: asciiGlyphs, Width: 20}.Table(cols, rows)
	want := []string{
		"  Name",
		"  alpha",
		"    rule  confirmed",
		"  beta",
		"    needs ticket",
		"  gamma",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
