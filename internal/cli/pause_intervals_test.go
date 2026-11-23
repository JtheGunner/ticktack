package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/pause"
)

// pauseBetween records a pause from start to end through the pause and
// resume commands.
func pauseBetween(t *testing.T, deps dailyDeps, start, end time.Time) {
	t.Helper()
	deps.Out = &bytes.Buffer{}
	deps.Now = func() time.Time { return start }
	if err := runPause(deps); err != nil {
		t.Fatal(err)
	}
	deps.Now = func() time.Time { return end }
	if err := runResume(deps); err != nil {
		t.Fatal(err)
	}
}

// breakPauseLog writes a pause log that cannot be parsed and returns its path.
func breakPauseLog(t *testing.T, deps dailyDeps) string {
	t.Helper()
	pausePath, err := deps.PausePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(pausePath), 0o700); err != nil {
		t.Fatal(err)
	}
	logPath := pause.LogPath(pausePath)
	if err := os.WriteFile(logPath, []byte("broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return logPath
}

func noPauses(time.Time) (pause.Intervals, error) { return nil, nil }

func TestPauseKeepsItsStartAndResumeStoresTheInterval(t *testing.T) {
	deps, _, _ := newDailyEnv(t, "")
	start := dailyNow.Add(-2 * time.Hour)
	deps.Now = func() time.Time { return start }
	if err := runPause(deps); err != nil {
		t.Fatal(err)
	}
	deps.Now = func() time.Time { return start.Add(30 * time.Minute) }
	if err := runPause(deps); err != nil {
		t.Fatal(err)
	}
	pausePath, err := deps.PausePath()
	if err != nil {
		t.Fatal(err)
	}

	running, err := pause.Load(pausePath, dailyNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(running) != 1 || !running[0].Start.Equal(start) || !running[0].End.Equal(dailyNow) {
		t.Fatalf("running pause = %+v, want %s until now", running, start)
	}

	end := start.Add(time.Hour)
	deps.Now = func() time.Time { return end }
	if err := runResume(deps); err != nil {
		t.Fatal(err)
	}
	finished, err := pause.Load(pausePath, dailyNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(finished) != 1 || !finished[0].Start.Equal(start) || !finished[0].End.Equal(end) {
		t.Fatalf("finished pause = %+v, want %s to %s", finished, start, end)
	}
}

func TestScanDropsEventsFromAFinishedPause(t *testing.T) {
	repo := newScanRepo(t, t.TempDir(), "proj")
	env := newScanEnv(t, repo)
	// The checkout of 26 Feb 09:00 lies inside the pause, the commit of
	// 27 Feb 09:00 after it.
	pauseBetween(t, env.deps,
		time.Date(2026, 2, 26, 8, 0, 0, 0, time.UTC), time.Date(2026, 2, 26, 10, 0, 0, 0, time.UTC))

	if err := env.scan(""); err != nil {
		t.Fatal(err)
	}
	events := env.events(t)
	if len(events) != 1 || !events[0].At.Equal(time.Date(2026, 2, 27, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("events = %+v, want only the commit made after the pause", events)
	}
}

func TestScanDropsEventsFromAPauseAcrossMidnight(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed; skipping scan tests that need a real repository")
	}
	dir := filepath.Join(t.TempDir(), "night")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	beforePause := time.Date(2026, 3, 1, 23, 0, 0, 0, time.UTC)
	lateEvening := time.Date(2026, 3, 1, 23, 45, 0, 0, time.UTC)
	earlyMorning := time.Date(2026, 3, 2, 0, 15, 0, 0, time.UTC)
	afterPause := time.Date(2026, 3, 2, 1, 0, 0, 0, time.UTC)
	gitIn(t, dir, beforePause, "init", "-q", "-b", "feature/ABC-12-login")
	gitCommit(t, dir, beforePause, "a.txt")
	gitCommit(t, dir, lateEvening, "b.txt")
	gitCommit(t, dir, earlyMorning, "c.txt")
	gitCommit(t, dir, afterPause, "d.txt")
	repo, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name  string
		since string
		want  []time.Time
	}{
		{"default window", "", []time.Time{beforePause, afterPause}},
		{"since the second day", "2026-03-02", []time.Time{afterPause}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newScanEnv(t, repo)
			pauseBetween(t, env.deps,
				time.Date(2026, 3, 1, 23, 30, 0, 0, time.UTC), time.Date(2026, 3, 2, 0, 30, 0, 0, time.UTC))
			if err := env.scan(tc.since); err != nil {
				t.Fatal(err)
			}
			events := env.events(t)
			if len(events) != len(tc.want) {
				t.Fatalf("events = %+v, want them at %v", events, tc.want)
			}
			for i, e := range events {
				if !e.At.Equal(tc.want[i]) {
					t.Errorf("event %d at %s, want %s", i, e.At, tc.want[i])
				}
			}
		})
	}
}

func TestScanStoresNothingWhenThePauseLogIsUnreadable(t *testing.T) {
	repo := newScanRepo(t, t.TempDir(), "proj")
	env := newScanEnv(t, repo)
	logPath := breakPauseLog(t, env.deps)

	err := env.scan("")
	if err == nil || !strings.Contains(err.Error(), logPath) {
		t.Fatalf("err = %v, want an error that names the pause log", err)
	}
	if got := len(env.events(t)); got != 0 {
		t.Fatalf("%d events stored without a readable pause log", got)
	}
}

func TestImportDropsEventsFromAFinishedPause(t *testing.T) {
	deps, _, out := newDailyEnv(t, "")
	enableActivityWatch(&deps)
	srv, _ := awTestServer(t)
	var called bool
	// The server reports presence at 9:00:00, 9:00:30 and 9:01:00 and a
	// context hint at 9:00:00; only the 9:00:30 sample lies inside the pause.
	pauseBetween(t, deps,
		time.Date(2026, 3, 2, 9, 0, 20, 0, time.UTC), time.Date(2026, 3, 2, 9, 0, 50, 0, time.UTC))

	if err := runImportActivityWatch(context.Background(), deps, awSourceFor(srv, &called), "2026-03-02"); err != nil {
		t.Fatal(err)
	}
	if got := storedCount(t, deps); got != 3 || labelValue(out.String(), "Presence") != "2" || labelValue(out.String(), "Context hints") != "1" {
		t.Fatalf("stored = %d, want 2 presence samples and 1 context hint; output:\n%s", got, out.String())
	}
}

func TestImportDropsEveryKindInsideAPause(t *testing.T) {
	deps, _, out := newDailyEnv(t, "")
	enableActivityWatch(&deps)
	srv, _ := awTestServer(t)
	var called bool
	pauseBetween(t, deps,
		time.Date(2026, 3, 2, 8, 30, 0, 0, time.UTC), time.Date(2026, 3, 2, 9, 30, 0, 0, time.UTC))

	if err := runImportActivityWatch(context.Background(), deps, awSourceFor(srv, &called), "2026-03-02"); err != nil {
		t.Fatal(err)
	}
	if got := storedCount(t, deps); got != 0 || labelValue(out.String(), "Presence") != "0" || labelValue(out.String(), "Context hints") != "0" {
		t.Fatalf("stored = %d, want nothing from the paused hour; output:\n%s", got, out.String())
	}
}

func TestImportStoresNothingWhenThePauseLogIsUnreadable(t *testing.T) {
	deps, _, _ := newDailyEnv(t, "")
	enableActivityWatch(&deps)
	srv, _ := awTestServer(t)
	logPath := breakPauseLog(t, deps)

	var called bool
	err := runImportActivityWatch(context.Background(), deps, awSourceFor(srv, &called), "2026-03-02")
	if err == nil || !strings.Contains(err.Error(), logPath) {
		t.Fatalf("err = %v, want an error that names the pause log", err)
	}
	if called || storedCount(t, deps) != 0 {
		t.Fatal("an import without a readable pause log must not contact the server or store anything")
	}
}

// runJobUntil runs job until done reports true, then stops it.
func runJobUntil(t *testing.T, job backgroundJob, done func() bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() { job(ctx); close(finished) }()
	deadline := time.After(5 * time.Second)
	for !done() {
		select {
		case <-deadline:
			cancel()
			<-finished
			t.Fatal("the scan job did not get there in time")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	<-finished
}

func TestScanJobDropsEventsFromAPause(t *testing.T) {
	repo := fakeRepoDir(t)
	inside, outside := dailyNow.Add(-90*time.Minute), dailyNow.Add(-10*time.Minute)
	scan := func(_ context.Context, path string, _, _ time.Time, _ *regexp.Regexp) ([]event.Event, error) {
		return []event.Event{
			{At: inside, Source: "git", Kind: event.KindContext, Repo: path, Key: "inside"},
			{At: outside, Source: "git", Kind: event.KindContext, Repo: path, Key: "outside"},
		}, nil
	}
	var asked atomic.Value
	pauses := func(now time.Time) (pause.Intervals, error) {
		asked.Store(now)
		return pause.Intervals{{Start: dailyNow.Add(-2 * time.Hour), End: dailyNow.Add(-time.Hour)}}, nil
	}
	sink := &syncSink{}
	cfg := config.Config{RepoPaths: []string{repo}, Location: time.UTC}
	job := newScanJob(cfg, "/home/u", sink, scan, func() bool { return false }, pauses, time.Hour,
		func() time.Time { return dailyNow }, t.Logf)
	runJobUntil(t, job, func() bool { return sink.count() > 0 })

	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.events) != 1 || sink.events[0].Key != "outside" {
		t.Fatalf("stored %+v, want only the event outside the pause", sink.events)
	}
	if got, _ := asked.Load().(time.Time); !got.Equal(dailyNow) {
		t.Errorf("pauses were loaded for %v, want the scan time %v", got, dailyNow)
	}
}

func TestScanJobSkipsThePassWhenPausesCannotBeLoaded(t *testing.T) {
	repo := fakeRepoDir(t)
	var scans atomic.Int32
	scan := func(_ context.Context, path string, _, _ time.Time, _ *regexp.Regexp) ([]event.Event, error) {
		scans.Add(1)
		return []event.Event{{At: dailyNow, Source: "git", Kind: event.KindContext, Repo: path, Key: "k"}}, nil
	}
	var logged atomic.Int32
	logf := func(string, ...any) { logged.Add(1) }
	pauses := func(time.Time) (pause.Intervals, error) { return nil, errors.New("pause log unreadable") }
	sink := &syncSink{}
	cfg := config.Config{RepoPaths: []string{repo}, Location: time.UTC}
	job := newScanJob(cfg, "/home/u", sink, scan, func() bool { return false }, pauses, 10*time.Millisecond,
		func() time.Time { return dailyNow }, logf)
	runJobUntil(t, job, func() bool { return logged.Load() >= 2 })

	if scans.Load() != 0 || sink.count() != 0 {
		t.Fatalf("scanned %d times and stored %d events without knowing the pauses", scans.Load(), sink.count())
	}
}

func TestResumeEndsAPauseWhoseFlagIsBroken(t *testing.T) {
	deps, _, _ := newDailyEnv(t, "")
	pausePath, err := deps.PausePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(pausePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pausePath, []byte("not a time\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	start := dailyNow.Add(-2 * time.Hour)
	if err := os.Chtimes(pausePath, start, start); err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	deps.Out = out
	if err := runResume(deps); err != nil {
		t.Fatalf("resume must succeed: %v", err)
	}
	if !strings.Contains(out.String(), "damaged") {
		t.Errorf("output %q does not report the damaged marker", out.String())
	}
	if running, err := pause.Running(pausePath); err != nil || running {
		t.Fatalf("Running = %v, %v, want the pause ended", running, err)
	}
	ivs, err := pause.Load(pausePath, dailyNow)
	if err != nil || len(ivs) != 1 || !ivs[0].Start.Equal(start) {
		t.Fatalf("intervals = %+v, %v, want one starting at the marker's modification time", ivs, err)
	}
}
