package config

import (
	"os"
	"strings"
	"testing"
)

func TestAppClassesRejectsAmbiguousKeys(t *testing.T) {
	tests := []struct {
		name string
		toml string
		want string
	}{
		{
			name: "trim collision with different classes",
			toml: "[app_classes]\n\"com.x\" = \"work\"\n\" com.x\" = \"personal\"\n",
			want: `" com.x" and "com.x" are the same bundle ID after trimming`,
		},
		{
			name: "trim collision with the same class",
			toml: "[app_classes]\n\"com.x\" = \"neutral\"\n\"com.x \" = \"neutral\"\n",
			want: `"com.x" and "com.x " are the same bundle ID after trimming`,
		},
		{
			name: "case collision",
			toml: "[app_classes]\n\"com.apple.Music\" = \"personal\"\n\"com.apple.music\" = \"neutral\"\n",
			want: `"com.apple.Music" and "com.apple.music" differ only in case`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The decoded map has random iteration order; the message must not
			// depend on it.
			for i := 0; i < 20; i++ {
				_, err := Load(writeConfig(t, tt.toml))
				if err == nil || !strings.Contains(err.Error(), tt.want) {
					t.Fatalf("error = %v, want containing %q", err, tt.want)
				}
			}
		})
	}
}

func TestAppClassesMatchingIsCaseSensitive(t *testing.T) {
	cfg, err := Load(writeConfig(t, "[app_classes]\n\"com.apple.Music\" = \"personal\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AppClasses["com.apple.Music"] != ClassPersonal || cfg.AppClasses["com.apple.music"] != "" {
		t.Errorf("AppClasses = %v, want the exact key only", cfg.AppClasses)
	}
}

// uncommentedAppClasses returns the [app_classes] example block of a config
// text with its comment markers removed.
func uncommentedAppClasses(t *testing.T, text string) string {
	t.Helper()
	var out []string
	in := false
	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(line, "# [app_classes]"):
			in = true
			out = append(out, "[app_classes]")
		case in && strings.HasPrefix(line, "# \""):
			out = append(out, strings.TrimPrefix(line, "# "))
		default:
			in = false
		}
	}
	if len(out) < 2 {
		t.Fatal("no commented [app_classes] example found")
	}
	return strings.Join(out, "\n") + "\n"
}

func TestAppClassesExamplesParseOnceUncommented(t *testing.T) {
	file, err := os.ReadFile(exampleConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"DefaultTOML": DefaultTOML(""), "examples/config.toml": string(file)} {
		t.Run(name, func(t *testing.T) {
			cfg, err := Load(writeConfig(t, uncommentedAppClasses(t, text)))
			if err != nil {
				t.Fatalf("uncommented example does not load: %v", err)
			}
			if cfg.AppClasses["us.zoom.xos"] != ClassNeutral || cfg.AppClasses["com.apple.Music"] != ClassPersonal {
				t.Errorf("AppClasses = %v", cfg.AppClasses)
			}
		})
	}
}
