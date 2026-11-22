package config

import (
	"strings"
	"testing"
)

func TestAppClasses(t *testing.T) {
	tests := []struct {
		name    string
		toml    string
		want    map[string]AppClass
		wantErr string
	}{
		{name: "absent table is empty", toml: ``, want: nil},
		{
			name: "all three classes",
			toml: "[app_classes]\n\"com.slack\" = \"neutral\"\n\"com.music\" = \"personal\"\n\"com.ide\" = \"work\"\n",
			want: map[string]AppClass{"com.slack": ClassNeutral, "com.music": ClassPersonal, "com.ide": ClassWork},
		},
		{
			name:    "unknown class",
			toml:    "[app_classes]\n\"com.slack\" = \"private\"\n",
			wantErr: "unknown class",
		},
		{
			name:    "empty bundle id",
			toml:    "[app_classes]\n\" \" = \"neutral\"\n",
			wantErr: "must not be empty",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load(writeConfig(t, tt.toml))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(cfg.AppClasses) != len(tt.want) {
				t.Fatalf("AppClasses = %v, want %v", cfg.AppClasses, tt.want)
			}
			for id, class := range tt.want {
				if cfg.AppClasses[id] != class {
					t.Errorf("AppClasses[%q] = %q, want %q", id, cfg.AppClasses[id], class)
				}
			}
		})
	}
}
