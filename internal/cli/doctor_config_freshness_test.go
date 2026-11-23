package cli

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/JtheGunner/ticktack/internal/config"
)

func TestCheckConfigFreshness(t *testing.T) {
	tests := []struct {
		name       string
		config     *string
		wantStatus checkStatus
		wantDetail []string
		wantHint   string
	}{
		{"missing file is skipped", nil, statusSkip, nil, ""},
		{"current template is up to date", ptr(config.DefaultTOML("https://example.youtrack.cloud")), statusOK, []string{"up to date"}, ""},
		{"missing options are counted", ptr("youtrack_url = \"https://example.youtrack.cloud\"\n"), statusWarn, []string{"new options"}, "ticktack config update"},
		{"removed key is counted", ptr(config.DefaultTOML("https://example.youtrack.cloud") + "legacy_option = 1\n"), statusWarn, []string{"1 removed"}, "ticktack config update"},
		{"unparsable file is skipped", ptr("this is = = not toml\n"), statusSkip, nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newTestEnv(t, "", "")
			path, err := env.deps.Doctor.ConfigPath()
			if err != nil {
				t.Fatal(err)
			}
			if tt.config != nil {
				writeRepoPathsConfig(t, *tt.config)
			}
			var before []byte
			if tt.config != nil {
				before, _ = os.ReadFile(path)
			}

			res := checkConfigFreshness(context.Background(), env.deps.Doctor)
			if res.Name != msgCheckConfigFreshness || res.Status != tt.wantStatus {
				t.Fatalf("result = %+v, want status %v", res, tt.wantStatus)
			}
			for _, w := range tt.wantDetail {
				if !strings.Contains(res.Detail, w) {
					t.Errorf("detail %q lacks %q", res.Detail, w)
				}
			}
			if !strings.Contains(res.Hint, tt.wantHint) {
				t.Errorf("hint %q lacks %q", res.Hint, tt.wantHint)
			}
			if tt.config != nil {
				after, _ := os.ReadFile(path)
				if string(after) != string(before) {
					t.Error("the check modified the config file")
				}
				if _, err := os.Stat(path + ".bak"); err == nil {
					t.Error("the check wrote a backup")
				}
			}
		})
	}
}

func TestConfigFixNamesTheUpdateCommand(t *testing.T) {
	if !strings.Contains(msgConfigFix, "ticktack config update") {
		t.Errorf("msgConfigFix = %q", msgConfigFix)
	}
}

func ptr(s string) *string { return &s }
