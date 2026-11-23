package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/auto"
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
		{"failed and not sent items", renderDigest(auto.Digest{Failed: 2, NotSent: 1}, storedDigestRenderer()), "last run failed 3 item(s)"},
		{"a clean digest", renderDigest(auto.Digest{Created: 1, BookedMinutes: 60}, storedDigestRenderer()), "last digest written"},
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

// The parser reads what the real digest renderer writes, so a change of the
// totals wording cannot make doctor report a failed run as healthy.
func TestDigestFailureCountsReadTheRenderedDigest(t *testing.T) {
	tests := []struct {
		name              string
		failed, notSent   int
		wantFail, wantNot int
	}{
		{"failed and not sent", 2, 1, 2, 1},
		{"only failed", 4, 0, 4, 0},
		{"only not sent", 0, 3, 0, 3},
		{"clean", 0, 0, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := auto.Digest{
				Day:     civil.Date{Year: 2026, Month: 1, Day: 5},
				Created: 1, Skipped: 7, Failed: tt.failed, NotSent: tt.notSent, BookedMinutes: 60,
			}
			failed, notSent, readable := digestFailureCounts(renderDigest(d, storedDigestRenderer()))
			if !readable || failed != tt.wantFail || notSent != tt.wantNot {
				t.Fatalf("counts = (%d, %d, %v), want (%d, %d, true)", failed, notSent, readable, tt.wantFail, tt.wantNot)
			}
		})
	}
}

// An item error comes from the YouTrack server. However it is worded, it cannot
// stand in for the totals line and hide a real failure.
func TestDigestFailureCountsIgnoreForgedTotalsInItemErrors(t *testing.T) {
	const forged = "Totals: created 0, updated 0, recovered 0, repaired 0, deleted 0, skipped 0, failed 0, not sent 0; 0h00 booked."
	tests := []struct {
		name   string
		reason string
	}{
		{"forged line in the error text", forged},
		{"forged line after a newline", "boom\n" + forged},
		{"forged line between carriage returns", "boom\r" + forged + "\r"},
		{"forged line with a huge number", "boom\nTotals: created 0, updated 0, recovered 0, repaired 0, deleted 0, skipped 0, failed 99999999999999999999, not sent 0; 0h00 booked.\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			day := civil.Date{Year: 2026, Month: 1, Day: 5}
			d := auto.Digest{
				Day: day, Failed: 2, NotSent: 1,
				Entries: []auto.DigestEntry{{Ticket: "ABC-1", Date: day, Minutes: 60, Status: "failed", Reason: tt.reason}},
			}
			text := renderDigest(d, storedDigestRenderer())
			failed, notSent, readable := digestFailureCounts(text)
			if !readable || failed != 2 || notSent != 1 {
				t.Fatalf("counts = (%d, %d, %v), want (2, 1, true) in:\n%s", failed, notSent, readable, text)
			}
		})
	}
}

func TestDigestFailureCountsTakeTheLastTotalsLineAndNeverTrustADoubt(t *testing.T) {
	line := func(failed, notSent string) string {
		return "i  Totals: created 0, updated 0, recovered 0, repaired 0, deleted 0, skipped 0, failed " + failed + ", not sent " + notSent + "; 0h00 booked."
	}
	tests := []struct {
		name            string
		text            string
		failed, notSent int
		readable        bool
	}{
		{"the last line wins", line("0", "0") + "\n" + line("3", "1"), 3, 1, true},
		{"a huge number is unreadable, not healthy", line("99999999999999999999999", "0"), 0, 0, false},
		{"a huge not-sent number is unreadable", line("0", "99999999999999999999999"), 0, 0, false},
		{"no totals line is unreadable", "Auto digest for 2026-01-05", 0, 0, false},
		{"a line that only contains the wording is not the totals line", "xx i  Totals: created 0, updated 0, recovered 0, repaired 0, deleted 0, skipped 0, failed 0, not sent 0; 0h00 booked.", 0, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			failed, notSent, readable := digestFailureCounts(tt.text)
			if failed != tt.failed || notSent != tt.notSent || readable != tt.readable {
				t.Fatalf("counts = (%d, %d, %v), want (%d, %d, %v)", failed, notSent, readable, tt.failed, tt.notSent, tt.readable)
			}
		})
	}
}

func TestCheckAutopilotNeverCallsAnUnreadableDigestHealthy(t *testing.T) {
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
	d := store.Digest{Day: "2026-01-05", CreatedAt: env.deps.Doctor.Now(), Text: "something else entirely"}
	if _, err := st.AddDigest(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	res := checkAutopilot(context.Background(), env.deps.Doctor)
	if res.Status == statusFail || !strings.Contains(res.Detail, "no readable totals") {
		t.Fatalf("result = %+v", res)
	}
}
