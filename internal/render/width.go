package render

import (
	"strings"
	"unicode/utf8"
)

const (
	// defaultLayoutWidth is the layout width when there is no terminal.
	defaultLayoutWidth = 78
	// maxLayoutWidth caps the layout width on wide terminals.
	maxLayoutWidth = 100
	// minLayoutWidth keeps the layout readable on tiny terminals.
	minLayoutWidth = 40
)

// layoutWidth returns the width of rules, boxes and tables. Without a terminal
// (width <= 0) it is a fixed default; on a terminal it is capped unless wide
// is set, and never below minLayoutWidth.
func layoutWidth(width int, wide bool) int {
	switch {
	case width <= 0:
		return defaultLayoutWidth
	case !wide && width > maxLayoutWidth:
		return maxLayoutWidth
	case width < minLayoutWidth:
		return minLayoutWidth
	default:
		return width
	}
}

// ansiLen returns the length of the ANSI colour sequence (ESC [ ... m) at the
// start of s, or 0 when s does not start with one.
func ansiLen(s string) int {
	if len(s) < 2 || s[0] != 0x1b || s[1] != '[' {
		return 0
	}
	end := strings.IndexByte(s, 'm')
	if end < 0 {
		return len(s)
	}
	return end + 1
}

// visibleWidth counts the runes of s that are shown on screen; ANSI colour
// sequences are skipped.
func visibleWidth(s string) int {
	n := 0
	for i := 0; i < len(s); {
		if l := ansiLen(s[i:]); l > 0 {
			i += l
			continue
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
		n++
	}
	return n
}

// padVisible pads s with spaces to width visible runes; it never shortens s.
func padVisible(s string, width int) string {
	if n := width - visibleWidth(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// padLeftVisible left-pads s with spaces to width visible runes.
func padLeftVisible(s string, width int) string {
	if n := width - visibleWidth(s); n > 0 {
		return strings.Repeat(" ", n) + s
	}
	return s
}

// fit shortens s to at most width visible runes, ending in ell when it had to
// cut. ANSI sequences are kept and do not count; a cut coloured string ends in
// a reset. A width of 0 or less means unlimited.
func fit(s string, width int, ell string) string {
	if width <= 0 || visibleWidth(s) <= width {
		return s
	}
	ellRunes := []rune(ell)
	keep := max(width-len(ellRunes), 0)
	if width < len(ellRunes) {
		ellRunes = ellRunes[:width]
	}
	var b strings.Builder
	sawEscape := false
	shown := 0
	for i := 0; i < len(s) && shown < keep; {
		if l := ansiLen(s[i:]); l > 0 {
			b.WriteString(s[i : i+l])
			sawEscape = true
			i += l
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		b.WriteRune(r)
		i += size
		shown++
	}
	b.WriteString(string(ellRunes))
	if sawEscape {
		b.WriteString(ansiReset)
	}
	return b.String()
}
