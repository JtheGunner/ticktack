// Package shellhook provides the prompt hooks that report the working
// directory to `ticktack hook`.
package shellhook

import (
	_ "embed"
	"fmt"
)

// Zsh is the hook script for zsh.
//
//go:embed zsh.sh
var Zsh string

// Bash is the hook script for bash.
//
//go:embed bash.sh
var Bash string

// Fish is the hook script for fish.
//
//go:embed fish.fish
var Fish string

// Script returns the hook script for the named shell (zsh, bash or fish).
func Script(shell string) (string, error) {
	switch shell {
	case "zsh":
		return Zsh, nil
	case "bash":
		return Bash, nil
	case "fish":
		return Fish, nil
	}
	return "", fmt.Errorf("unsupported shell %q (want zsh, bash or fish)", shell)
}
