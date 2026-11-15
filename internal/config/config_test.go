package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadAppliesDefaults(t *testing.T) {
	cfg, err := Load(writeConfig(t, `youtrack_url = "https://example.youtrack.cloud"`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.YouTrackURL != "https://example.youtrack.cloud" {
		t.Errorf("YouTrackURL = %q", cfg.YouTrackURL)
	}
	if cfg.GapThreshold != 5*time.Minute {
		t.Errorf("GapThreshold = %v", cfg.GapThreshold)
	}
	if cfg.MinBlockDuration != DefaultMinBlockDuration {
		t.Errorf("MinBlockDuration = %v", cfg.MinBlockDuration)
	}
	if cfg.Rounding != DefaultRounding {
		t.Errorf("Rounding = %v", cfg.Rounding)
	}
	if cfg.DailyCap != DefaultDailyCap {
		t.Errorf("DailyCap = %v", cfg.DailyCap)
	}
	if cfg.TicketPattern == nil || cfg.TicketPattern.String() != DefaultTicketPattern {
		t.Errorf("TicketPattern = %v", cfg.TicketPattern)
	}
	if !cfg.TicketPattern.MatchString("TICKT-42") {
		t.Error("default pattern should match TICKT-42")
	}
	if cfg.Location == nil {
		t.Error("Location must default to a non-nil location")
	}
}

func TestLoadParsesValues(t *testing.T) {
	cfg, err := Load(writeConfig(t, `
youtrack_url = "https://yt.example.com"
gap_threshold = "10m"
min_block_duration = "15m"
rounding = "30m"
daily_cap = "8h"
ticket_pattern = "PRJ-\\d+"
repo_paths = ["/work/a", "/work/b"]
time_zone = "Europe/Zurich"

[[rules]]
repo = "/work/a"
ticket = "PRJ-1"
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GapThreshold != 10*time.Minute || cfg.DailyCap != 8*time.Hour {
		t.Errorf("durations not parsed: %+v", cfg)
	}
	if cfg.Location.String() != "Europe/Zurich" {
		t.Errorf("Location = %v", cfg.Location)
	}
	if len(cfg.RepoPaths) != 2 || len(cfg.Rules) != 1 || cfg.Rules[0].Ticket != "PRJ-1" {
		t.Errorf("lists not parsed: %+v", cfg)
	}
}

func TestLoadRejectsInvalidRegex(t *testing.T) {
	if _, err := Load(writeConfig(t, `ticket_pattern = "[unclosed"`)); err == nil {
		t.Fatal("expected error for invalid regex")
	}
}

func TestLoadRejectsUnknownKeys(t *testing.T) {
	if _, err := Load(writeConfig(t, `bogus_key = 1`)); err == nil {
		t.Fatal("expected error for unknown key")
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	cases := map[string]string{
		"bad duration":  `gap_threshold = "soon"`,
		"negative":      `gap_threshold = "-5m"`,
		"bad time zone": `time_zone = "Mars/Olympus"`,
		"bad url":       `youtrack_url = "not a url"`,
		"rule no repo":  "[[rules]]\nticket = \"A-1\"",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, content)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.toml")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestDefaultPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	got, err := DefaultPath()
	if err != nil || got != filepath.Join("/xdg", "ticktack", "config.toml") {
		t.Errorf("got %q, %v", got, err)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/home/u")
	got, err = DefaultPath()
	if err != nil || got != filepath.Join("/home/u", ".config", "ticktack", "config.toml") {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestTokenRoundTrip(t *testing.T) {
	keyring.MockInit()
	if _, err := GetToken(); !errors.Is(err, ErrTokenNotSet) {
		t.Fatalf("want ErrTokenNotSet, got %v", err)
	}
	if err := SetToken(""); err == nil {
		t.Fatal("empty token must be rejected")
	}
	if err := SetToken("perm:secret"); err != nil {
		t.Fatal(err)
	}
	got, err := GetToken()
	if err != nil || got != "perm:secret" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestTokenNeverWrittenToConfig(t *testing.T) {
	keyring.MockInit()
	const secret = "perm:super-secret-token"
	if err := SetToken(secret); err != nil {
		t.Fatal(err)
	}
	path := writeConfig(t, `youtrack_url = "https://yt.example.com"`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) {
		t.Error("token found in config file")
	}
	if _, err := Load(writeConfig(t, `token = "x"`)); err == nil {
		t.Error("a token key in the config must be rejected as unknown")
	}
	typ := reflect.TypeOf(cfg)
	for i := 0; i < typ.NumField(); i++ {
		if strings.Contains(strings.ToLower(typ.Field(i).Name), "token") {
			t.Errorf("Config exposes field %s", typ.Field(i).Name)
		}
	}
}
