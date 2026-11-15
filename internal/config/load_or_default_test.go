package config

import (
	"path/filepath"
	"testing"
)

func TestLoadOrDefaultMissingFileYieldsDefaults(t *testing.T) {
	cfg, err := LoadOrDefault(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GapThreshold != DefaultGapThreshold || cfg.TicketPattern == nil || cfg.Location == nil {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
}

func TestLoadOrDefaultReadsExistingFile(t *testing.T) {
	cfg, err := LoadOrDefault(writeConfig(t, `rounding = "30m"`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Rounding.Minutes() != 30 {
		t.Fatalf("Rounding = %v", cfg.Rounding)
	}
}

func TestLoadOrDefaultInvalidFileStillFails(t *testing.T) {
	if _, err := LoadOrDefault(writeConfig(t, `rounding = "nonsense"`)); err == nil {
		t.Fatal("expected an error for an invalid file")
	}
}

func TestLoadOrDefaultDirectoryPathStillFails(t *testing.T) {
	if _, err := LoadOrDefault(t.TempDir()); err == nil {
		t.Fatal("expected an error when the path is not a readable file")
	}
}
