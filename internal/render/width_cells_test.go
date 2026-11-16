package render

import (
	"strings"
	"testing"
)

func TestVisibleWidthCountsTerminalCells(t *testing.T) {
	c := Colors{Enabled: true}
	tests := []struct {
		name string
		in   string
		want int
	}{
		{"wide CJK", "日本", 4},
		{"mixed ascii and CJK", "a日b", 4},
		{"fullwidth latin", "Ａ", 2},
		{"emoji", "👍", 2},
		{"combining accent", "é", 1},
		{"ellipsis stays narrow", "…", 1},
		{"coloured CJK", c.OK("日本"), 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := visibleWidth(tt.in); got != tt.want {
				t.Errorf("visibleWidth(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestFitNeverExceedsWidthInCells(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		width int
		want  string
	}{
		{"fits", "日本", 4, "日本"},
		{"cut between wide runes", "日本語テキスト", 5, "日本…"},
		{"wide rune does not fit in the last cell", "ab日本", 4, "ab…"},
		{"ascii before wide", "a日本", 4, "a日…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fit(tt.in, tt.width, "…")
			if got != tt.want {
				t.Errorf("fit(%q, %d) = %q, want %q", tt.in, tt.width, got, tt.want)
			}
			if w := visibleWidth(got); w > tt.width {
				t.Errorf("fit(%q, %d) is %d cells wide", tt.in, tt.width, w)
			}
		})
	}
}

func TestWrapVisibleBreaksByCells(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		width int
		want  []string
	}{
		{"words wrap at spaces", "日本語 日本語", 6, []string{"日本語", "日本語"}},
		{"long word splits without loss", "日本語日本語", 5, []string{"日本", "語日", "本語"}},
		{"wide rune wider than the line still advances", "日日", 1, []string{"日", "日"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wrapVisible(tt.in, tt.width)
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Fatalf("wrapVisible(%q, %d) = %q, want %q", tt.in, tt.width, got, tt.want)
			}
		})
	}
}

func TestSplitVisibleKeepsWideRunesWhole(t *testing.T) {
	head, rest := splitVisible("日本語", 5)
	if head != "日本" || rest != "語" {
		t.Errorf("splitVisible = %q, %q; want %q, %q", head, rest, "日本", "語")
	}
}

func TestTableAlignsColumnsByCells(t *testing.T) {
	r := Plain()
	lines := r.Table(
		[]Column{{Header: "Name"}, {Header: "State"}},
		[][]string{{"日本語", "ok"}, {"abc", "ok"}},
	)
	var starts []int
	for _, line := range lines[1:] {
		i := strings.LastIndex(line, "ok")
		if i < 0 {
			t.Fatalf("no State cell in %q", line)
		}
		starts = append(starts, visibleWidth(line[:i]))
	}
	if starts[0] != starts[1] {
		t.Errorf("State column starts at cells %v, want one position\n%s", starts, strings.Join(lines, "\n"))
	}
}
