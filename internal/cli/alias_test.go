package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func foundTT(string) (string, error) { return "/usr/local/bin/tt", nil }

func TestRCDefinesTT(t *testing.T) {
	block := renderManagedBlock("ticktack hook\nalias tt=ticktack")
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{"empty", "", false},
		{"zsh alias", "alias tt=foo\n", true},
		{"alias with options", "alias -g tt=foo\n", true},
		{"fish alias", "alias tt 'foo --bar'\n", true},
		{"indented alias", "  alias tt=foo\n", true},
		{"bash function", "tt() { echo hi; }\n", true},
		{"bash function with space", "tt () { echo hi; }\n", true},
		{"function keyword", "function tt { echo hi; }\n", true},
		{"fish function", "function tt\n  echo hi\nend\n", true},
		{"commented out", "# alias tt=foo\n", false},
		{"other alias", "alias ttt=foo\nalias gst='git status'\n", false},
		{"function with longer name", "function ttl { :; }\n", false},
		{"only our own block", block, false},
		{"own block plus user alias", block + "alias tt=foo\n", true},
		{"user alias before own block", "alias tt=foo\n\n" + block, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rcDefinesTT(tt.content); got != tt.want {
				t.Errorf("rcDefinesTT = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestInitWritesTheAliasIntoTheManagedBlock(t *testing.T) {
	tests := []struct {
		shell     string
		rcRel     []string
		wantAlias string
	}{
		{"zsh", []string{".zshrc"}, "alias tt=ticktack\n"},
		{"bash", []string{".bashrc"}, "alias tt=ticktack\n"},
		{"fish", []string{".config", "fish", "config.fish"}, "alias tt ticktack\n"},
	}
	for _, tt := range tests {
		t.Run(tt.shell, func(t *testing.T) {
			env := newTestEnv(t, "", "")
			rc := filepath.Join(append([]string{env.home}, tt.rcRel...)...)
			opts := initOptions{Yes: true, Shell: tt.shell, SkipConfig: true, SkipToken: true, SkipAgent: true}
			var out bytes.Buffer
			if err := runInit(context.Background(), env.deps, opts, &out); err != nil {
				t.Fatal(err)
			}
			got := readFile(t, rc)
			if !strings.HasSuffix(got, tt.wantAlias+rcBlockEnd+"\n") {
				t.Errorf("alias is not the last line of the block:\n%s", got)
			}
			if !strings.Contains(out.String(), "alias tt set") {
				t.Errorf("output does not report the alias:\n%s", out.String())
			}
			// A second run changes nothing.
			if err := runInit(context.Background(), env.deps, opts, &out); err != nil {
				t.Fatal(err)
			}
			second := readFile(t, rc)
			if second != got {
				t.Errorf("second run changed the file:\n%s", second)
			}
			if strings.Count(second, tt.wantAlias) != 1 {
				t.Errorf("alias duplicated:\n%s", second)
			}
		})
	}
}

func TestInitAliasIsSkippedWithTheFlag(t *testing.T) {
	env := newTestEnv(t, "", "")
	rc := filepath.Join(env.home, ".zshrc")
	opts := initOptions{Yes: true, SkipConfig: true, SkipToken: true, SkipAgent: true, SkipAlias: true}
	var out bytes.Buffer
	if err := runInit(context.Background(), env.deps, opts, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readFile(t, rc), "alias tt") {
		t.Errorf("alias written despite --skip-alias:\n%s", readFile(t, rc))
	}
	if !strings.Contains(out.String(), "alias tt skipped") {
		t.Errorf("output does not say the alias was skipped:\n%s", out.String())
	}
}

func TestInitAliasIsNotSetWhenTTExists(t *testing.T) {
	tests := []struct {
		name     string
		lookPath func(string) (string, error)
		rcBefore string
		wantSay  string
	}{
		{"command on PATH", foundTT, "", "/usr/local/bin/tt"},
		{"alias in rc file", nil, "alias tt='tmux attach'\n", "startup file"},
		{"function in rc file", nil, "tt() { :; }\n", "startup file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newTestEnv(t, "", "")
			if tt.lookPath != nil {
				env.deps.Doctor.LookPath = tt.lookPath
			}
			rc := filepath.Join(env.home, ".zshrc")
			if err := os.WriteFile(rc, []byte(tt.rcBefore), 0o644); err != nil {
				t.Fatal(err)
			}
			opts := initOptions{Yes: true, SkipConfig: true, SkipToken: true, SkipAgent: true}
			var out bytes.Buffer
			if err := runInit(context.Background(), env.deps, opts, &out); err != nil {
				t.Fatal(err)
			}
			got := readFile(t, rc)
			block, _ := managedBlockOf(got)
			if strings.Contains(block, "alias tt") {
				t.Errorf("alias written although tt exists:\n%s", got)
			}
			if !strings.HasPrefix(got, tt.rcBefore) {
				t.Errorf("user text changed:\n%s", got)
			}
			if !strings.Contains(out.String(), "alias tt not set: tt already exists") || !strings.Contains(out.String(), tt.wantSay) {
				t.Errorf("init does not explain the conflict:\n%s", out.String())
			}
		})
	}
}

func TestInitUpgradesAnOlderBlockIdempotently(t *testing.T) {
	env := newTestEnv(t, "", "")
	rc := filepath.Join(env.home, ".zshrc")
	opts := initOptions{Yes: true, SkipConfig: true, SkipToken: true, SkipAgent: true}
	run := func(o initOptions) string {
		t.Helper()
		var out bytes.Buffer
		if err := runInit(context.Background(), env.deps, o, &out); err != nil {
			t.Fatal(err)
		}
		return readFile(t, rc)
	}

	// A block of an older version: hook only, no alias line.
	skipped := opts
	skipped.SkipAlias = true
	old := run(skipped)
	if strings.Contains(old, "alias tt") {
		t.Fatalf("setup: older block already has the alias:\n%s", old)
	}
	upgraded := run(opts)
	if strings.Count(upgraded, "alias tt=ticktack") != 1 || strings.Count(upgraded, rcBlockBegin) != 1 {
		t.Fatalf("block not upgraded in place:\n%s", upgraded)
	}
	if again := run(opts); again != upgraded {
		t.Errorf("rerun changed the upgraded file:\n%s", again)
	}
	// Skipping later removes the alias again.
	if back := run(skipped); back != old {
		t.Errorf("--skip-alias did not restore the hook-only block:\n%s", back)
	}
}

func TestUninstallRemovesTheAliasWithTheBlock(t *testing.T) {
	env, deps := newInstalledEnv(t, "")
	if !strings.Contains(readFile(t, env.rc), "alias tt=ticktack") {
		t.Fatalf("setup: init did not write the alias:\n%s", readFile(t, env.rc))
	}
	if err := runUninstall(context.Background(), deps, true); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, env.rc); got != userRCLine {
		t.Errorf("rc file = %q, want only the user's own line %q", got, userRCLine)
	}
}

func TestDoctorReportsTheAliasAsAHintOnly(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T, env *testEnv)
		lookPath func(string) (string, error)
		want     string
	}{
		{"no block", func(*testing.T, *testEnv) {}, nil, "not set (no managed block"},
		{"alias set", func(t *testing.T, env *testEnv) {
			writeZshrc(t, env, renderManagedBlock("ticktack hook\nalias tt=ticktack"))
		}, nil, "tt is set in the managed block"},
		{"not set", func(t *testing.T, env *testEnv) {
			writeZshrc(t, env, renderManagedBlock("ticktack hook"))
		}, nil, "tt is not set"},
		{"skipped for command", func(t *testing.T, env *testEnv) {
			writeZshrc(t, env, renderManagedBlock("ticktack hook"))
		}, foundTT, "skipped because tt already exists (command /usr/local/bin/tt)"},
		{"skipped for user alias", func(t *testing.T, env *testEnv) {
			writeZshrc(t, env, "alias tt=x\n"+renderManagedBlock("ticktack hook"))
		}, nil, "skipped because tt already exists"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newTestEnv(t, "", "")
			tt.setup(t, env)
			doc := env.deps.Doctor
			if tt.lookPath != nil {
				doc.LookPath = tt.lookPath
			}
			res := checkAlias(context.Background(), doc)
			if res.Status != statusInfo || !strings.Contains(res.Detail, tt.want) {
				t.Errorf("result = %+v, want info containing %q", res, tt.want)
			}
		})
	}
}

func TestDoctorAliasHintDoesNotChangeTheOutcome(t *testing.T) {
	env := newTestEnv(t, "", "")
	var out bytes.Buffer
	err := runDoctor(context.Background(), env.deps.Doctor, &out)
	if !errors.Is(err, ErrChecksFailed) {
		t.Fatalf("err = %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "alias tt:") {
		t.Errorf("no alias line:\n%s", text)
	}
	if strings.Contains(text, "x  alias tt") {
		t.Errorf("alias reported as failure:\n%s", text)
	}
	if !strings.Contains(text, "of 12 checks failed.") {
		t.Errorf("the hint must not count as a check:\n%s", text)
	}
}

func writeZshrc(t *testing.T, env *testEnv, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(env.home, ".zshrc"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
