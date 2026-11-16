package render

import "testing"

func TestGlyphsFromEnv(t *testing.T) {
	tests := []struct {
		name        string
		env         map[string]string
		wantUnicode bool
	}{
		{"empty env", nil, false},
		{"LANG utf-8", map[string]string{"LANG": "en_US.UTF-8"}, true},
		{"LANG utf8 spelling", map[string]string{"LANG": "de_CH.utf8"}, true},
		{"LANG C", map[string]string{"LANG": "C"}, false},
		{"LC_CTYPE beats LANG (ascii wins)", map[string]string{"LC_CTYPE": "C", "LANG": "en_US.UTF-8"}, false},
		{"LC_CTYPE beats LANG (utf-8 wins)", map[string]string{"LC_CTYPE": "en_US.UTF-8", "LANG": "C"}, true},
		{"LC_ALL beats LC_CTYPE (ascii wins)", map[string]string{"LC_ALL": "POSIX", "LC_CTYPE": "en_US.UTF-8"}, false},
		{"LC_ALL beats LC_CTYPE (utf-8 wins)", map[string]string{"LC_ALL": "en_US.UTF-8", "LC_CTYPE": "C"}, true},
		{"empty LC_ALL falls through", map[string]string{"LC_ALL": "", "LANG": "en_US.UTF-8"}, true},
		{"TERM dumb forces ascii", map[string]string{"TERM": "dumb", "LC_ALL": "en_US.UTF-8"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := asciiGlyphs
			if tt.wantUnicode {
				want = unicodeGlyphs
			}
			if got := GlyphsFromEnv(envOf(tt.env)); got != want {
				t.Errorf("GlyphsFromEnv(%v) = %+v, want %+v", tt.env, got, want)
			}
		})
	}
}

func TestGlyphSetsDifferentiateFailAndSkip(t *testing.T) {
	if unicodeGlyphs.Fail != "✗" || unicodeGlyphs.Skip != "–" {
		t.Errorf("unicode Fail/Skip = %q/%q", unicodeGlyphs.Fail, unicodeGlyphs.Skip)
	}
	if asciiGlyphs.Fail != "x" || asciiGlyphs.Skip != "-" {
		t.Errorf("ascii Fail/Skip = %q/%q", asciiGlyphs.Fail, asciiGlyphs.Skip)
	}
	for name, g := range map[string]Glyphs{"unicode": unicodeGlyphs, "ascii": asciiGlyphs} {
		seen := map[string]string{}
		for label, s := range map[string]string{"Info": g.Info, "OK": g.OK, "Warn": g.Warn, "Fail": g.Fail, "Skip": g.Skip, "Go": g.Go} {
			if s == "" {
				t.Errorf("%s: %s glyph is empty", name, label)
			}
			if prev, dup := seen[s]; dup {
				t.Errorf("%s: %s and %s share glyph %q", name, label, prev, s)
			}
			seen[s] = label
		}
	}
}
