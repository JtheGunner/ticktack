package render

import (
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
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
// (width <= 0) it is a fixed default; on a terminal it is capped at
// maxLayoutWidth, and terminals narrower than minLayoutWidth (40 columns) get
// a 40-column layout.
func layoutWidth(width int) int {
	switch {
	case width <= 0:
		return defaultLayoutWidth
	case width > maxLayoutWidth:
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

// cells measures text in terminal cells. East Asian ambiguous-width
// characters count as one cell regardless of the locale, so output and golden
// files do not depend on the environment.
var cells = &runewidth.Condition{StrictEmojiNeutral: true}

// visibleWidth counts the terminal cells of s that are shown on screen; ANSI
// colour sequences are skipped. Wide characters (CJK, emoji) count as two
// cells and combining marks as none.
func visibleWidth(s string) int {
	var plain strings.Builder
	for i := 0; i < len(s); {
		if l := ansiLen(s[i:]); l > 0 {
			i += l
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		plain.WriteRune(r)
		i += size
	}
	return cells.StringWidth(plain.String())
}

// cutIndex returns the byte index after the last rune of s that still fits
// into n cells, and whether an ANSI sequence lies before that index. A wide
// rune that would cross the limit is left for the next piece; zero-width runes
// directly after the last kept rune stay with it. At least one rune is kept
// when n is positive and s has text, so splitting always makes progress.
func cutIndex(s string, n int) (end int, sawEscape bool) {
	shown, seen := 0, false
	for i := 0; i < len(s); {
		if l := ansiLen(s[i:]); l > 0 {
			seen = true
			i += l
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		w := cells.RuneWidth(r)
		if w > 0 && shown+w > n && (shown > 0 || n <= 0) {
			return end, sawEscape
		}
		shown += w
		i += size
		end, sawEscape = i, seen
	}
	return len(s), seen
}

// padVisible pads s with spaces to width terminal cells; it never shortens s.
func padVisible(s string, width int) string {
	if n := width - visibleWidth(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// padLeftVisible left-pads s with spaces to width terminal cells.
func padLeftVisible(s string, width int) string {
	if n := width - visibleWidth(s); n > 0 {
		return strings.Repeat(" ", n) + s
	}
	return s
}

// fit shortens s to at most width terminal cells, ending in ell when it had to
// cut. ANSI sequences are kept and do not count; a cut coloured string ends in
// a reset. A width of 0 or less means unlimited.
func fit(s string, width int, ell string) string {
	if width <= 0 || visibleWidth(s) <= width {
		return s
	}
	if visibleWidth(ell) > width {
		end, _ := cutIndex(ell, width)
		ell = ell[:end]
	}
	end, sawEscape := cutIndex(s, width-visibleWidth(ell))
	out := s[:end] + ell
	if sawEscape {
		out += ansiReset
	}
	return out
}

// wrapVisible breaks s at spaces into lines of at most width terminal cells.
// A word longer than width is split without loss. ANSI sequences do not count;
// a line that was cut inside a coloured word ends in a reset. A width of 0 or
// less is treated as 1.
func wrapVisible(s string, width int) []string {
	width = max(width, 1)
	var lines []string
	line, lineWidth := "", 0
	flush := func() {
		if lineWidth > 0 {
			lines = append(lines, line)
		}
		line, lineWidth = "", 0
	}
	for _, word := range strings.Fields(s) {
		for visibleWidth(word) > width {
			flush()
			head, rest := splitVisible(word, width)
			lines = append(lines, head)
			word = rest
		}
		w := visibleWidth(word)
		if lineWidth > 0 && lineWidth+1+w > width {
			flush()
		}
		if lineWidth > 0 {
			line += " "
			lineWidth++
		}
		line += word
		lineWidth += w
	}
	flush()
	return lines
}

// splitVisible splits s after n terminal cells. A wide rune that would cross
// the limit goes to rest, but head always holds at least one rune. When the
// cut falls inside ANSI-styled text the head ends in a reset.
func splitVisible(s string, n int) (head, rest string) {
	end, sawEscape := cutIndex(s, n)
	head = s[:end]
	if sawEscape && end < len(s) {
		head += ansiReset
	}
	return head, s[end:]
}
