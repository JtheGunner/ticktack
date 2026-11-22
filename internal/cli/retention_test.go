package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/render"
	"github.com/JtheGunner/ticktack/internal/retention"
	"github.com/JtheGunner/ticktack/internal/sink"
	"github.com/JtheGunner/ticktack/internal/youtrack"
)

// retentionNow is far enough after the seeded day 2026-01-05 for retention.
var retentionNow = time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

// bookedSeededDay books the seeded day 2026-01-05 through review and sync and
// returns the deps with retention turned on 30 days.
func bookedSeededDay(t *testing.T) (syncDeps, *recordingSink) {
	t.Helper()
	deps, rec := syncTestDeps(t)
	bookSeededDay(t, deps, rec)
	load := deps.LoadConfig
	deps.LoadConfig = func() (config.Config, string, error) {
		cfg, path, err := load()
		cfg.RetentionDays = 30
		return cfg, path, err
	}
	deps.Now = func() time.Time { return retentionNow }
	return deps, rec
}

func purgeDeps(t *testing.T, deps syncDeps, input string) (dailyDeps, *bytes.Buffer) {
	t.Helper()
	out := &bytes.Buffer{}
	return dailyDeps{
		OpenStore: func() (dailyStore, error) { return deps.OpenStore() },
		LoadConfig: func() (config.Config, error) {
			cfg, _, err := deps.LoadConfig()
			return cfg, err
		},
		Now:    deps.Now,
		In:     strings.NewReader(input),
		Out:    out,
		Render: render.Plain(),
	}, out
}

func seededEventCount(t *testing.T, deps syncDeps) int {
	t.Helper()
	st, err := deps.OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	events, err := st.Events(context.Background(), time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 6, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return len(events)
}

func TestPurgeExpiredDryRunPrintsCountsAndDeletesNothing(t *testing.T) {
	deps, _ := bookedSeededDay(t)
	before := seededEventCount(t, deps)
	d, out := purgeDeps(t, deps, "")

	if err := runPurgeExpired(context.Background(), d, false, true); err != nil {
		t.Fatal(err)
	}
	// One event, the context the next day starts from, always stays.
	want := before - 1
	for _, text := range []string{"would delete", "events of 1 fully booked days", "no longer be re-derived"} {
		if !strings.Contains(out.String(), text) {
			t.Errorf("output lacks %q:\n%s", text, out)
		}
	}
	if !strings.Contains(out.String(), "would delete "+strconv.Itoa(want)+" events") {
		t.Errorf("output lacks the count %d:\n%s", want, out)
	}
	if got := seededEventCount(t, deps); got != before {
		t.Errorf("dry run changed the events: %d -> %d", before, got)
	}
}

func TestPurgeExpiredAsksBeforeDeletingAndStatesTheLoss(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		yes       bool
		wantGone  bool
		wantInOut string
		wantErr   bool
	}{
		{"declined", "n\n", false, false, "Nothing was deleted", false},
		{"closed input is an error", "", false, false, "re-derived", true},
		{"confirmed", "y\n", false, true, "Those days are archived and frozen", false},
		{"--yes skips the question", "", true, true, "Those days are archived and frozen", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			deps, _ := bookedSeededDay(t)
			before := seededEventCount(t, deps)
			d, out := purgeDeps(t, deps, tc.input)
			err := runPurgeExpired(context.Background(), d, tc.yes, false)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, want error: %v", err, tc.wantErr)
			}
			if !strings.Contains(out.String(), tc.wantInOut) {
				t.Errorf("output lacks %q:\n%s", tc.wantInOut, out)
			}
			got := seededEventCount(t, deps)
			if tc.wantGone && got >= before || !tc.wantGone && got != before {
				t.Errorf("events %d -> %d, wantGone = %v", before, got, tc.wantGone)
			}
		})
	}
}

func TestPurgeExpiredWithRetentionOffDoesNothing(t *testing.T) {
	deps, _ := bookedSeededDay(t)
	load := deps.LoadConfig
	deps.LoadConfig = func() (config.Config, string, error) {
		cfg, path, err := load()
		cfg.RetentionDays = 0
		return cfg, path, err
	}
	before := seededEventCount(t, deps)
	d, out := purgeDeps(t, deps, "y\n")
	if err := runPurgeExpired(context.Background(), d, true, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "retention_days is 0 (off)") {
		t.Errorf("output = %q", out)
	}
	if got := seededEventCount(t, deps); got != before {
		t.Errorf("events %d -> %d with retention off", before, got)
	}
}

func TestPurgeDryRunNeedsExpired(t *testing.T) {
	root := NewRootCommand()
	root.SetArgs([]string{"purge", "--dry-run"})
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "--expired") {
		t.Errorf("err = %v", err)
	}
}

func TestPurgeExpiredSecondRunFindsNothing(t *testing.T) {
	deps, _ := bookedSeededDay(t)
	d, _ := purgeDeps(t, deps, "")
	if err := runPurgeExpired(context.Background(), d, true, false); err != nil {
		t.Fatal(err)
	}
	d, out := purgeDeps(t, deps, "")
	if err := runPurgeExpired(context.Background(), d, true, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Nothing to clean") {
		t.Errorf("second run output = %q", out)
	}
}

