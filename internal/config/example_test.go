package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const exampleConfigPath = "../../examples/config.toml"

func TestExampleConfigLoadsWithDefaults(t *testing.T) {
	example, err := Load(exampleConfigPath)
	if err != nil {
		t.Fatalf("example config does not load: %v", err)
	}
	defaults, err := Load(writeConfig(t, DefaultTOML(example.YouTrackURL)))
	if err != nil {
		t.Fatal(err)
	}

	if example.YouTrackURL == "" {
		t.Error("example config must show a youtrack_url placeholder")
	}
	if example.GapThreshold != defaults.GapThreshold ||
		example.MinBlockDuration != defaults.MinBlockDuration ||
		example.Rounding != defaults.Rounding ||
		example.DailyCap != defaults.DailyCap {
		t.Errorf("durations differ from defaults: %+v vs %+v", example, defaults)
	}
	if example.TicketPattern.String() != DefaultTicketPattern {
		t.Errorf("ticket_pattern = %q, want %q", example.TicketPattern, DefaultTicketPattern)
	}
	if example.ActivityWatch != defaults.ActivityWatch {
		t.Errorf("activitywatch = %+v, want %+v", example.ActivityWatch, defaults.ActivityWatch)
	}
	if example.Scan != defaults.Scan {
		t.Errorf("scan = %+v, want %+v", example.Scan, defaults.Scan)
	}
	if example.Auto != defaults.Auto {
		t.Errorf("auto = %+v, want %+v", example.Auto, defaults.Auto)
	}
}

func TestExampleConfigKeepsOptionalFeaturesOff(t *testing.T) {
	cfg, err := Load(exampleConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TitleHint.Enabled || cfg.ActivityWatch.Enabled || cfg.Auto.Enabled {
		t.Errorf("optional features must be disabled: titlehint=%v activitywatch=%v auto=%v",
			cfg.TitleHint.Enabled, cfg.ActivityWatch.Enabled, cfg.Auto.Enabled)
	}
}

func TestExampleConfigDocumentsEveryKey(t *testing.T) {
	raw, err := os.ReadFile(filepath.FromSlash(exampleConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	data := string(raw)
	keys := []string{
		"youtrack_url", "gap_threshold", "min_block_duration", "rounding", "daily_cap",
		"ticket_pattern", "repo_paths", "time_zone", "[[rules]]", "repo =", "ticket =",
		"[titlehint]", "enabled", "[titlehint.projects]", "[activitywatch]", "url", "hostname",
		"[auto]", "trusted_days", "[scan]", "interval",
	}
	for _, key := range keys {
		if !strings.Contains(data, key) {
			t.Errorf("example config does not mention %q", key)
		}
	}
}
