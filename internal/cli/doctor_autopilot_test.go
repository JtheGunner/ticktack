package cli

import (
	"context"
	"strings"
	"testing"
)

func TestCheckAutopilotStatesTheMode(t *testing.T) {
	tests := []struct {
		name       string
		config     string
		wantStatus checkStatus
		want       []string
	}{
		{"off by default", "", statusSkip, []string{"off", "not enabled"}},
		{"enabled but gated", "[auto]\nenabled = true\n", statusInfo, []string{"off", "gated", "trust gate"}},
		{"mode set but not enabled", "[auto]\nmode = \"autopilot\"\n", statusSkip, []string{"off", "not enabled"}},
		{"active with the default lookback", "[auto]\nenabled = true\nmode = \"autopilot\"\n", statusInfo, []string{"active", "7 days", "no digest written yet"}},
		{"active with a custom lookback", "[auto]\nenabled = true\nmode = \"autopilot\"\nautopilot_lookback_days = 3\n", statusInfo, []string{"active", "3 days"}},
		{"broken config is skipped", "[auto]\nmode = \"robot\"\n", statusSkip, []string{"config cannot be read"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newTestEnv(t, "", "")
			writeRepoPathsConfig(t, tt.config)
			res := checkAutopilot(context.Background(), env.deps.Doctor)
			if res.Name != msgCheckAutopilot || res.Status != tt.wantStatus {
				t.Fatalf("result = %+v, want status %v", res, tt.wantStatus)
			}
			for _, w := range tt.want {
				if !strings.Contains(res.Detail, w) {
					t.Errorf("detail %q lacks %q", res.Detail, w)
				}
			}
		})
	}
}

func TestDoctorReportIncludesTheAutopilotLine(t *testing.T) {
	env := newTestEnv(t, "", "")
	var out strings.Builder
	_ = runDoctor(context.Background(), env.deps.Doctor, &out)
	if !strings.Contains(out.String(), msgCheckAutopilot) {
		t.Fatalf("doctor output lacks the autopilot line:\n%s", out.String())
	}
}
