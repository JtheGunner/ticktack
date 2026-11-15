// Package config loads and validates the ticktack configuration file. The
// YouTrack token is not part of the configuration; see package token.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/JtheGunner/ticktack/internal/loopback"
)

// Defaults applied when a key is absent from the configuration file.
const (
	DefaultGapThreshold     = 5 * time.Minute
	DefaultMinBlockDuration = 10 * time.Minute
	DefaultRounding         = 15 * time.Minute
	DefaultDailyCap         = 10 * time.Hour
	DefaultTicketPattern    = `[A-Z][A-Z0-9]+-\d+`
	// DefaultActivityWatchURL is where a local ActivityWatch server listens.
	DefaultActivityWatchURL = "http://localhost:5600"
)

// ActivityWatch configures the optional ActivityWatch importer. It is off by
// default and may only point at a loopback server.
type ActivityWatch struct {
	Enabled bool
	URL     string
	// Hostname restricts the import to buckets of this host; empty accepts
	// every host.
	Hostname string
}

// Rule maps a repository to a fixed YouTrack ticket.
type Rule struct {
	Repo   string `toml:"repo"`
	Ticket string `toml:"ticket"`
}

// Config is the validated, ready-to-use configuration.
type Config struct {
	YouTrackURL      string
	GapThreshold     time.Duration
	MinBlockDuration time.Duration
	Rounding         time.Duration
	DailyCap         time.Duration
	Rules            []Rule
	TicketPattern    *regexp.Regexp
	RepoPaths        []string
	Location         *time.Location
	ActivityWatch    ActivityWatch
}

// fileConfig mirrors the TOML file; durations and patterns stay strings until validated.
type fileConfig struct {
	YouTrackURL      string   `toml:"youtrack_url"`
	GapThreshold     string   `toml:"gap_threshold"`
	MinBlockDuration string   `toml:"min_block_duration"`
	Rounding         string   `toml:"rounding"`
	DailyCap         string   `toml:"daily_cap"`
	Rules            []Rule   `toml:"rules"`
	TicketPattern    string   `toml:"ticket_pattern"`
	RepoPaths        []string `toml:"repo_paths"`
	TimeZone         string   `toml:"time_zone"`

	ActivityWatch struct {
		Enabled  bool   `toml:"enabled"`
		URL      string `toml:"url"`
		Hostname string `toml:"hostname"`
	} `toml:"activitywatch"`
}

// DefaultPath returns the default config file location:
// $XDG_CONFIG_HOME/ticktack/config.toml or ~/.config/ticktack/config.toml.
func DefaultPath() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "ticktack", "config.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".config", "ticktack", "config.toml"), nil
}

// Load reads, strictly decodes and validates the configuration at path.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var raw fileConfig
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return Config{}, describeDecodeError(err)
	}
	return build(raw)
}

// LoadOrDefault behaves like Load but returns the defaults when no file
// exists at path. An unreadable or invalid file is still an error.
func LoadOrDefault(path string) (Config, error) {
	cfg, err := Load(path)
	if errors.Is(err, fs.ErrNotExist) {
		return build(fileConfig{})
	}
	return cfg, err
}

// describeDecodeError builds a parse error from key names, positions and the
// short message only. go-toml's human-readable context quotes the source
// line, which could echo a secret, so it is never used and the original error
// is not wrapped.
func describeDecodeError(err error) error {
	var strict *toml.StrictMissingError
	if errors.As(err, &strict) {
		var b strings.Builder
		b.WriteString("parse config: unknown keys:")
		for i := range strict.Errors {
			line, column := strict.Errors[i].Position()
			fmt.Fprintf(&b, "\n  %s (line %d, column %d)", strings.Join(strict.Errors[i].Key(), "."), line, column)
		}
		return errors.New(b.String())
	}
	var decodeErr *toml.DecodeError
	if errors.As(err, &decodeErr) {
		line, column := decodeErr.Position()
		return fmt.Errorf("parse config: line %d, column %d: %s", line, column, decodeErr.Error())
	}
	return fmt.Errorf("parse config: %w", err)
}

func build(raw fileConfig) (Config, error) {
	cfg := Config{YouTrackURL: raw.YouTrackURL, Rules: raw.Rules, RepoPaths: raw.RepoPaths}

	if raw.YouTrackURL != "" {
		u, err := url.Parse(raw.YouTrackURL)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return Config{}, fmt.Errorf("youtrack_url %q is not an absolute URL", raw.YouTrackURL)
		}
	}

	durations := []struct {
		key      string
		value    string
		fallback time.Duration
		target   *time.Duration
	}{
		{"gap_threshold", raw.GapThreshold, DefaultGapThreshold, &cfg.GapThreshold},
		{"min_block_duration", raw.MinBlockDuration, DefaultMinBlockDuration, &cfg.MinBlockDuration},
		{"rounding", raw.Rounding, DefaultRounding, &cfg.Rounding},
		{"daily_cap", raw.DailyCap, DefaultDailyCap, &cfg.DailyCap},
	}
	for _, d := range durations {
		parsed, err := parseDuration(d.key, d.value, d.fallback)
		if err != nil {
			return Config{}, err
		}
		*d.target = parsed
	}

	pattern := raw.TicketPattern
	if pattern == "" {
		pattern = DefaultTicketPattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return Config{}, fmt.Errorf("ticket_pattern: %w", err)
	}
	cfg.TicketPattern = re

	cfg.Location = time.Local
	if raw.TimeZone != "" {
		loc, err := time.LoadLocation(raw.TimeZone)
		if err != nil {
			return Config{}, fmt.Errorf("time_zone %q: %w", raw.TimeZone, err)
		}
		cfg.Location = loc
	}

	for i, rule := range raw.Rules {
		if rule.Repo == "" || rule.Ticket == "" {
			return Config{}, fmt.Errorf("rules[%d]: repo and ticket are both required", i)
		}
	}
	aw, err := buildActivityWatch(raw)
	if err != nil {
		return Config{}, err
	}
	cfg.ActivityWatch = aw
	return cfg, nil
}

// buildActivityWatch validates the [activitywatch] table. The URL is checked
// even while the importer is disabled so a bad value never lies in wait. The
// error never echoes the URL because it could carry credentials.
func buildActivityWatch(raw fileConfig) (ActivityWatch, error) {
	aw := ActivityWatch{
		Enabled:  raw.ActivityWatch.Enabled,
		URL:      raw.ActivityWatch.URL,
		Hostname: strings.TrimSpace(raw.ActivityWatch.Hostname),
	}
	if aw.URL == "" {
		aw.URL = DefaultActivityWatchURL
	}
	if _, err := loopback.CheckURL(aw.URL); err != nil {
		return ActivityWatch{}, fmt.Errorf("activitywatch.url %w", err)
	}
	return aw, nil
}

func parseDuration(key, value string, fallback time.Duration) (time.Duration, error) {
	if value == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("%s must not be negative, got %q", key, value)
	}
	return d, nil
}
