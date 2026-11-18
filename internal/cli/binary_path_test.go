package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHomebrewOptLink(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		want   string
		wantOK bool
	}{
		{"apple silicon cellar", "/opt/homebrew/Cellar/ticktack/0.3.5/bin/ticktack", "/opt/homebrew/opt/ticktack/bin/ticktack", true},
		{"intel cellar", "/usr/local/Cellar/ticktack/0.3.5/bin/ticktack", "/usr/local/opt/ticktack/bin/ticktack", true},
		{"revisioned version", "/opt/homebrew/Cellar/ticktack/0.3.5_1/bin/ticktack", "/opt/homebrew/opt/ticktack/bin/ticktack", true},
		{"versioned formula name", "/opt/homebrew/Cellar/ticktack@0.3/0.3.5/bin/ticktack", "/opt/homebrew/opt/ticktack@0.3/bin/ticktack", true},
		{"homebrew bin link", "/opt/homebrew/bin/ticktack", "", false},
		{"go install", "/Users/u/go/bin/ticktack", "", false},
		{"cellar without bin", "/opt/homebrew/Cellar/ticktack/0.3.5/ticktack", "", false},
		{"cellar with nested bin", "/opt/homebrew/Cellar/ticktack/0.3.5/libexec/bin/ticktack", "", false},
		{"cellar directory only", "/opt/homebrew/Cellar/ticktack/0.3.5/bin", "", false},
		{"relative path", "Cellar/ticktack/0.3.5/bin/ticktack", "", false},
		{"empty", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := homebrewOptLink(tt.path)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("homebrewOptLink(%q) = %q, %v; want %q, %v", tt.path, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestStableBinaryPath(t *testing.T) {
	const (
		cellar  = "/opt/homebrew/Cellar/ticktack/0.3.5/bin/ticktack"
		optLink = "/opt/homebrew/opt/ticktack/bin/ticktack"
		binLink = "/opt/homebrew/bin/ticktack"
	)
	tests := []struct {
		name     string
		invoked  string
		resolved string
		// links maps a symlink to the file it resolves to.
		links map[string]string
		want  string
	}{
		{"cellar binary maps to the opt link", cellar, cellar, map[string]string{optLink: cellar}, optLink},
		{"bin link resolving into the cellar maps to the opt link", binLink, cellar, map[string]string{optLink: cellar, binLink: cellar}, optLink},
		{"missing opt link keeps the invoked path", binLink, cellar, map[string]string{binLink: cellar}, binLink},
		{
			"opt link of another version keeps the invoked path", binLink, cellar,
			map[string]string{optLink: "/opt/homebrew/Cellar/ticktack/0.3.4/bin/ticktack", binLink: cellar}, binLink,
		},
		{"cellar binary without opt link stays as invoked", cellar, cellar, nil, cellar},
		{"plain symlink stays unresolved", "/Users/u/bin/ticktack", "/Users/u/src/ticktack/ticktack", map[string]string{"/Users/u/bin/ticktack": "/Users/u/src/ticktack/ticktack"}, "/Users/u/bin/ticktack"},
		{"go install path is unchanged", "/Users/u/go/bin/ticktack", "/Users/u/go/bin/ticktack", nil, "/Users/u/go/bin/ticktack"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolve := func(p string) string {
				if target, ok := tt.links[p]; ok {
					return target
				}
				return p
			}
			sameFile := func(a, b string) bool {
				_, aLink := tt.links[a]
				// A path that is neither a known link nor the real binary does not exist.
				if !aLink && a != tt.resolved {
					return false
				}
				return resolve(a) == resolve(b)
			}
			if got := stableBinaryPath(tt.invoked, tt.resolved, sameFile); got != tt.want {
				t.Errorf("stableBinaryPath(%q, %q) = %q, want %q", tt.invoked, tt.resolved, got, tt.want)
			}
		})
	}
}

func TestSameFile(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "ticktack")
	other := filepath.Join(dir, "other")
	link := filepath.Join(dir, "link")
	for _, p := range []string{binary, other} {
		if err := os.WriteFile(p, []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(binary, link); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{"identical path", binary, binary, true},
		{"symlink and target", link, binary, true},
		{"different files", binary, other, false},
		{"missing file", filepath.Join(dir, "gone"), binary, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sameFile(tt.a, tt.b); got != tt.want {
				t.Errorf("sameFile = %v, want %v", got, tt.want)
			}
		})
	}
}
