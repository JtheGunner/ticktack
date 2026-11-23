package cli

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JtheGunner/ticktack/internal/config"
)

func TestConfigUpdate(t *testing.T) {
	template := config.DefaultTOML("")
	missingOption := cutOptionBlock(t, template, "retention_days")
	withLegacy := template + "\nlegacy_option = \"" + strings.Repeat("x", 120) + "\"\n"
	hostile := template + "\nlegacy_option = \"a\u202ered\"\n"
	invalid := template + "\nlegacy_option = 1\nretention_days = \"not a number\"\n"

	tests := []struct {
		name        string
		file        *string // nil: no config file
		opts        configUpdateOptions
		input       string
		wantErr     string
		wantOut     []string
		notWantOut  []string
		wantWritten bool
		wantBackup  bool
	}{
		{name: "up to date", file: &template, wantOut: []string{"Config is up to date: "}, notWantOut: []string{"Apply"}},
		{name: "no config file", wantErr: "no config at "},
		{name: "added only with yes", file: &missingOption, opts: configUpdateOptions{Yes: true},
			wantOut: []string{"+ retention_days", "1 to add, 0 to remove.", "Updated ", ".bak)"}, wantWritten: true, wantBackup: true},
		{name: "removed only with yes", file: &withLegacy, opts: configUpdateOptions{Yes: true},
			wantOut: []string{"- legacy_option (was: ", "0 to add, 1 to remove.", "Updated "}, wantWritten: true, wantBackup: true},
		{name: "dry run writes nothing", file: &missingOption, opts: configUpdateOptions{DryRun: true},
			wantOut: []string{"+ retention_days"}, notWantOut: []string{"Apply", "Updated"}},
		{name: "answer n writes nothing", file: &missingOption, input: "n\n",
			wantOut: []string{"Apply these changes? [Y/n] ", "Nothing changed."}, notWantOut: []string{"Updated"}},
		{name: "end of input counts as no", file: &missingOption, input: "",
			wantOut: []string{"Apply these changes? [Y/n] ", "Nothing changed."}, notWantOut: []string{"Updated"}},
		{name: "empty answer counts as yes", file: &missingOption, input: "\n",
			wantOut: []string{"Updated "}, wantWritten: true, wantBackup: true},
		{name: "removed key Load rejects", file: &invalid, opts: configUpdateOptions{Yes: true},
			wantErr: "cannot update "},
		{name: "removed value truncated", file: &withLegacy, opts: configUpdateOptions{DryRun: true},
			wantOut: []string{strings.Repeat("x", 79)}, notWantOut: []string{strings.Repeat("x", 81)}},
		{name: "removed value sanitized", file: &hostile, opts: configUpdateOptions{DryRun: true},
			wantOut: []string{"- legacy_option (was: "}, notWantOut: []string{"\u202e"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			original := ""
			if tt.file != nil {
				original = *tt.file
				if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			deps := configUpdateDeps{ConfigPath: func() (string, error) { return path, nil }}
			var out strings.Builder
			err := runConfigUpdate(context.Background(), deps, tt.opts, bufio.NewReader(strings.NewReader(tt.input)), &out)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for _, want := range tt.wantOut {
				if !strings.Contains(out.String(), want) {
					t.Errorf("output missing %q:\n%s", want, out.String())
				}
			}
			for _, not := range tt.notWantOut {
				if strings.Contains(out.String(), not) {
					t.Errorf("output contains %q:\n%s", not, out.String())
				}
			}
			if tt.file == nil {
				return
			}
			after, _ := os.ReadFile(path)
			if written := string(after) != original; written != tt.wantWritten {
				t.Errorf("file written = %v, want %v", written, tt.wantWritten)
			}
			_, statErr := os.Stat(path + ".bak")
			if hasBackup := statErr == nil; hasBackup != tt.wantBackup {
				t.Errorf("backup present = %v, want %v", hasBackup, tt.wantBackup)
			}
		})
	}
}

func TestConfigUpdateCommandUsesConfigPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	root := NewRootCommand()
	var out strings.Builder
	root.SetOut(&out)
	root.SetArgs([]string{"config", "update"})
	err := root.Execute()
	want := filepath.Join(dir, "ticktack", "config.toml")
	if err == nil || !strings.Contains(err.Error(), "no config at "+want) {
		t.Fatalf("error = %v, want no config at %s", err, want)
	}
}

// cutOptionBlock removes the commented template block of one option.
func cutOptionBlock(t *testing.T, template, key string) string {
	t.Helper()
	lines := strings.Split(template, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "# "+key+" =") {
			start := i
			for start > 0 && strings.HasPrefix(lines[start-1], "#") && !strings.HasPrefix(lines[start-1], "# [") {
				start--
			}
			return strings.Join(append(append([]string{}, lines[:start]...), lines[i+1:]...), "\n")
		}
	}
	t.Fatalf("option %q not found in template", key)
	return ""
}
