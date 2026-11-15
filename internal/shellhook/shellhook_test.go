package shellhook

import (
	"os/exec"
	"strings"
	"testing"
)

func TestScriptContent(t *testing.T) {
	tests := []struct {
		shell      string
		background string
	}{
		{"zsh", "( ticktack hook --cwd \"$PWD\" >/dev/null 2>&1 & )"},
		{"bash", "( ticktack hook --cwd \"$PWD\" >/dev/null 2>&1 & )"},
		{"fish", "ticktack hook --cwd \"$PWD\" >/dev/null 2>&1 &"},
	}
	for _, tt := range tests {
		t.Run(tt.shell, func(t *testing.T) {
			s, err := Script(tt.shell)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{
				"command -v ticktack >/dev/null 2>&1",
				"TICKTACK_HOOK_DISABLED",
				tt.background,
				"_ticktack_hook_",
			} {
				if !strings.Contains(s, want) {
					t.Errorf("script missing %q", want)
				}
			}
		})
	}
}

func TestScriptHookRegistration(t *testing.T) {
	for shell, want := range map[string]string{
		"zsh":  "add-zsh-hook precmd",
		"bash": "PROMPT_COMMAND=",
		"fish": "--on-event fish_prompt",
	} {
		s, err := Script(shell)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(s, want) {
			t.Errorf("%s: missing %q", shell, want)
		}
	}
}

func TestScriptUnsupportedShell(t *testing.T) {
	if _, err := Script("tcsh"); err == nil {
		t.Fatal("expected error for tcsh")
	}
}

func TestScriptSyntax(t *testing.T) {
	for shell, args := range map[string][]string{
		"zsh":  {"-n"},
		"bash": {"-n"},
		"fish": {"--no-execute"},
	} {
		t.Run(shell, func(t *testing.T) {
			bin, err := exec.LookPath(shell)
			if err != nil {
				t.Skipf("%s not installed", shell)
			}
			s, err := Script(shell)
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(bin, args...)
			cmd.Stdin = strings.NewReader(s)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("syntax check failed: %v\n%s", err, out)
			}
		})
	}
}
