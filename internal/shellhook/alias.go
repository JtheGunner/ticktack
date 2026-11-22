package shellhook

import "fmt"

// AliasName is the short name the optional alias gives the ticktack binary.
const AliasName = "tt"

// AliasLine returns the line that defines the tt alias for the named shell
// (zsh, bash or fish). It is the same text `init` writes into the managed rc
// block, so a block can be searched for it.
func AliasLine(shell string) (string, error) {
	switch shell {
	case "zsh", "bash":
		return "alias " + AliasName + "=ticktack", nil
	case "fish":
		return "alias " + AliasName + " ticktack", nil
	}
	return "", fmt.Errorf("unsupported shell %q (want zsh, bash or fish)", shell)
}
