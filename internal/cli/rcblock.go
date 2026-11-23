package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	rcBlockBegin = "# >>> ticktack >>>"
	rcBlockEnd   = "# <<< ticktack <<<"
)

// hasManagedBlock reports whether content holds a complete managed block.
func hasManagedBlock(content string) bool {
	begin := strings.Index(content, rcBlockBegin)
	if begin < 0 {
		return false
	}
	return strings.Contains(content[begin:], rcBlockEnd)
}

// renderManagedBlock wraps script in the begin and end markers.
func renderManagedBlock(script string) string {
	return rcBlockBegin + "\n" + strings.TrimRight(script, "\n") + "\n" + rcBlockEnd + "\n"
}

// upsertManagedBlock replaces the managed block in content, or appends it when
// absent. A begin marker without end marker is an error so the file is never
// corrupted.
func upsertManagedBlock(content, block string) (string, error) {
	begin := strings.Index(content, rcBlockBegin)
	if begin < 0 {
		if content == "" {
			return block, nil
		}
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		return content + "\n" + block, nil
	}
	endRel := strings.Index(content[begin:], rcBlockEnd)
	if endRel < 0 {
		return "", errors.New(msgRCBlockNoEndMarker)
	}
	end := begin + endRel + len(rcBlockEnd)
	if end < len(content) && content[end] == '\n' {
		end++
	}
	return content[:begin] + block + content[end:], nil
}

// removeManagedBlock cuts the managed block out of content and reports
// whether there was one. It reverses upsertManagedBlock, including the blank
// line that separates an appended block from the text before it. A begin
// marker without end marker is an error so the file is never corrupted.
func removeManagedBlock(content string) (string, bool, error) {
	begin := strings.Index(content, rcBlockBegin)
	if begin < 0 {
		return content, false, nil
	}
	endRel := strings.Index(content[begin:], rcBlockEnd)
	if endRel < 0 {
		return "", false, errors.New(msgRCBlockNoEndMarker)
	}
	end := begin + endRel + len(rcBlockEnd)
	if end < len(content) && content[end] == '\n' {
		end++
	}
	before, after := content[:begin], content[end:]
	if after == "" && strings.HasSuffix(before, "\n\n") {
		before = strings.TrimSuffix(before, "\n")
	}
	return before + after, true, nil
}

// zshDir returns the directory zsh reads its startup files from: ZDOTDIR when
// it is an absolute path, the home directory otherwise.
func zshDir(home, zdotdir string) string {
	if filepath.IsAbs(zdotdir) {
		return zdotdir
	}
	return home
}

// bashLoginFiles are the startup files a bash login shell reads, in the order
// bash tries them: it uses the first one that exists and ignores the rest.
var bashLoginFiles = []string{".bash_profile", ".bash_login", ".profile"}

// rcFileCandidates lists every shell startup file `init` may have written the
// managed block to, whatever shell, platform and ZDOTDIR it ran with.
func rcFileCandidates(home, zdotdir string) []string {
	candidates := []string{filepath.Join(zshDir(home, zdotdir), ".zshrc")}
	candidates = append(candidates, filepath.Join(home, ".zshrc"), filepath.Join(home, ".bashrc"))
	for _, name := range bashLoginFiles {
		candidates = append(candidates, filepath.Join(home, name))
	}
	candidates = append(candidates, filepath.Join(home, ".config", "fish", "config.fish"))
	return uniqueStrings(candidates)
}

// uniqueStrings drops repeated entries and keeps the order of the first
// occurrences.
func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	unique := values[:0:0]
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			unique = append(unique, v)
		}
	}
	return unique
}

// writeFileAtomic writes data to path through a temp file and a rename. An
// existing file keeps its mode; a symlink is followed so it stays a symlink.
func writeFileAtomic(path string, data []byte, defaultMode os.FileMode) error {
	resolved, err := filepath.EvalSymlinks(path)
	switch {
	case err == nil:
		path = resolved
	case isSymlink(path):
		return fmt.Errorf(msgRCDanglingSymlink, path)
	}
	mode := defaultMode
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ticktack-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("set mode: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace file: %w", err)
	}
	return nil
}

// rcFileFor returns the rc file the hook of the given shell goes into. zdotdir
// is the value of $ZDOTDIR. Terminal.app and iTerm2 start bash as a login
// shell, which does not read .bashrc, so on macOS the hook goes into the first
// login file that exists (.bash_profile when none does).
func rcFileFor(home, goos, shell, zdotdir string) (string, error) {
	switch shell {
	case "zsh":
		return filepath.Join(zshDir(home, zdotdir), ".zshrc"), nil
	case "bash":
		if goos != "darwin" {
			return filepath.Join(home, ".bashrc"), nil
		}
		for _, name := range bashLoginFiles {
			if path := filepath.Join(home, name); fileExists(path) {
				return path, nil
			}
		}
		return filepath.Join(home, bashLoginFiles[0]), nil
	case "fish":
		return filepath.Join(home, ".config", "fish", "config.fish"), nil
	}
	return "", fmt.Errorf(msgRCUnsupportedShell, shell)
}

func isSymlink(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
