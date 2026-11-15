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
		return "", errors.New("found the begin marker but no end marker; fix the file by hand")
	}
	end := begin + endRel + len(rcBlockEnd)
	if end < len(content) && content[end] == '\n' {
		end++
	}
	return content[:begin] + block + content[end:], nil
}

// writeFileAtomic writes data to path through a temp file and a rename. An
// existing file keeps its mode; a symlink is followed so it stays a symlink.
func writeFileAtomic(path string, data []byte, defaultMode os.FileMode) error {
	resolved, err := filepath.EvalSymlinks(path)
	switch {
	case err == nil:
		path = resolved
	case isSymlink(path):
		return fmt.Errorf("%s is a dangling symlink; refusing to replace it", path)
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

// rcFileFor returns the rc file the hook of the given shell goes into.
func rcFileFor(home, goos, shell string) (string, error) {
	switch shell {
	case "zsh":
		return filepath.Join(home, ".zshrc"), nil
	case "bash":
		bashrc := filepath.Join(home, ".bashrc")
		profile := filepath.Join(home, ".bash_profile")
		if goos == "darwin" && fileExists(profile) && !fileExists(bashrc) {
			return profile, nil
		}
		return bashrc, nil
	case "fish":
		return filepath.Join(home, ".config", "fish", "config.fish"), nil
	}
	return "", fmt.Errorf("unsupported shell %q", shell)
}

func isSymlink(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
