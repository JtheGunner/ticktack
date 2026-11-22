// Package config loads and validates the ticktack configuration file. The
// YouTrack token is not part of the configuration; see package token.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
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
	DefaultGapThreshold = 5 * time.Minute
	// DefaultIdleThreshold is the idle time from which a presence sample no
	// longer counts as activity.
	DefaultIdleThreshold    = 3 * time.Minute
	DefaultMinBlockDuration = 10 * time.Minute
	// DefaultContextMaxAge is how long context is carried at its own tier
	// after it was last confirmed.
	DefaultContextMaxAge = 2 * time.Hour
	DefaultRounding      = 15 * time.Minute
	DefaultDailyCap      = 10 * time.Hour
	DefaultTicketPattern = `[A-Z][A-Z0-9]+-\d+`
	// DefaultActivityWatchURL is where a local ActivityWatch server listens.
	DefaultActivityWatchURL = "http://localhost:5600"
	// DefaultAutoTrustedDays is the number of consecutive clean reviewed days
	// that unlocks auto mode.
	DefaultAutoTrustedDays = 10
	// DefaultScanInterval is the agent's git scan interval; zero turns the
	// periodic scan off.
	DefaultScanInterval time.Duration = 0
	// MinScanInterval is the shortest accepted non-zero scan interval.
	MinScanInterval = 5 * time.Minute
)

// Scan configures the optional periodic git reflog scan run by the agent.
// Interval zero means off.
type Scan struct {
	Interval time.Duration
}

// Auto configures the optional auto mode. It is off by default and only
// books once TrustedDays consecutive reviewed days passed without correction.
type Auto struct {
	Enabled     bool
	TrustedDays int
}

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

// DefaultIssue names the standard task that receives time without a ticket.
// Project is the YouTrack project key of the global standard task; Summary is
// the title used for every standard task ticktack creates.
type DefaultIssue struct {
	Project string `toml:"project"`
	Summary string `toml:"summary"`
}

// Config is the validated, ready-to-use configuration.
type Config struct {
	YouTrackURL      string
	GapThreshold     time.Duration
	IdleThreshold    time.Duration
	MinBlockDuration time.Duration
	// ContextMaxAge is how long context is carried at its own tier after it
	// was last confirmed; older context is only a suggestion. Zero turns the
	// limit off for automatic context. A manual pin does not depend on it: it
	// is only a suggestion from the day after it was set.
	ContextMaxAge time.Duration
	Rounding      time.Duration
	DailyCap      time.Duration
	// DailyCapDisabled turns the daily cap off: no warning, no acknowledgement,
	// and no block on sync or auto.
	DailyCapDisabled bool
	Rules            []Rule
	TicketPattern    *regexp.Regexp
	RepoPaths        []string
	// RepoRoots are folders whose immediate children that are Git
	// repositories are tracked; entries are validated like RepoPaths.
	RepoRoots []string
	// DefaultIssue is nil when no standard task is configured.
	DefaultIssue  *DefaultIssue
	Location      *time.Location
	ActivityWatch ActivityWatch
	TitleHint     TitleHint
	Auto          Auto
	Scan          Scan
	// AppClasses maps a bundle ID to the class of that foreground app. It is
	// empty by default; an app that is not listed is ClassWork.
	AppClasses map[string]AppClass
}

// TitleHint configures the optional window-title collector. It is off unless
// Enabled is set. Projects maps an IDE project name to the absolute path of
// its repository. Titles themselves are never stored.
type TitleHint struct {
	Enabled  bool
	Projects map[string]string
}

type titleHintFile struct {
	Enabled  bool              `toml:"enabled"`
	Projects map[string]string `toml:"projects"`
}