// An archived day has no events, but its bookings must stay: sync must not
// read the day as "no time any more" and delete the booked work items.
func TestArchivedDayIsNeverReconciledAwayBySync(t *testing.T) {
	deps, rec := bookedSeededDay(t)
	d, _ := purgeDeps(t, deps, "")
	if err := runPurgeExpired(context.Background(), d, true, false); err != nil {
		t.Fatal(err)
	}

	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer srv.Close()
	deps.NewSink = func(config.Config, string) (sink.TimeSink, error) {
		return youtrack.New(srv.URL, "test-token")
	}

	for _, args := range [][]string{
		{"sync", "--day", "2026-01-05"},
		{"sync", "--day", "2026-01-05", "--dry-run"},
	} {
		_, _, err := execCommand(t, deps, args...)
		if err == nil || !strings.Contains(err.Error(), "archived") || !strings.Contains(err.Error(), "unchanged") {
			t.Errorf("%v: err = %v, want a clear archived-day message", args, err)
		}
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("sync of an archived day sent %d requests to YouTrack", n)
	}
	if len(rec.items) != 1 || rec.items["ABC-1/1"].Minutes != 60 {
		t.Errorf("booked items changed: %v", rec.items)
	}
	st, err := deps.OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	rows, err := st.LedgerRowsForDate(context.Background(), "2026-01-05")
	if err != nil || len(rows) != 1 || rows[0].Minutes != 60 {
		t.Errorf("ledger rows = %+v, %v", rows, err)
	}
}

func TestArchivedDayGivesAClearMessageInEveryDayCommand(t *testing.T) {
	deps, _ := bookedSeededDay(t)
	d, _ := purgeDeps(t, deps, "")
	if err := runPurgeExpired(context.Background(), d, true, false); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	commands := map[string]func() error{
		"today":   func() error { dd, _ := purgeDeps(t, deps, ""); return runToday(ctx, dd, "2026-01-05") },
		"explain": func() error { dd, _ := purgeDeps(t, deps, ""); return runExplain(ctx, dd, "2026-01-05", "1") },
		"review": func() error {
			cfg, _, _ := deps.LoadConfig()
			rd := reviewDeps{
				OpenStore:  func() (reviewStore, error) { return deps.OpenStore() },
				LoadConfig: func() (config.Config, error) { return cfg, nil },
				Now:        deps.Now, In: strings.NewReader("q\n"), Out: &bytes.Buffer{},
			}
			return runReview(ctx, rd, "2026-01-05")
		},
		"auto": func() error {
			return runAuto(ctx, &bytes.Buffer{}, deps, "2026-01-05", true)
		},
	}
	for name, run := range commands {
		t.Run(name, func(t *testing.T) {
			err := run()
			if err == nil || !strings.Contains(err.Error(), "2026-01-05 is archived") || !strings.Contains(err.Error(), "retention_days") {
				t.Errorf("err = %v, want the archived-day message", err)
			}
		})
	}
}

func TestUndoStillWorksAndNamesArchivedDays(t *testing.T) {
	deps, rec := bookedSeededDay(t)
	d, _ := purgeDeps(t, deps, "")
	if err := runPurgeExpired(context.Background(), d, true, false); err != nil {
		t.Fatal(err)
	}
	out, _, err := execCommand(t, deps, "undo")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "touched 1 archived days") {
		t.Errorf("undo output lacks the archived-day note:\n%s", out)
	}
	if len(rec.items) != 0 {
		t.Errorf("undo did not revert the booking: %v", rec.items)
	}
}

func TestRetentionJobCleansOnceAndLogsFailuresWithoutCrashing(t *testing.T) {
	deps, _ := bookedSeededDay(t)
	cfg, _, _ := deps.LoadConfig()
	st, err := deps.OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	busy := func() (func(), error) { return nil, ErrBookingRunActive }
	free := func() (func(), error) { return func() {}, nil }

	if newRetentionJob(config.Config{}, st, free, time.Hour, deps.Now, func(string, ...any) {}) != nil {
		t.Error("a job was built while retention_days is 0")
	}

	t.Run("busy lock skips the run", func(t *testing.T) {
		if got := runRetentionJobOnce(t, newRetentionJob, cfg, st, busy, deps.Now); got != msgRetentionLogBusy {
			t.Errorf("logged %q", got)
		}
		if got := seededEventCount(t, deps); got < 2 {
			t.Errorf("a skipped run deleted events: %d left", got)
		}
	})
	t.Run("cleans and logs the counts", func(t *testing.T) {
		if got := runRetentionJobOnce(t, newRetentionJob, cfg, st, free, deps.Now); got != msgRetentionLogDone {
			t.Errorf("logged %q", got)
		}
		if got := seededEventCount(t, deps); got != 1 {
			t.Errorf("%d events remain, want only the context anchor", got)
		}
	})
	t.Run("a failing store is logged and does not stop the job", func(t *testing.T) {
		_ = st.Close()
		if got := runRetentionJobOnce(t, newRetentionJob, cfg, st, free, deps.Now); got != msgRetentionLogFail {
			t.Errorf("logged %q", got)
		}
	})
}

type retentionJobBuilder func(config.Config, retention.Store, func() (func(), error), time.Duration, func() time.Time, func(string, ...any)) backgroundJob

// runRetentionJobOnce starts the job, waits for its first log line, stops it
// and returns that line's format.
func runRetentionJobOnce(t *testing.T, build retentionJobBuilder, cfg config.Config, st retention.Store, lock func() (func(), error), now func() time.Time) string {
	t.Helper()
	logged := make(chan string, 8)
	job := build(cfg, st, lock, time.Hour, now, func(format string, _ ...any) { logged <- format })
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() { job(ctx); close(finished) }()
	var line string
	select {
	case line = <-logged:
	case <-time.After(10 * time.Second):
		t.Error("the job logged nothing")
	}
	cancel()
	<-finished
	return line
}
