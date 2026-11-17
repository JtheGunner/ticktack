package render

import "testing"

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestNewColorsGatingMatrix(t *testing.T) {
	tests := []struct {
		name  string
		isTTY bool
		flag  bool
		env   map[string]string
		want  bool
	}{
		{"tty, nothing else", true, false, nil, true},
		{"tty with TERM xterm", true, false, map[string]string{"TERM": "xterm-256color"}, true},
		{"not a tty", false, false, nil, false},
		{"tty with --no-color", true, true, nil, false},
		{"tty with NO_COLOR set", true, false, map[string]string{"NO_COLOR": "1"}, false},
		{"tty with NO_COLOR empty", true, false, map[string]string{"NO_COLOR": ""}, true},
		{"tty with TERM dumb", true, false, map[string]string{"TERM": "dumb"}, false},
		{"not a tty, flag, NO_COLOR, dumb", false, true, map[string]string{"NO_COLOR": "1", "TERM": "dumb"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewColors(tt.isTTY, tt.flag, envOf(tt.env))
			if got.Enabled != tt.want {
				t.Errorf("NewColors(%v, %v, %v).Enabled = %v, want %v", tt.isTTY, tt.flag, tt.env, got.Enabled, tt.want)
			}
		})
	}
}

func TestColorsStyleMethods(t *testing.T) {
	on := Colors{Enabled: true}
	off := Colors{}
	tests := []struct {
		name string
		fn   func(Colors, string) string
		code string
	}{
		{"Bold", Colors.Bold, "\x1b[1m"},
		{"Dim", Colors.Dim, "\x1b[2m"},
		{"Accent", Colors.Accent, "\x1b[36m"},
		{"Placeholder", Colors.Placeholder, "\x1b[35m"},
		{"OK", Colors.OK, "\x1b[32m"},
		{"Warn", Colors.Warn, "\x1b[33m"},
		{"Fail", Colors.Fail, "\x1b[31m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, want := tt.fn(on, "x"), tt.code+"x\x1b[0m"; got != want {
				t.Errorf("enabled: got %q, want %q", got, want)
			}
			if got := tt.fn(off, "x"); got != "x" {
				t.Errorf("disabled: got %q, want %q", got, "x")
			}
		})
	}
}
