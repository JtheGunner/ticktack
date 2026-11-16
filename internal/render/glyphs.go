package render

import "strings"

// Glyphs is the set of symbols used for status markers, rules and boxes.
type Glyphs struct {
	Info, OK, Warn, Fail, Skip, Go, Dot, Ellipsis string
	// H and V are the horizontal and vertical line characters; TL, TR, BL and
	// BR are the corners of a box.
	H, V, TL, TR, BL, BR string
}

var unicodeGlyphs = Glyphs{
	Info: "ℹ", OK: "✓", Warn: "⚠", Fail: "✗", Skip: "–", Go: "→", Dot: "·", Ellipsis: "…",
	H: "─", V: "│", TL: "╭", TR: "╮", BL: "╰", BR: "╯",
}

var asciiGlyphs = Glyphs{
	Info: "i", OK: "+", Warn: "!", Fail: "x", Skip: "-", Go: ">", Dot: "*", Ellipsis: "...",
	H: "-", V: "|", TL: "+", TR: "+", BL: "+", BR: "+",
}

// GlyphsFromEnv picks the Unicode symbols when the locale announces UTF-8 and
// the terminal is not "dumb"; otherwise it falls back to ASCII. Of LC_ALL,
// LC_CTYPE and LANG the first non-empty value decides, as in POSIX.
func GlyphsFromEnv(getenv func(string) string) Glyphs {
	if getenv("TERM") == "dumb" {
		return asciiGlyphs
	}
	for _, key := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		locale := strings.ToLower(getenv(key))
		if locale == "" {
			continue
		}
		if strings.Contains(locale, "utf-8") || strings.Contains(locale, "utf8") {
			return unicodeGlyphs
		}
		return asciiGlyphs
	}
	return asciiGlyphs
}
