package render

import (
	"io"
	"os"

	"golang.org/x/term"
)

// Renderer holds the style decisions for one output stream. The zero value
// behaves exactly like Plain.
type Renderer struct {
	Colors Colors
	Glyphs Glyphs
	// Width is the layout width in columns; 0 means the default of 78.
	Width int
}

// Plain returns the renderer used when nothing is known about the terminal:
// ASCII glyphs, no colour, 78 columns. It is also the zero value's behaviour.
func Plain() Renderer {
	return Renderer{Glyphs: asciiGlyphs, Width: defaultLayoutWidth}
}

// Options are the inputs New needs to detect the terminal.
type Options struct {
	// NoColor disables colour regardless of the terminal (--no-color).
	NoColor bool
	// Wide lifts the cap on the layout width of wide terminals.
	Wide bool
	// Getenv reads environment variables; nil means an empty environment.
	Getenv func(string) string
	// TermSize reports the terminal size of a file descriptor; nil means
	// golang.org/x/term.GetSize.
	TermSize func(fd int) (width, height int, err error)
	// IsTerminal reports whether w is a terminal and, if so, its file
	// descriptor for TermSize. nil means the real detection: w must be an
	// *os.File that golang.org/x/term reports as a terminal.
	IsTerminal func(w io.Writer) (fd int, ok bool)
}

// New builds the renderer for w. Colour is enabled only when w is a terminal
// and neither Options.NoColor, NO_COLOR nor TERM=dumb say otherwise. Unicode
// glyphs are used when the locale says UTF-8. The layout width follows the
// terminal; without one it is the fixed default.
func New(w io.Writer, o Options) Renderer {
	getenv := o.Getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	termSize := o.TermSize
	if termSize == nil {
		termSize = term.GetSize
	}
	width := 0
	isTTY := false
	isTerminal := o.IsTerminal
	if isTerminal == nil {
		isTerminal = fileTerminal
	}
	if fd, ok := isTerminal(w); ok {
		isTTY = true
		if cols, _, err := termSize(fd); err == nil {
			width = cols
		}
	}
	return Renderer{
		Colors: NewColors(isTTY, o.NoColor, getenv),
		Glyphs: GlyphsFromEnv(getenv),
		Width:  layoutWidth(width, o.Wide),
	}
}

// glyphs returns the renderer's glyphs, ASCII for the zero value.
func (r Renderer) glyphs() Glyphs {
	if r.Glyphs == (Glyphs{}) {
		return asciiGlyphs
	}
	return r.Glyphs
}

// LayoutWidth returns the width rules, boxes and tables are laid out in: the
// renderer's Width, or the default for the zero value.
func (r Renderer) LayoutWidth() int { return r.layoutWidth() }

// layoutWidth returns the renderer's layout width, the default for the zero
// value.
func (r Renderer) layoutWidth() int {
	if r.Width <= 0 {
		return defaultLayoutWidth
	}
	return r.Width
}

// fileTerminal is the real terminal detection: w is an *os.File on a terminal.
func fileTerminal(w io.Writer) (int, bool) {
	f, ok := w.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return 0, false
	}
	return int(f.Fd()), true
}
