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
