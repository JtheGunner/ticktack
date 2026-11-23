package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/store"
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

func TestCheckAutopilotShowsTheFailureOfTheLastDigest(t *testing.T) {
	tests := []struct {
		name   string
		totals string
		want   string
	}{
		{"failed and not sent items", "Totals: created 0, updated 0, recovered 0, repaired 0, deleted 0, skipped 0, failed 2, not sent 1; 0h00 booked.", "last run failed 3 item(s)"},
		{"a clean digest", "Totals: created 1, updated 0, recovered 0, repaired 0, deleted 0, skipped 0, failed 0, not sent 0; 1h00 booked.", "last digest written"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newTestEnv(t, "", "")
			writeRepoPathsConfig(t, "[auto]\nenabled = true\nmode = \"autopilot\"\n")
			dbPath, err := env.deps.Doctor.DBPath()
			if err != nil {
				t.Fatal(err)
			}
			opened, err := env.deps.Doctor.OpenStore(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			st, ok := opened.(*store.Store)
			if !ok {
				t.Fatal("the test store is not a *store.Store")
			}
			d := store.Digest{Day: "2026-01-05", CreatedAt: env.deps.Doctor.Now().Add(-90 * time.Minute), Text: tt.totals}
			if _, err := st.AddDigest(context.Background(), d); err != nil {
				t.Fatal(err)
			}
			_ = st.Close()

			res := checkAutopilot(context.Background(), env.deps.Doctor)
			if res.Status == statusFail || !strings.Contains(res.Detail, tt.want) {
				t.Fatalf("result = %+v, want detail with %q", res, tt.want)
			}
		})
	}
}
