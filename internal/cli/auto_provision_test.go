package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/auto"
	"github.com/JtheGunner/ticktack/internal/config"
)

// autoStandardTaskDeps is standardTaskDeps with auto mode unlocked by a clean
// track record.
func autoStandardTaskDeps(t *testing.T) (syncDeps, *provisioningSink) {
	t.Helper()
	deps, prov := standardTaskDeps(t)
	load := deps.LoadConfig
	deps.LoadConfig = func() (config.Config, string, error) {
		cfg, path, err := load()
		cfg.Auto = config.Auto{Enabled: true, TrustedDays: 3}
		cfg.DailyCap = 10 * time.Hour
		return cfg, path, err
	}
	seedCleanDays(t, deps, 3)
	return deps, prov
}

func consentTo(t *testing.T, deps syncDeps, project string) {
	t.Helper()
	st, err := deps.OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if err := st.SetConsent(context.Background(), project, time.Date(2026, 1, 4, 18, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
}

func TestAutoCreatesTaskOnlyForConsentedProject(t *testing.T) {
	deps, prov := autoStandardTaskDeps(t)
	consentTo(t, deps, "GEN")

	out, err := runAutoFor(t, deps)
	if err != nil {
		t.Fatalf("auto: %v\n%s", err, out)
	}
	if len(prov.created) != 1 || prov.created[0] != "GEN" {
		t.Fatalf("created = %v, want GEN once", prov.created)
	}
	if _, ok := prov.items["GEN-1/1"]; !ok {
		t.Errorf("time was not booked on GEN-1: %v\n%s", prov.items, out)
	}
	if !strings.Contains(out, "GEN-1") {
		t.Errorf("digest does not name the real task:\n%s", out)
	}
}

func TestAutoSkipsUnconsentedProjectAndSaysSo(t *testing.T) {
	deps, prov := autoStandardTaskDeps(t)

	out, err := runAutoFor(t, deps)
	if err != nil {
		t.Fatalf("auto: %v\n%s", err, out)
	}
	if len(prov.created) != 0 || len(prov.items) != 0 {
		t.Errorf("auto mode created or booked without consent: created %v booked %v", prov.created, prov.items)
	}
	if !strings.Contains(out, "ticktack sync") || !strings.Contains(out, "standard task") {
		t.Errorf("digest does not say why the time was skipped:\n%s", out)
	}
}

func TestAutoUsesAnExistingStandardTaskWithoutConsent(t *testing.T) {
	deps, prov := autoStandardTaskDeps(t)
	prov.existing["GEN"] = "GEN-5"

	if out, err := runAutoFor(t, deps); err != nil {
		t.Fatalf("auto: %v\n%s", err, out)
	}
	if len(prov.created) != 0 {
		t.Errorf("an existing task must not be created again: %v", prov.created)
	}
	if _, ok := prov.items["GEN-5/1"]; !ok {
		t.Errorf("time was not booked on GEN-5: %v", prov.items)
	}
}

func TestAutoDryRunCreatesNothingEvenWithConsent(t *testing.T) {
	deps, prov := autoStandardTaskDeps(t)
	consentTo(t, deps, "GEN")

	if out, err := runAutoFor(t, deps, "--dry-run"); err != nil {
		t.Fatalf("auto --dry-run: %v\n%s", err, out)
	}
	if len(prov.created) != 0 || len(prov.items) != 0 {
		t.Errorf("dry run acted: created %v booked %v", prov.created, prov.items)
	}
}

func TestAutoStillSkipsTimeBelowRuleTier(t *testing.T) {
	// The standard task is tier rule, so it is eligible; time without any
	// attribution is not. Without default_issue nothing changes.
	deps, prov := autoStandardTaskDeps(t)
	load := deps.LoadConfig
	deps.LoadConfig = func() (config.Config, string, error) {
		cfg, path, err := load()
		cfg.DefaultIssue = nil
		return cfg, path, err
	}
	out, err := runAutoFor(t, deps)
	if err != nil {
		t.Fatalf("auto: %v\n%s", err, out)
	}
	if len(prov.created) != 0 || len(prov.items) != 0 {
		t.Errorf("unattributed time was booked: created %v booked %v", prov.created, prov.items)
	}
	if !strings.Contains(out, auto.ReasonUnassigned) {
		t.Errorf("digest does not say the time is unassigned:\n%s", out)
	}
}
