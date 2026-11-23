package config

import (
	"strings"
	"testing"
)

func TestAutoModeIsOffByDefault(t *testing.T) {
	cfg, err := Load(writeConfig(t, DefaultTOML("")))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auto.Enabled || cfg.Auto.TrustedDays != DefaultAutoTrustedDays || DefaultAutoTrustedDays != 10 {
		t.Errorf("auto mode must be off with 10 trusted days: %+v", cfg.Auto)
	}
}

func TestLoadParsesAutoSection(t *testing.T) {
	cfg, err := Load(writeConfig(t, "[auto]\nenabled = true\ntrusted_days = 3\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Auto.Enabled || cfg.Auto.TrustedDays != 3 {
		t.Errorf("unexpected auto config: %+v", cfg.Auto)
	}
}

func TestLoadRejectsInvalidTrustedDays(t *testing.T) {
	for _, v := range []string{"0", "-2"} {
		_, err := Load(writeConfig(t, "[auto]\ntrusted_days = "+v+"\n"))
		if err == nil || !strings.Contains(err.Error(), "auto.trusted_days") {
			t.Errorf("trusted_days = %s: err = %v", v, err)
		}
	}
}

func TestDefaultTOMLDocumentsAutoSection(t *testing.T) {
	text := DefaultTOML("")
	if !strings.Contains(text, "# [auto]") || !strings.Contains(text, "# trusted_days = 10") {
		t.Errorf("default config lacks the commented auto example:\n%s", text)
	}
}

func TestAutoModeDefaultsToGated(t *testing.T) {
	cfg, err := Load(writeConfig(t, "[auto]\nenabled = true\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auto.Mode != AutoModeGated || cfg.Auto.Autopilot() || cfg.Auto.AutopilotLookbackDays != 7 {
		t.Errorf("default auto config must be gated with 7 lookback days: %+v", cfg.Auto)
	}
}

func TestAutopilotNeedsEnabledAndTheModeTogether(t *testing.T) {
	tests := []struct {
		name string
		toml string
		want bool
	}{
		{"opt-in", "[auto]\nenabled = true\nmode = \"autopilot\"\n", true},
		{"mode without enabled", "[auto]\nmode = \"autopilot\"\n", false},
		{"enabled with explicit gated", "[auto]\nenabled = true\nmode = \"gated\"\n", false},
		{"enabled without mode", "[auto]\nenabled = true\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load(writeConfig(t, tt.toml))
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.Auto.Autopilot(); got != tt.want {
				t.Errorf("Autopilot() = %v, want %v (%+v)", got, tt.want, cfg.Auto)
			}
		})
	}
}

func TestLoadRejectsUnknownAutoMode(t *testing.T) {
	for _, v := range []string{"auto", "Autopilot", ""} {
		_, err := Load(writeConfig(t, "[auto]\nmode = \""+v+"\"\n"))
		if err == nil || !strings.Contains(err.Error(), "auto.mode") || !strings.Contains(err.Error(), "autopilot") {
			t.Errorf("mode = %q: err = %v", v, err)
		}
	}
}

func TestLoadChecksTheAutopilotLookbackBounds(t *testing.T) {
	tests := []struct {
		value   string
		want    int
		wantErr bool
	}{
		{"1", 1, false},
		{"31", 31, false},
		{"0", 0, true},
		{"32", 0, true},
		{"-1", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			cfg, err := Load(writeConfig(t, "[auto]\nautopilot_lookback_days = "+tt.value+"\n"))
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "auto.autopilot_lookback_days") {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err != nil || cfg.Auto.AutopilotLookbackDays != tt.want {
				t.Fatalf("lookback = %d, err = %v", cfg.Auto.AutopilotLookbackDays, err)
			}
		})
	}
}
