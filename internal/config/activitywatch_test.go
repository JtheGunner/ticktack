package config

import (
	"strings"
	"testing"
)

func TestActivityWatchDefaultsToDisabled(t *testing.T) {
	cfg, err := Load(writeConfig(t, ``))
	if err != nil {
		t.Fatal(err)
	}
	aw := cfg.ActivityWatch
	if aw.Enabled || aw.URL != DefaultActivityWatchURL || aw.Hostname != "" {
		t.Fatalf("ActivityWatch = %+v", aw)
	}
}

func TestActivityWatchReadsTable(t *testing.T) {
	cfg, err := Load(writeConfig(t, "[activitywatch]\nenabled = true\nurl = \"http://127.0.0.1:5666\"\nhostname = \"my-mac\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	aw := cfg.ActivityWatch
	if !aw.Enabled || aw.URL != "http://127.0.0.1:5666" || aw.Hostname != "my-mac" {
		t.Fatalf("ActivityWatch = %+v", aw)
	}
}

func TestActivityWatchRejectsNonLoopbackURL(t *testing.T) {
	for _, raw := range []string{
		"http://example.com:5600",
		"http://localhost.evil.com",
		"http://localhost@evil.com",
		"http://user:secret-pw@localhost:5600",
		"ftp://localhost",
		"localhost:5600",
	} {
		for _, enabled := range []string{"true", "false"} {
			_, err := Load(writeConfig(t, "[activitywatch]\nenabled = "+enabled+"\nurl = \""+raw+"\"\n"))
			if err == nil {
				t.Errorf("url %q enabled=%s accepted", raw, enabled)
				continue
			}
			if strings.Contains(err.Error(), "secret-pw") || strings.Contains(err.Error(), "evil.com") {
				t.Errorf("error echoes the url: %v", err)
			}
			if !strings.Contains(err.Error(), "activitywatch.url") {
				t.Errorf("error does not name the key: %v", err)
			}
		}
	}
}

func TestActivityWatchRejectsUnknownKey(t *testing.T) {
	if _, err := Load(writeConfig(t, "[activitywatch]\nbogus = 1\n")); err == nil {
		t.Fatal("unknown key accepted")
	}
}

func TestDefaultTOMLDocumentsDisabledActivityWatch(t *testing.T) {
	text := DefaultTOML("")
	for _, want := range []string{"# [activitywatch]", "# enabled = false", `# url = "http://localhost:5600"`} {
		if !strings.Contains(text, want) {
			t.Errorf("DefaultTOML lacks %q", want)
		}
	}
}
