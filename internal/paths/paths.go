// Package paths resolves the on-disk locations ticktack uses, following the
// XDG base directory conventions.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
)

const appDir = "ticktack"

func resolve(envKey string, homeRelative ...string) (string, error) {
	if base := os.Getenv(envKey); base != "" {
		return filepath.Join(base, appDir), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determine home directory: %w", err)
	}
	return filepath.Join(append([]string{home}, append(homeRelative, appDir)...)...), nil
}

// DataDir returns the directory for persistent data.
func DataDir() (string, error) { return resolve("XDG_DATA_HOME", ".local", "share") }

// StateDir returns the directory for state such as the pause flag and logs.
func StateDir() (string, error) { return resolve("XDG_STATE_HOME", ".local", "state") }

// DBPath returns the path of the SQLite event database.
func DBPath() (string, error) { return inDir(DataDir, "events.db") }

// PausePath returns the path of the pause flag. Tracking is paused while a
// file exists there.
func PausePath() (string, error) { return inDir(StateDir, "paused") }

// LogPath returns the path of the log file.
func LogPath() (string, error) { return inDir(StateDir, "ticktack.log") }

func inDir(dir func() (string, error), name string) (string, error) {
	d, err := dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, name), nil
}
