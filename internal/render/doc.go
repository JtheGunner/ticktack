// Package render builds styled terminal output: colours, glyphs, rules,
// boxes, status lines, label blocks and tables.
//
// The package is pure. Rendering methods read no environment, clock or
// terminal; they depend only on the Renderer value. Terminal detection lives
// in New, which takes every input it needs through Options.
//
// Text that comes from outside the program must be sanitised by the caller
// before it is passed in; colour codes are added last. A zero-value Renderer
// behaves exactly like Plain: ASCII glyphs, no colour, 78 columns.
package render
