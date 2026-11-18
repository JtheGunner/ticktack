package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// currentBinary returns the path of the running executable in the form that
// survives upgrades (see stableBinaryPath).
func currentBinary() (string, error) {
	invoked, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(invoked)
	if err != nil {
		return "", fmt.Errorf("resolve executable symlinks: %w", err)
	}
	return stableBinaryPath(invoked, resolved, sameFile), nil
}

// stableBinaryPath picks the path of the running binary that keeps working
// after an upgrade. invoked is the path the binary was started through,
// resolved the same path with every symlink followed.
//
// Homebrew installs each version into its own Cellar directory, so a binary
// there maps to the formula's opt link, provided sameFile confirms the link
// currently leads to this binary. Otherwise the invoked path wins over the
// resolved one: a symlink the user started the binary through is more likely
// to follow an upgrade than its target.
func stableBinaryPath(invoked, resolved string, sameFile func(a, b string) bool) string {
	for _, candidate := range []string{invoked, resolved} {
		if link, ok := homebrewOptLink(candidate); ok && sameFile(link, resolved) {
			return link
		}
	}
	return invoked
}

// homebrewOptLink maps <prefix>/Cellar/<name>/<version>/bin/<file> to
// <prefix>/opt/<name>/bin/<file>. It reports false for any other path.
func homebrewOptLink(path string) (string, bool) {
	if !filepath.IsAbs(path) {
		return "", false
	}
	sep := string(filepath.Separator)
	parts := strings.Split(filepath.Clean(path), sep)
	// The last five elements are Cellar, name, version, bin and the file.
	n := len(parts)
	if n < 5 || parts[n-5] != "Cellar" || parts[n-2] != "bin" {
		return "", false
	}
	name, file := parts[n-4], parts[n-1]
	prefix := strings.Join(parts[:n-5], sep)
	return filepath.Join(prefix+sep, "opt", name, "bin", file), true
}

// sameFile reports whether both paths exist and lead to the same file once
// symlinks are followed.
func sameFile(a, b string) bool {
	infoA, err := os.Stat(a)
	if err != nil {
		return false
	}
	infoB, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(infoA, infoB)
}
