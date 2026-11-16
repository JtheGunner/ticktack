package render

const (
	ansiReset  = "\x1b[0m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiCyan   = "\x1b[36m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
)

// Colors decides whether ANSI colour sequences are written. The zero value
// writes none.
type Colors struct {
	Enabled bool
}

// NewColors returns the colour setting for a stream. Colours are off when the
// stream is not a terminal, when noColorFlag is set, when NO_COLOR is set to a
// non-empty value, or when TERM is "dumb".
func NewColors(isTTY, noColorFlag bool, getenv func(string) string) Colors {
	if !isTTY || noColorFlag || getenv("NO_COLOR") != "" || getenv("TERM") == "dumb" {
		return Colors{}
	}
	return Colors{Enabled: true}
}

func (c Colors) paint(code, s string) string {
	if !c.Enabled || s == "" {
		return s
	}
	return code + s + ansiReset
}

// Bold renders s in bold.
func (c Colors) Bold(s string) string { return c.paint(ansiBold, s) }

// Dim renders s de-emphasised; it is used for labels, headers and unknowns.
func (c Colors) Dim(s string) string { return c.paint(ansiDim, s) }

// Accent renders s in the accent colour (cyan).
func (c Colors) Accent(s string) string { return c.paint(ansiCyan, s) }

// OK renders s in green.
func (c Colors) OK(s string) string { return c.paint(ansiGreen, s) }

// Warn renders s in yellow.
func (c Colors) Warn(s string) string { return c.paint(ansiYellow, s) }

// Fail renders s in red.
func (c Colors) Fail(s string) string { return c.paint(ansiRed, s) }

// forStatus returns the painter for a status: green, yellow, red, dim or the
// accent colour.
func (c Colors) forStatus(s Status) func(string) string {
	switch s {
	case StatusOK:
		return c.OK
	case StatusWarn:
		return c.Warn
	case StatusFail:
		return c.Fail
	case StatusSkip:
		return c.Dim
	default:
		return c.Accent
	}
}
