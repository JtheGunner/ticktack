package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestHookFallsBackToDefaultPatternWhenConfigFails(t *testing.T) {
	sink := &fakeSink{}
	deps, logs := newTestDeps(sink, "/r\nfeat/ABC-12-x\n", nil)
	deps.Pattern = func() (*regexp.Regexp, error) { return nil, errors.New("bad config") }
	if err := runHook(context.Background(), deps, "/r"); err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != 2 || sink.events[0].Ticket != "ABC-12" {
		t.Fatalf("events = %+v, want a context event with ABC-12", sink.events)
	}
	if !strings.Contains(logs.String(), "ticket pattern") {
		t.Errorf("log = %q, want the pattern failure recorded", logs.String())
	}
}

func TestConfiguredTicketPattern(t *testing.T) {
	custom := `ticket_pattern = "PRJ_\\d+"`
	invalid := `ticket_pattern = "[x"`
	tests := []struct {
		name    string
		config  *string // nil: no config file
		branch  string
		want    string
		wantErr bool
	}{
		{name: "no config file uses the default", branch: "feat/ABC-1", want: "ABC-1"},
		{name: "custom pattern", config: &custom, branch: "feat/PRJ_4", want: "PRJ_4"},
		{name: "invalid config reports an error", config: &invalid, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", dir)
			if tt.config != nil {
				if err := os.MkdirAll(filepath.Join(dir, "ticktack"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "ticktack", "config.toml"), []byte(*tt.config), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			pattern, err := configuredTicketPattern()
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got := strings.ToUpper(pattern.FindString(tt.branch)); got != tt.want {
				t.Errorf("match = %q, want %q", got, tt.want)
			}
		})
	}
}

// BenchmarkHookPromptCost measures what each shell prompt pays on top of the
// event write: loading the config and detecting the branch with git.
func BenchmarkHookPromptCost(b *testing.B) {
	dir := b.TempDir()
	b.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "ticktack"), 0o700); err != nil {
		b.Fatal(err)
	}
	cfg := "ticket_pattern = \"[A-Z]+-\\\\d+\"\nrepo_paths = [\"~/code\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "ticktack", "config.toml"), []byte(cfg), 0o600); err != nil {
		b.Fatal(err)
	}
	repo := b.TempDir()
	if err := exec.Command("git", "-C", repo, "init", "-q", "-b", "main").Run(); err != nil {
		b.Skip("git unavailable")
	}
	b.Run("config", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := configuredTicketPattern(); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("git", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, _ = gitRepoInfo(context.Background(), repo)
		}
	})
}
