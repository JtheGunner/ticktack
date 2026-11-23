package config

import (
	"strings"
	"testing"
	"time"
)

func TestScanIntervalDefaultsToFifteenMinutes(t *testing.T) {
	cfg, err := Load(writeConfig(t, DefaultTOML("")))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Scan.Interval != 15*time.Minute {
		t.Errorf("scan interval = %s, want 15m", cfg.Scan.Interval)
	}
}

func TestScanIntervalValidation(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    time.Duration
		wantErr string
	}{
		{"explicit zero", "[scan]\ninterval = \"0\"\n", 0, ""},
		{"zero with unit", "[scan]\ninterval = \"0s\"\n", 0, ""},
		{"minimum", "[scan]\ninterval = \"5m\"\n", 5 * time.Minute, ""},
		{"hourly", "[scan]\ninterval = \"1h\"\n", time.Hour, ""},
		{"too short", "[scan]\ninterval = \"1m\"\n", 0, "scan.interval"},
		{"negative", "[scan]\ninterval = \"-5m\"\n", 0, "scan.interval"},
		{"not a duration", "[scan]\ninterval = \"often\"\n", 0, "scan.interval"},
		{"unknown key", "[scan]\nevery = \"5m\"\n", 0, "scan.every"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load(writeConfig(t, tt.body))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to mention %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Scan.Interval != tt.want {
				t.Errorf("interval = %s, want %s", cfg.Scan.Interval, tt.want)
			}
		})
	}
}

func TestRepoPathsValidation(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{"absolute", "repo_paths = [\"/work/a\"]\n", false},
		{"tilde", "repo_paths = [\"~/work/a\"]\n", false},
		{"relative", "repo_paths = [\"work/a\"]\n", true},
		{"other user", "repo_paths = [\"~bob/a\"]\n", true},
		{"empty", "repo_paths = [\"\"]\n", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tt.body))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "repo_paths[0]") {
				t.Errorf("error should name the key: %v", err)
			}
		})
	}
}

func TestExpandRepoPath(t *testing.T) {
	tests := []struct {
		in, home, want string
		wantErr        bool
	}{
		{"/work/a", "/home/u", "/work/a", false},
		{"/work/a/../b/", "/home/u", "/work/b", false},
		{"~", "/home/u", "/home/u", false},
		{"~/code/x", "/home/u", "/home/u/code/x", false},
		{"~/code/x", "", "", true},
		{"rel/x", "/home/u", "", true},
		{"~bob/x", "/home/u", "", true},
	}
	for _, tt := range tests {
		got, err := ExpandRepoPath(tt.in, tt.home)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("ExpandRepoPath(%q, %q) = %q, %v; want %q (err %v)", tt.in, tt.home, got, err, tt.want, tt.wantErr)
		}
	}
}
