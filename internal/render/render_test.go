package render

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run with -update)", path, err)
	}
	if got != string(want) {
		t.Errorf("%s mismatch\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

const testWidth = 60

type variant struct {
	name string
	r    Renderer
}

func variants() []variant {
	return []variant{
		{"unicode_color", Renderer{Colors: Colors{Enabled: true}, Glyphs: unicodeGlyphs, Width: testWidth}},
		{"unicode_plain", Renderer{Glyphs: unicodeGlyphs, Width: testWidth}},
		{"ascii_plain", Renderer{Glyphs: asciiGlyphs, Width: testWidth}},
	}
}

func TestZeroValueRendererEqualsPlain(t *testing.T) {
	zero, plain := Renderer{}, Plain()
	if zero.Rule("Title") != plain.Rule("Title") {
		t.Error("Rule differs")
	}
	if zero.Box("Title", []string{"a", "b"}) != plain.Box("Title", []string{"a", "b"}) {
		t.Error("Box differs")
	}
	if zero.Heading("H") != plain.Heading("H") {
		t.Error("Heading differs")
	}
	for _, s := range []Status{StatusInfo, StatusOK, StatusWarn, StatusFail, StatusSkip, StatusGo} {
		if zero.Gutter(s, "x") != plain.Gutter(s, "x") {
			t.Errorf("Gutter(%d) differs", s)
		}
	}
	rows := []LabelRow{{"a", "1"}, {"bbb", "2"}}
	if strings.Join(zero.Labels(rows, 2), "\n") != strings.Join(plain.Labels(rows, 2), "\n") {
		t.Error("Labels differ")
	}
	if strings.Join(zero.Table(sampleCols, sampleRows(zero)), "\n") != strings.Join(plain.Table(sampleCols, sampleRows(plain)), "\n") {
		t.Error("Table differs")
	}
	if got := plain.Rule(""); len([]rune(got)) != 78 || strings.ContainsRune(got, 0x1b) {
		t.Errorf("Plain rule = %q, want 78 plain runes", got)
	}
}

func TestPlainAndZeroOutputNeverContainsEscape(t *testing.T) {
	for _, r := range []Renderer{Plain(), {}, {Glyphs: unicodeGlyphs, Width: 50}} {
		var out []string
		out = append(out, r.Rule("T"), r.Rule(""), r.Box("T", []string{"x"}), r.Heading("H"))
		for _, s := range []Status{StatusInfo, StatusOK, StatusWarn, StatusFail, StatusSkip, StatusGo} {
			out = append(out, r.Gutter(s, "text"))
		}
		out = append(out, r.Labels([]LabelRow{{"k", "v"}}, 2)...)
		out = append(out, r.Table(sampleCols, sampleRows(r))...)
		if s := strings.Join(out, "\n"); strings.ContainsRune(s, 0x1b) {
			t.Errorf("plain output contains ESC: %q", s)
		}
	}
}

func TestRuleGolden(t *testing.T) {
	for _, v := range variants() {
		t.Run(v.name, func(t *testing.T) {
			got := strings.Join([]string{v.r.Rule(""), v.r.Rule("Blocks for 2026-11-16"), v.r.Rule(strings.Repeat("long title ", 10))}, "\n") + "\n"
			assertGolden(t, "rule_"+v.name, got)
		})
	}
}

func TestBoxGolden(t *testing.T) {
	for _, v := range variants() {
		t.Run(v.name, func(t *testing.T) {
			rows := []string{"short", v.r.Colors.OK("coloured row"), strings.Repeat("overlong row ", 8)}
			got := v.r.Box("Summary", rows) + "\n" + v.r.Box("", []string{"untitled"}) + "\n"
			assertGolden(t, "box_"+v.name, got)
			for _, line := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
				if n := visibleWidth(line); n != testWidth {
					t.Errorf("box line %q is %d wide, want %d", line, n, testWidth)
				}
			}
		})
	}
}

func TestGutterGolden(t *testing.T) {
	for _, v := range variants() {
		t.Run(v.name, func(t *testing.T) {
			var lines []string
			for _, s := range []Status{StatusInfo, StatusOK, StatusWarn, StatusFail, StatusSkip, StatusGo} {
				lines = append(lines, v.r.Gutter(s, "status text"))
			}
			assertGolden(t, "gutter_"+v.name, strings.Join(lines, "\n")+"\n")
		})
	}
}

func TestGutterDistinguishableWithoutColour(t *testing.T) {
	r := Plain()
	seen := map[string]Status{}
	for _, s := range []Status{StatusInfo, StatusOK, StatusWarn, StatusFail, StatusSkip, StatusGo} {
		prefix := r.Gutter(s, "")
		if prev, dup := seen[prefix]; dup {
			t.Errorf("statuses %d and %d render the same plain prefix %q", prev, s, prefix)
		}
		seen[prefix] = s
	}
}

func TestLabelsGolden(t *testing.T) {
	for _, v := range variants() {
		t.Run(v.name, func(t *testing.T) {
			rows := []LabelRow{{"Ticket", "TICKT-47"}, {"Repo", "ticktack"}, {"Confidence", "explicit"}}
			assertGolden(t, "labels_"+v.name, strings.Join(v.r.Labels(rows, 2), "\n")+"\n")
		})
	}
}

func TestHeadingGolden(t *testing.T) {
	for _, v := range variants() {
		t.Run(v.name, func(t *testing.T) {
			assertGolden(t, "heading_"+v.name, v.r.Heading("Totals")+"\n")
		})
	}
}

func TestNewForNonTerminalWriter(t *testing.T) {
	tests := []struct {
		name        string
		opts        Options
		wantGlyphs  Glyphs
		wantWidth   int
		wantColours bool
	}{
		{"nil getenv is plain ascii", Options{}, asciiGlyphs, 78, false},
		{"utf-8 locale gives unicode", Options{Getenv: envOf(map[string]string{"LANG": "en_US.UTF-8"})}, unicodeGlyphs, 78, false},
		{"term size ignored without a tty", Options{TermSize: func(int) (int, int, error) { return 120, 40, nil }}, asciiGlyphs, 78, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := New(&strings.Builder{}, tt.opts)
			if r.Glyphs != tt.wantGlyphs || r.Width != tt.wantWidth || r.Colors.Enabled != tt.wantColours {
				t.Errorf("New() = %+v, want glyphs %+v width %d colours %v", r, tt.wantGlyphs, tt.wantWidth, tt.wantColours)
			}
		})
	}
}
