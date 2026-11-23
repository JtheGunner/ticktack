package config

import "testing"

func TestDescriptionFromCommits(t *testing.T) {
	tests := []struct {
		name string
		toml string
		want bool
	}{
		{"absent is on", ``, true},
		{"true", `description_from_commits = true`, true},
		{"false", `description_from_commits = false`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Load(writeConfig(t, tc.toml))
			if err != nil {
				t.Fatal(err)
			}
			if cfg.DescriptionFromCommits != tc.want {
				t.Errorf("DescriptionFromCommits = %v, want %v", cfg.DescriptionFromCommits, tc.want)
			}
		})
	}
}

func TestDescriptionFromCommitsRejectsNonBoolean(t *testing.T) {
	for _, value := range []string{`"yes"`, `1`, `"false"`} {
		if _, err := Load(writeConfig(t, `description_from_commits = `+value)); err == nil {
			t.Errorf("value %s was accepted", value)
		}
	}
}

func TestMissingConfigFileKeepsDescriptionFromCommitsOn(t *testing.T) {
	cfg, err := LoadOrDefault(t.TempDir() + "/none.toml")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.DescriptionFromCommits {
		t.Error("DescriptionFromCommits is off without a config file")
	}
}
