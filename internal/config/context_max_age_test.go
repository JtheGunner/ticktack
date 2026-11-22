package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadContextMaxAge(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    time.Duration
	}{
		{"absent uses the default", ``, 2 * time.Hour},
		{"custom value", `context_max_age = "45m"`, 45 * time.Minute},
		{"zero turns the limit off", `context_max_age = "0"`, 0},
		{"zero with a unit turns the limit off", `context_max_age = "0s"`, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Load(writeConfig(t, tc.content))
			if err != nil {
				t.Fatal(err)
			}
			if cfg.ContextMaxAge != tc.want {
				t.Errorf("ContextMaxAge = %v, want %v", cfg.ContextMaxAge, tc.want)
			}
		})
	}
}

func TestLoadRejectsInvalidContextMaxAge(t *testing.T) {
	tests := map[string]string{
		"not a duration": `context_max_age = "later"`,
		"negative":       `context_max_age = "-2h"`,
		"bare number":    `context_max_age = "120"`,
	}
	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Load(writeConfig(t, content))
			if err == nil || !strings.Contains(err.Error(), "context_max_age") {
				t.Fatalf("error = %v, want one naming context_max_age", err)
			}
		})
	}
}

func TestLoadOrDefaultUsesDefaultContextMaxAge(t *testing.T) {
	cfg, err := LoadOrDefault(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ContextMaxAge != DefaultContextMaxAge {
		t.Errorf("ContextMaxAge = %v, want %v", cfg.ContextMaxAge, DefaultContextMaxAge)
	}
}

func TestDefaultTOMLDocumentsContextMaxAge(t *testing.T) {
	if got := DefaultTOML(""); !strings.Contains(got, `# context_max_age = "2h0m0s"`) {
		t.Errorf("generated config lacks the context_max_age default:\n%s", got)
	}
}
