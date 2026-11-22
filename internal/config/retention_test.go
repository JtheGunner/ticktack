package config

import (
	"strings"
	"testing"
)

func TestRetentionDaysIsValidatedAtTheBoundary(t *testing.T) {
	tests := []struct {
		name    string
		toml    string
		want    int
		wantErr string
	}{
		{"absent means off", "", 0, ""},
		{"zero is off", "retention_days = 0\n", 0, ""},
		{"one day is allowed", "retention_days = 1\n", 1, ""},
		{"six days are allowed", "retention_days = 6\n", 6, ""},
		{"typical value", "retention_days = 90\n", 90, ""},
		{"upper bound", "retention_days = 3650\n", 3650, ""},
		{"negative is rejected", "retention_days = -1\n", 0, "retention_days must not be negative"},
		{"above the bound is rejected", "retention_days = 3651\n", 0, "retention_days must be at most 3650"},
		{"a string is rejected", "retention_days = \"30\"\n", 0, "RetentionDays"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Load(writeConfig(t, tc.toml))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.RetentionDays != tc.want {
				t.Errorf("RetentionDays = %d, want %d", cfg.RetentionDays, tc.want)
			}
		})
	}
}
