package render

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestLayoutWidth(t *testing.T) {
	tests := []struct {
		name  string
		width int
		wide  bool
		want  int
	}{
		{"no terminal", 0, false, 78},
		{"negative", -5, false, 78},
		{"no terminal wide", 0, true, 78},
		{"narrow terminal", 20, false, 40},
		{"exactly minimum", 40, false, 40},
		{"normal", 80, false, 80},
		{"at cap", 100, false, 100},
		{"above cap", 160, false, 100},
		{"above cap wide", 160, true, 160},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := layoutWidth(tt.width, tt.wide); got != tt.want {
				t.Errorf("layoutWidth(%d, %v) = %d, want %d", tt.width, tt.wide, got, tt.want)
			}
		})
	}
}

func TestVisibleWidthAndPadVisible(t *testing.T) {
	c := Colors{Enabled: true}
	tests := []struct {
		name string
		in   string
		want int
	}{
		{"empty", "", 0},
		{"ascii", "abc", 3},
		{"multibyte", "né…", 3},
		{"coloured", c.OK("abc"), 3},
		{"nested", c.Bold(c.Dim("ab")) + "c", 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := visibleWidth(tt.in); got != tt.want {
				t.Errorf("visibleWidth(%q) = %d, want %d", tt.in, got, tt.want)
			}
			padded := padVisible(tt.in, 6)
			if got := visibleWidth(padded); got != max(tt.want, 6) {
				t.Errorf("padVisible(%q, 6) visible = %d, want %d", tt.in, got, max(tt.want, 6))
			}
		})
	}
	if got := visibleWidth("ab\x1b[3"); got != 2 {
		t.Errorf("unterminated escape: visibleWidth = %d, want 2", got)
	}
	if got := padVisible("abcdef", 3); got != "abcdef" {
		t.Errorf("padVisible must not shorten, got %q", got)
	}
}

func TestFit(t *testing.T) {
	c := Colors{Enabled: true}
	tests := []struct {
		name  string
		in    string
		width int
		ell   string
		want  string
	}{
		{"fits", "abc", 5, "…", "abc"},
		{"exact", "abcde", 5, "…", "abcde"},
		{"cut with unicode ellipsis", "abcdefgh", 5, "…", "abcd…"},
		{"cut with ascii ellipsis", "abcdefgh", 5, "...", "ab..."},
		{"width smaller than ellipsis", "abcdefgh", 2, "...", ".."},
		{"width one", "abcdefgh", 1, "…", "…"},
		{"unlimited", "abcdefgh", 0, "…", "abcdefgh"},
		{"multibyte", "ünïcödé", 4, "…", "ünï…"},
		{"coloured fits", c.OK("abc"), 5, "…", c.OK("abc")},
		{"coloured cut", c.OK("abcdefgh"), 5, "…", "\x1b[32mabcd…\x1b[0m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fit(tt.in, tt.width, tt.ell)
			if got != tt.want {
				t.Errorf("fit(%q, %d, %q) = %q, want %q", tt.in, tt.width, tt.ell, got, tt.want)
			}
			if tt.width > 0 && visibleWidth(got) > tt.width {
				t.Errorf("fit result %q is %d visible runes, over width %d", got, visibleWidth(got), tt.width)
			}
		})
	}
}

func TestFitNeverExceedsWidth(t *testing.T) {
	c := Colors{Enabled: true}
	inputs := []string{"", "a", strings.Repeat("x", 50), "ünïcödé wörld", c.Warn(strings.Repeat("y", 30)) + " tail"}
	for _, in := range inputs {
		for _, ell := range []string{"…", "..."} {
			for width := 1; width <= 40; width++ {
				got := fit(in, width, ell)
				if v := visibleWidth(got); v > width {
					t.Fatalf("fit(%q, %d, %q) = %q: %d visible > %d", in, width, ell, got, v, width)
				}
				if !utf8.ValidString(got) {
					t.Fatalf("fit(%q, %d) produced invalid UTF-8", in, width)
				}
			}
		}
	}
}
