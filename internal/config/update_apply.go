package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const backupSuffix = ".bak"

// errConfigChanged is returned when the file no longer matches the text the
// plan was computed from.
var errConfigChanged = errors.New("config update: the config file changed while updating; run the update again")

// ApplyUpdate writes plan.Result over the config file at path. It first
// re-reads the file and refuses to continue when it differs from
// plan.Original. The original text is saved to path+".bak" (mode 0600,
// overwritten) before the new text replaces the file through a temp file in
// the same directory and a rename. On any error the config file is untouched.
// A plan without changes writes nothing.
func ApplyUpdate(path string, plan UpdatePlan) error {
	if !plan.Changed() {
		return nil
	}
	current, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("config update: read %s: %w", path, err)
	}
	if string(current) != plan.Original {
		return errConfigChanged
	}
	if err := writeFileAtomic(path+backupSuffix, []byte(plan.Original)); err != nil {
		return fmt.Errorf("config update: write backup: %w", err)
	}
	if err := writeFileAtomic(path, []byte(plan.Result)); err != nil {
		return fmt.Errorf("config update: write %s: %w", path, err)
	}
	return nil
}

// writeFileAtomic replaces path with data (mode 0600) via a temp file in the
// same directory, flushed to disk before the rename. The temp file is removed
// on failure.
func writeFileAtomic(path string, data []byte) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if err = tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("set temp file mode: %w", err)
	}
	if _, err = tmp.Write(data); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replace file: %w", err)
	}
	return nil
}