// fileConfig mirrors the TOML file; durations and patterns stay strings until validated.
type fileConfig struct {
	YouTrackURL      string   `toml:"youtrack_url"`
	GapThreshold     string   `toml:"gap_threshold"`
	IdleThreshold    string   `toml:"idle_threshold"`
	MinBlockDuration string   `toml:"min_block_duration"`
	ContextMaxAge    string   `toml:"context_max_age"`
	Rounding         string   `toml:"rounding"`
	DailyCap         string   `toml:"daily_cap"`
	DailyCapEnabled  *bool    `toml:"daily_cap_enabled"`
	Rules            []Rule   `toml:"rules"`
	TicketPattern    string   `toml:"ticket_pattern"`
	RepoPaths        []string `toml:"repo_paths"`
	RepoRoots        []string `toml:"repo_roots"`
	TimeZone         string   `toml:"time_zone"`

	DefaultIssue *DefaultIssue `toml:"default_issue"`

	ActivityWatch struct {
		Enabled  bool   `toml:"enabled"`
		URL      string `toml:"url"`
		Hostname string `toml:"hostname"`
	} `toml:"activitywatch"`

	TitleHint titleHintFile `toml:"titlehint"`

	AppClasses map[string]string `toml:"app_classes"`

	Scan struct {
		Interval string `toml:"interval"`
	} `toml:"scan"`

	Auto struct {
		Enabled     bool `toml:"enabled"`
		TrustedDays *int `toml:"trusted_days"`
	} `toml:"auto"`
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
	cfg := Config{YouTrackURL: raw.YouTrackURL, RepoPaths: raw.RepoPaths}

	if err := CheckYouTrackURL(raw.YouTrackURL); err != nil {
		return Config{}, err
	}

	// zeroFix is the hint appended when the value is zero. A zero duration
	// is rejected because it would silently fall back to the default.
	durations := []struct {
		key      string
		value    string
		fallback time.Duration
		target   *time.Duration
		zeroFix  string
	}{
		{"gap_threshold", raw.GapThreshold, DefaultGapThreshold, &cfg.GapThreshold, msgZeroFixRemove},
		{"idle_threshold", raw.IdleThreshold, DefaultIdleThreshold, &cfg.IdleThreshold, msgZeroFixRemove},
		{"min_block_duration", raw.MinBlockDuration, DefaultMinBlockDuration, &cfg.MinBlockDuration, msgZeroFixRemove},
		{"rounding", raw.Rounding, DefaultRounding, &cfg.Rounding, msgZeroFixRounding},
		{"daily_cap", raw.DailyCap, DefaultDailyCap, &cfg.DailyCap, msgZeroFixDailyCap},
	}
	for _, d := range durations {
		parsed, err := parseDuration(d.key, d.value, d.fallback)
		if err != nil {
			return Config{}, err
		}
		if parsed == 0 {
			return Config{}, fmt.Errorf(msgZeroDuration, d.key, d.zeroFix)
		}
		*d.target = parsed
	}
	// Unlike the durations above, zero is a valid value here: it turns the
	// age limit of automatic context off.
	contextMaxAge, err := parseDuration("context_max_age", raw.ContextMaxAge, DefaultContextMaxAge)
	if err != nil {
		return Config{}, err
	}
	cfg.ContextMaxAge = contextMaxAge
	if cfg.Rounding%time.Minute != 0 {
		return Config{}, fmt.Errorf(msgRoundingNotMinutes, raw.Rounding)
	}

	cfg.DailyCapDisabled = raw.DailyCapEnabled != nil && !*raw.DailyCapEnabled

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

	rules, err := buildRules(raw.Rules, pattern)
	if err != nil {
		return Config{}, err
	}
	cfg.Rules = rules
	aw, err := buildActivityWatch(raw)
	if err != nil {
		return Config{}, err
	}
	cfg.ActivityWatch = aw
	hint, err := buildTitleHint(raw.TitleHint)
	if err != nil {
		return Config{}, err
	}
	cfg.TitleHint = hint
	for i, p := range raw.RepoPaths {
		if !validRepoPathSyntax(p) {
			return Config{}, fmt.Errorf("repo_paths[%d] must be an absolute path or start with \"~/\"", i)
		}
	}
	for i, p := range raw.RepoRoots {
		if !validRepoPathSyntax(p) {
			return Config{}, fmt.Errorf("repo_roots[%d] must be an absolute path or start with \"~/\"", i)
		}
	}
	cfg.RepoRoots = raw.RepoRoots
	defaultIssue, err := buildDefaultIssue(raw.DefaultIssue)
	if err != nil {
		return Config{}, err
	}
	cfg.DefaultIssue = defaultIssue
	scan, err := buildScan(raw)
	if err != nil {
		return Config{}, err
	}
	cfg.Scan = scan
	appClasses, err := buildAppClasses(raw.AppClasses)
	if err != nil {
		return Config{}, err
	}
	cfg.AppClasses = appClasses
	cfg.Auto = Auto{Enabled: raw.Auto.Enabled, TrustedDays: DefaultAutoTrustedDays}
	if days := raw.Auto.TrustedDays; days != nil {
		if *days < 1 {
			return Config{}, errors.New("auto.trusted_days must be at least 1")
		}
		cfg.Auto.TrustedDays = *days
	}
	return cfg, nil
}

