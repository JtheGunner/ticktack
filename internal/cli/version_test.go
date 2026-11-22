package cli

import (
	"bytes"
	"runtime/debug"
	"strings"
	"testing"
)

func TestResolveVersion(t *testing.T) {
	settings := func(kv ...string) []debug.BuildSetting {
		var out []debug.BuildSetting
		for i := 0; i < len(kv); i += 2 {
			out = append(out, debug.BuildSetting{Key: kv[i], Value: kv[i+1]})
		}
		return out
	}
	tests := []struct {
		name    string
		ldflags string
		info    *debug.BuildInfo
		want    string
	}{
		{"ldflags wins", "1.2.3", &debug.BuildInfo{Main: debug.Module{Version: "v9.9.9"}}, "1.2.3"},
		{"no build info", "dev", nil, "dev"},
		{"go install module version", "dev", &debug.BuildInfo{Main: debug.Module{Version: "v0.4.1"}}, "v0.4.1"},
		{"devel without vcs", "dev", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, "dev"},
		{"empty version without vcs", "dev", &debug.BuildInfo{}, "dev"},
		{"devel with revision", "dev", &debug.BuildInfo{
			Main:     debug.Module{Version: "(devel)"},
			Settings: settings("vcs.revision", "0123456789abcdef0123", "vcs.modified", "false"),
		}, "dev-0123456789ab"},
		{"devel with dirty revision", "dev", &debug.BuildInfo{
			Settings: settings("vcs.revision", "abc123", "vcs.modified", "true"),
		}, "dev-abc123-dirty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveVersion(tt.ldflags, tt.info); got != tt.want {
				t.Errorf("resolveVersion = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestVersionCommandPrintsVersion(t *testing.T) {
	original := Version
	t.Cleanup(func() { Version = original })
	Version = "1.2.3-test"

	var out bytes.Buffer
	root := NewRootCommand()
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"version"})

	if err := root.Execute(); err != nil {
		t.Fatalf("execute version command: %v", err)
	}
	if !strings.Contains(out.String(), "1.2.3-test") {
		t.Errorf("output %q does not contain version %q", out.String(), "1.2.3-test")
	}
}
