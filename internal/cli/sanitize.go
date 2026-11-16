package cli

import (
	"strings"
	"unicode"
)

// SanitizeForTerminal makes text from outside the program (YouTrack issue
// keys and summaries, repository paths, branch names, server error messages)
// safe to print: control characters (including ESC, CR, newlines, tabs, DEL
// and the C1 range) and Unicode bidi override/isolate characters are dropped,
// so the text cannot move the cursor, rewrite the screen, set the window
// title or reorder surrounding output. Runs of whitespace left behind are
// collapsed to one space and the result is trimmed.
//
// Dropping the ESC byte alone defuses escape sequences: the rest of the
// sequence remains as inert printable text.
func SanitizeForTerminal(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			b.WriteByte(' ')
		case unicode.IsControl(r), isBidiControl(r):
			// dropped
		default:
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func isBidiControl(r rune) bool {
	return r >= 0x202A && r <= 0x202E || r >= 0x2066 && r <= 0x2069 ||
		r == 0x200E || r == 0x200F || r == 0x061C
}
