package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/store"
)

func TestCheckRetentionStatesTheSettingAndTheArchivedDays(t *testing.T) {
	tests := []struct {
		name     string
		config   string
		archived int
		want     []string
	}{
		{"off by default", "", 0, []string{"off", "0 archived days"}},
		{"on without archived days", "retention_days = 45\n", 0, []string{"45 days", "0 archived days"}},
		{"on with archived days", "retention_days = 45\n", 2, []string{"45 days", "2 archived days"}},
		{"off with earlier archived days", "retention_days = 0\n", 1, []string{"off", "1 archived days"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newTestEnv(t, "", "")
			writeRepoPathsConfig(t, tt.config)
			archiveDays(t, env, tt.archived)

			res := checkRetention(context.Background(), env.deps.Doctor)
			wantStatus := statusOK
			if tt.want[0] == "off" {
				wantStatus = statusSkip
			}
			if res.Name != msgCheckRetention || res.Status != wantStatus {
				t.Fatalf("result = %+v, want status %v", res, wantStatus)
			}
			for _, w := range tt.want {
				if !strings.Contains(res.Detail, w) {
					t.Errorf("detail %q lacks %q", res.Detail, w)
				}
			}
		})
	}
}

func TestCheckRetentionIsSkippedWhenTheConfigIsBroken(t *testing.T) {
	env := newTestEnv(t, "", "")
	writeRepoPathsConfig(t, "retention_days = \"many\"\n")
	if res := checkRetention(context.Background(), env.deps.Doctor); res.Status != statusSkip {
		t.Fatalf("result = %+v, want skip", res)
	}
}

func TestDoctorReportIncludesTheRetentionLine(t *testing.T) {
	env := newTestEnv(t, "", "")
	var out strings.Builder
	_ = runDoctor(context.Background(), env.deps.Doctor, &out)
	if !strings.Contains(out.String(), msgCheckRetention) {
		t.Fatalf("doctor output lacks the retention line:\n%s", out.String())
	}
}

// archiveDays records n archived days in the test environment's database.
func archiveDays(t *testing.T, env *testEnv, n int) {
	t.Helper()
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
	for i := 0; i < n; i++ {
		from := time.Date(2026, 1, 1+i, 0, 0, 0, 0, time.UTC)
		req := store.ArchiveRequest{Day: from.Format("2006-01-02"), From: from, To: from.AddDate(0, 0, 1), ArchivedAt: from}
		if _, err := st.ArchiveDay(context.Background(), req); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
}