// buildDefaultIssue validates the [default_issue] table. An absent table turns
// the feature off; a present one needs both keys.
func buildDefaultIssue(raw *DefaultIssue) (*DefaultIssue, error) {
	if raw == nil {
		return nil, nil
	}
	project := strings.ToUpper(strings.TrimSpace(raw.Project))
	if project == "" {
		return nil, errors.New("default_issue.project is required")
	}
	summary := strings.TrimSpace(raw.Summary)
	if summary == "" {
		return nil, errors.New("default_issue.summary is required")
	}
	return &DefaultIssue{Project: project, Summary: summary}, nil
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

// buildTitleHint validates the project mapping and normalises the repository
// paths, so they compare equal to the paths git reports. Errors name the
// project key only, never the configured value.
func buildTitleHint(raw titleHintFile) (TitleHint, error) {
	hint := TitleHint{Enabled: raw.Enabled}
	if len(raw.Projects) == 0 {
		return hint, nil
	}
	hint.Projects = make(map[string]string, len(raw.Projects))
	for name, repo := range raw.Projects {
		if strings.TrimSpace(name) == "" {
			return TitleHint{}, errors.New("titlehint.projects: project names must not be empty")
		}
		if !filepath.IsAbs(repo) {
			return TitleHint{}, fmt.Errorf("titlehint.projects.%q: repo path must be absolute", name)
		}
		hint.Projects[name] = resolvePath(repo)
	}
	return hint, nil
}

// buildScan validates the [scan] table. Zero turns the periodic scan off;
// any other value must be at least MinScanInterval.
func buildScan(raw fileConfig) (Scan, error) {
	interval, err := parseDuration("scan.interval", raw.Scan.Interval, DefaultScanInterval)
	if err != nil {
		return Scan{}, err
	}
	if interval != 0 && interval < MinScanInterval {
		return Scan{}, fmt.Errorf("scan.interval must be 0 (off) or at least %s", MinScanInterval)
	}
	return Scan{Interval: interval}, nil
}

// validRepoPathSyntax reports whether p is an absolute path or a path below
// the home directory written as "~" or "~/...".
func validRepoPathSyntax(p string) bool {
	return filepath.IsAbs(p) || p == "~" || strings.HasPrefix(p, "~/")
}

// ExpandRepoPath resolves one repo_paths entry: a leading "~" becomes home and
// the result is a clean absolute path. It does not touch the file system.
func ExpandRepoPath(p, home string) (string, error) {
	if !validRepoPathSyntax(p) {
		return "", errors.New("path must be absolute or start with \"~/\"")
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		if !filepath.IsAbs(home) {
			return "", errors.New("home directory is not available")
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return filepath.Clean(p), nil
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
