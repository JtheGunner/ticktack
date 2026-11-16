package config

import (
	"strings"
	"testing"
)

func TestLoadRepoRoots(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    []string
		wantErr string
	}{
		{name: "absolute and tilde", body: "repo_roots = [\"/work\", \"~/Projects\"]\n", want: []string{"/work", "~/Projects"}},
		{name: "absent", body: "", want: nil},
		{name: "relative", body: "repo_roots = [\"rel/path\"]\n", wantErr: "repo_roots[0]"},
		{name: "second entry invalid", body: "repo_roots = [\"/work\", \"~bob/x\"]\n", wantErr: "repo_roots[1]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load(writeConfig(t, tt.body))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want it to mention %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(cfg.RepoRoots) != len(tt.want) {
				t.Fatalf("RepoRoots = %v, want %v", cfg.RepoRoots, tt.want)
			}
			for i := range tt.want {
				if cfg.RepoRoots[i] != tt.want[i] {
					t.Errorf("RepoRoots[%d] = %q, want %q", i, cfg.RepoRoots[i], tt.want[i])
				}
			}
		})
	}
}

func TestLoadDefaultIssue(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantProject string
		wantSummary string
		wantNil     bool
		wantErr     string
	}{
		{name: "both keys", body: "[default_issue]\nproject = \"general\"\nsummary = \"  Meetings / Admin \"\n", wantProject: "GENERAL", wantSummary: "Meetings / Admin"},
		{name: "table absent", body: "", wantNil: true},
		{name: "project missing", body: "[default_issue]\nsummary = \"x\"\n", wantErr: "default_issue.project"},
		{name: "summary missing", body: "[default_issue]\nproject = \"GEN\"\n", wantErr: "default_issue.summary"},
		{name: "blank project", body: "[default_issue]\nproject = \"  \"\nsummary = \"x\"\n", wantErr: "default_issue.project"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load(writeConfig(t, tt.body))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want it to mention %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tt.wantNil {
				if cfg.DefaultIssue != nil {
					t.Fatalf("DefaultIssue = %+v, want nil", cfg.DefaultIssue)
				}
				return
			}
			if cfg.DefaultIssue == nil {
				t.Fatal("DefaultIssue is nil")
			}
			if cfg.DefaultIssue.Project != tt.wantProject || cfg.DefaultIssue.Summary != tt.wantSummary {
				t.Errorf("DefaultIssue = %+v, want {%s %s}", *cfg.DefaultIssue, tt.wantProject, tt.wantSummary)
			}
		})
	}
}
