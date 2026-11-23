package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// commentedSetting matches a commented-out table header or key/value line, as
// opposed to a line of prose.
var commentedSetting = regexp.MustCompile(`^# (\[\[?[a-z_.]+\]\]?|"?[a-z_-]+"? = .+)$`)

// tableHeader matches a table header, active or commented out.
var tableHeader = regexp.MustCompile(`^(# )?\[\[?[a-z_.]+\]\]?$`)

// activateCommentedSettings uncomments every commented-out setting of a
// configuration text and returns it with the number of activated lines.
func activateCommentedSettings(content string) (string, int) {
	lines := strings.Split(content, "\n")
	activated := 0
	for i, line := range lines {
		if commentedSetting.MatchString(line) {
			lines[i] = strings.TrimPrefix(line, "# ")
			activated++
		}
	}
	return strings.Join(lines, "\n"), activated
}

func configTemplates(t *testing.T) map[string]string {
	t.Helper()
	example, err := os.ReadFile(filepath.FromSlash(exampleConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		"example":             string(example),
		"default without url": DefaultTOML(""),
		"default with url":    DefaultTOML("https://example.youtrack.cloud"),
	}
}

func TestTemplatesLoadWithEveryCommentedSettingActivated(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for name, content := range configTemplates(t) {
		t.Run(name, func(t *testing.T) {
			active, activated := activateCommentedSettings(content)
			if activated == 0 {
				t.Fatal("no commented-out setting found")
			}
			if strings.Contains(active, "\n# [") {
				t.Fatalf("a commented-out table header was not activated:\n%s", active)
			}
			cfg, err := Load(writeConfig(t, active))
			if err != nil {
				t.Fatalf("does not load with every setting activated: %v", err)
			}
			if cfg.Location.String() != "Europe/Zurich" {
				t.Errorf("time_zone was not activated: %v", cfg.Location)
			}
			if len(cfg.Rules) != 1 || len(cfg.RepoPaths) != 1 || len(cfg.TitleHint.Projects) != 1 {
				t.Errorf("rules, repo_paths and titlehint.projects must be activated: %+v", cfg)
			}
		})
	}
}

func TestTemplatesPlaceTimeZoneBeforeFirstTable(t *testing.T) {
	for name, content := range configTemplates(t) {
		t.Run(name, func(t *testing.T) {
			timeZoneLine, firstTableLine := -1, -1
			for i, line := range strings.Split(content, "\n") {
				if strings.HasPrefix(strings.TrimPrefix(line, "# "), "time_zone = ") {
					timeZoneLine = i
				}
				if firstTableLine < 0 && tableHeader.MatchString(line) {
					firstTableLine = i
				}
			}
			if timeZoneLine < 0 || firstTableLine < 0 {
				t.Fatalf("time_zone line %d, first table line %d", timeZoneLine, firstTableLine)
			}
			if timeZoneLine > firstTableLine {
				t.Errorf("time_zone (line %d) must come before the first table header (line %d)", timeZoneLine+1, firstTableLine+1)
			}
		})
	}
}

func TestLoadRejectsZeroDurations(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{"zero rounding", `rounding = "0s"`, []string{"rounding", "1m"}},
		{"zero daily cap", `daily_cap = "0"`, []string{"daily_cap", "daily_cap_enabled = false"}},
		{"zero min block", `min_block_duration = "0s"`, []string{"min_block_duration", "greater than 0"}},
		{"zero gap", `gap_threshold = "0m"`, []string{"gap_threshold", "greater than 0"}},
		{"zero idle", `idle_threshold = "0s"`, []string{"idle_threshold", "greater than 0"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tc.content))
			if err == nil {
				t.Fatal("expected an error")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

func TestLoadRequiresRoundingInWholeMinutes(t *testing.T) {
	tests := []struct {
		value string
		ok    bool
	}{
		{"1m", true},
		{"15m", true},
		{"1h", true},
		{"90m", true},
		{"120s", true},
		{"90s", false},
		{"30s", false},
		{"15m30s", false},
		{"500ms", false},
	}
	for _, tc := range tests {
		t.Run(tc.value, func(t *testing.T) {
			_, err := Load(writeConfig(t, `rounding = "`+tc.value+`"`))
			if tc.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.ok {
				return
			}
			if err == nil || !strings.Contains(err.Error(), "rounding") || !strings.Contains(err.Error(), "whole minutes") {
				t.Fatalf("error must name rounding and whole minutes: %v", err)
			}
		})
	}
}

func TestLoadAcceptsZeroScanInterval(t *testing.T) {
	cfg, err := Load(writeConfig(t, "[scan]\ninterval = \"0\"\n"))
	if err != nil || cfg.Scan.Interval != 0 {
		t.Fatalf("scan.interval 0 must stay supported: %+v, %v", cfg.Scan, err)
	}
}

func TestLoadNormalisesRuleRepos(t *testing.T) {
	home := resolvedTempDir(t)
	t.Setenv("HOME", home)
	real := filepath.Join(home, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		repo string
		want string
	}{
		{"tilde is expanded", "~/real", real},
		{"home itself", "~", home},
		{"trailing slash", real + "/", real},
		{"dot segments", home + "/x/../real/.", real},
		{"symlink is resolved", link, real},
		{"symlink below tilde", "~/link/", real},
		{"missing path stays cleaned", home + "/missing//repo/", filepath.Join(home, "missing", "repo")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Load(writeConfig(t, "[[rules]]\nrepo = \""+tc.repo+"\"\nticket = \"ABC-1\"\n"))
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.Rules[0].Repo; got != tc.want {
				t.Errorf("repo = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLoadRejectsInvalidRules(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"relative repo", "[[rules]]\nrepo = \"work/app\"\nticket = \"ABC-1\"\n", "rules[0].repo"},
		{"tilde user repo", "[[rules]]\nrepo = \"~other/app\"\nticket = \"ABC-1\"\n", "rules[0].repo"},
		{"missing ticket", "[[rules]]\nrepo = \"/work/app\"\n", "rules[0]"},
		{"ticket does not match", "[[rules]]\nrepo = \"/work/app\"\nticket = \"abc\"\n", "rules[0].ticket"},
		{"ticket with surrounding text", "[[rules]]\nrepo = \"/work/app\"\nticket = \"see ABC-1\"\n", "rules[0].ticket"},
		{"ticket against custom pattern", "ticket_pattern = \"PRJ-\\\\d+\"\n[[rules]]\nrepo = \"/work/app\"\nticket = \"ABC-1\"\n", "rules[0].ticket"},
		{"second rule is named", "[[rules]]\nrepo = \"/a\"\nticket = \"ABC-1\"\n[[rules]]\nrepo = \"/b\"\nticket = \"nope\"\n", "rules[1].ticket"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tc.content))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error must name %s: %v", tc.want, err)
			}
		})
	}
}

func TestLoadRejectsTildeRuleWithoutHome(t *testing.T) {
	t.Setenv("HOME", "")
	_, err := Load(writeConfig(t, "[[rules]]\nrepo = \"~/app\"\nticket = \"ABC-1\"\n"))
	if err == nil || !strings.Contains(err.Error(), "rules[0].repo") {
		t.Fatalf("error must name rules[0].repo: %v", err)
	}
	if _, err := Load(writeConfig(t, "[[rules]]\nrepo = \"/work/app\"\nticket = \"ABC-1\"\n")); err != nil {
		t.Fatalf("an absolute rule repo needs no home directory: %v", err)
	}
}

func TestLoadNormalisesTitleHintRepos(t *testing.T) {
	dir := resolvedTempDir(t)
	real := filepath.Join(dir, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		repo string
		want string
	}{
		{"trailing slash", real + "/", real},
		{"symlink is resolved", link + "/", real},
		{"missing path stays cleaned", "/abs//path/to/repo/", "/abs/path/to/repo"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Load(writeConfig(t, "[titlehint.projects]\n\"proj\" = \""+tc.repo+"\"\n"))
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.TitleHint.Projects["proj"]; got != tc.want {
				t.Errorf("repo = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLoadYouTrackURLScheme(t *testing.T) {
	tests := []struct {
		url string
		ok  bool
	}{
		{"https://example.youtrack.cloud", true},
		{"https://yt.example.com/youtrack", true},
		{"HTTPS://yt.example.com", true},
		{"https://localhost:8443", true},
		{"http://localhost:8080", true},
		{"http://127.0.0.1:49152", true},
		{"http://127.5.6.7", true},
		{"http://[::1]:8080", true},
		{"http://yt.example.com", false},
		{"http://192.168.1.2:8080", false},
		{"http://localhost.evil.example", false},
		{"http://localhost@evil.example", false},
		{"ftp://yt.example.com", false},
		{"file:///etc/passwd", false},
		{"ws://localhost", false},
		{"yt.example.com", false},
	}
	for _, tc := range tests {
		t.Run(tc.url, func(t *testing.T) {
			_, err := Load(writeConfig(t, `youtrack_url = "`+tc.url+`"`))
			if tc.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.ok && (err == nil || !strings.Contains(err.Error(), "youtrack_url")) {
				t.Fatalf("error must name youtrack_url: %v", err)
			}
		})
	}
}

func TestYouTrackURLSchemeErrorNamesFixWithoutEchoingURL(t *testing.T) {
	_, err := Load(writeConfig(t, `youtrack_url = "http://user:sekrit@yt.example.com"`))
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("error must name the https fix: %v", err)
	}
	if strings.Contains(err.Error(), "sekrit") {
		t.Errorf("error echoes the URL: %v", err)
	}
}

// resolvedTempDir returns a temporary directory whose path contains no
// symlinks, so expected paths can be compared literally.
func resolvedTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
