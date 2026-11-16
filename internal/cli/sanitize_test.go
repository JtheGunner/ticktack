package cli

import (
	"strings"
	"testing"
)

func TestSanitizeForTerminal(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"plain unicode kept", "Fix Übergröße – 日本語 ✓", "Fix Übergröße – 日本語 ✓"},
		{"clear screen CSI", "a\x1b[2Jb", "a[2Jb"},
		{"OSC title set", "x\x1b]0;pwned\x07y", "x]0;pwnedy"},
		{"CR overwrite", "real\rfake", "real fake"},
		{"newline and tab collapse", "a\n\n\tb", "a b"},
		{"bidi override", "abc\u202Edef\u2066g\u2069", "abcdefg"},
		{"DEL and C1", "a\x7fb\u009bc", "abc"},
		{"trim", "  x  ", "x"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeForTerminal(tt.in)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func assertNoControl(t *testing.T, out string) {
	t.Helper()
	for _, r := range out {
		if r == '\n' {
			continue
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) || (r >= 0x202A && r <= 0x202E) {
			t.Fatalf("output contains control rune %U:\n%q", r, out)
		}
	}
}

func TestSanitizeNoControlBytesSurvive(t *testing.T) {
	var all strings.Builder
	for r := rune(0); r < 0xA0; r++ {
		all.WriteRune(r)
	}
	assertNoControl(t, SanitizeForTerminal(all.String()))
}
