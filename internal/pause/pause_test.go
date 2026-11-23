package pause

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"
)

var t0 = time.Date(2026, 3, 2, 13, 0, 0, 0, time.UTC)

func flagIn(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "state", "paused")
}

func mustBegin(t *testing.T, flag string, at time.Time) {
	t.Helper()
	if err := Begin(flag, at); err != nil {
		t.Fatal(err)
	}
}

func mustEnd(t *testing.T, flag string, at time.Time) {
	t.Helper()
	ended, err := End(flag, at)
	if err != nil {
		t.Fatal(err)
	}
	if ended.Recovered != nil {
		t.Fatalf("a healthy pause was reported as recovered: %v", ended.Recovered)
	}
}

func mustLoad(t *testing.T, flag string, now time.Time) Intervals {
	t.Helper()
	got, err := Load(flag, now)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestContains(t *testing.T) {
	zurich, err := time.LoadLocation("Europe/Zurich")
	if err != nil {
		t.Fatal(err)
	}
	midnight := Interval{
		Start: time.Date(2026, 3, 2, 23, 30, 0, 0, zurich),
		End:   time.Date(2026, 3, 3, 0, 30, 0, 0, zurich),
	}
	ivs := Intervals{{Start: t0, End: t0.Add(2 * time.Hour)}, midnight}
	cases := []struct {
		name string
		at   time.Time
		want bool
	}{
		{"before", t0.Add(-time.Second), false},
		{"at start", t0, true},
		{"inside", t0.Add(time.Hour), true},
		{"at end", t0.Add(2 * time.Hour), false},
		{"after", t0.Add(3 * time.Hour), false},
		{"before midnight inside", time.Date(2026, 3, 2, 23, 45, 0, 0, zurich), true},
		{"after midnight inside", time.Date(2026, 3, 3, 0, 15, 0, 0, zurich), true},
		{"after midnight inside, as UTC", time.Date(2026, 3, 2, 23, 15, 0, 0, time.UTC), true},
		{"after midnight outside", time.Date(2026, 3, 3, 0, 30, 0, 0, zurich), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ivs.Contains(tc.at); got != tc.want {
				t.Errorf("Contains(%s) = %v, want %v", tc.at, got, tc.want)
			}
		})
	}
	if (Intervals{}).Contains(t0) {
		t.Error("no intervals must contain nothing")
	}
}

func TestLoadWithoutStateIsEmpty(t *testing.T) {
	if got := mustLoad(t, flagIn(t), t0); len(got) != 0 {
		t.Fatalf("intervals = %+v, want none", got)
	}
}

func TestBeginThenEndStoresTheInterval(t *testing.T) {
	flag := flagIn(t)
	mustBegin(t, flag, t0)
	if _, err := os.Stat(flag); err != nil {
		t.Fatalf("pause flag missing after Begin: %v", err)
	}
	mustEnd(t, flag, t0.Add(2*time.Hour))
	if _, err := os.Stat(flag); !os.IsNotExist(err) {
		t.Fatalf("pause flag still present after End: %v", err)
	}
	got := mustLoad(t, flag, t0.Add(5*time.Hour))
	if len(got) != 1 || !got[0].Start.Equal(t0) || !got[0].End.Equal(t0.Add(2*time.Hour)) {
		t.Fatalf("intervals = %+v, want one from %s for 2h", got, t0)
	}
}

func TestIntervalsAccumulate(t *testing.T) {
	flag := flagIn(t)
	mustBegin(t, flag, t0)
	mustEnd(t, flag, t0.Add(time.Hour))
	mustBegin(t, flag, t0.Add(3*time.Hour))
	mustEnd(t, flag, t0.Add(4*time.Hour))
	got := mustLoad(t, flag, t0.Add(5*time.Hour))
	if len(got) != 2 || !got.Contains(t0.Add(30*time.Minute)) || got.Contains(t0.Add(2*time.Hour)) || !got.Contains(t0.Add(210*time.Minute)) {
		t.Fatalf("intervals = %+v, want both pauses and the time between them free", got)
	}
}

func TestRunningPauseCountsUntilNow(t *testing.T) {
	flag := flagIn(t)
	mustBegin(t, flag, t0)
	now := t0.Add(90 * time.Minute)
	got := mustLoad(t, flag, now)
	if len(got) != 1 || !got[0].Start.Equal(t0) || !got[0].End.Equal(now) {
		t.Fatalf("intervals = %+v, want the running pause from %s until %s", got, t0, now)
	}
}

func TestSecondBeginKeepsTheOriginalStart(t *testing.T) {
	flag := flagIn(t)
	mustBegin(t, flag, t0)
	mustBegin(t, flag, t0.Add(time.Hour))
	mustEnd(t, flag, t0.Add(2*time.Hour))
	got := mustLoad(t, flag, t0.Add(3*time.Hour))
	if len(got) != 1 || !got[0].Start.Equal(t0) {
		t.Fatalf("intervals = %+v, want the start of the first pause", got)
	}
}

func TestEndWithoutPauseStoresNothing(t *testing.T) {
	flag := flagIn(t)
	mustEnd(t, flag, t0)
	mustBegin(t, flag, t0)
	mustEnd(t, flag, t0.Add(time.Hour))
	mustEnd(t, flag, t0.Add(2*time.Hour))
	if got := mustLoad(t, flag, t0.Add(3*time.Hour)); len(got) != 1 {
		t.Fatalf("intervals = %+v, want exactly one", got)
	}
}

// A flag file written by an older version is empty; its modification time is
// the best known start.
func TestEmptyFlagFileStartsAtItsModificationTime(t *testing.T) {
	flag := flagIn(t)
	if err := os.MkdirAll(filepath.Dir(flag), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(flag, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(flag, t0, t0); err != nil {
		t.Fatal(err)
	}
	running := mustLoad(t, flag, t0.Add(time.Hour))
	if len(running) != 1 || !running[0].Start.Equal(t0) {
		t.Fatalf("running = %+v, want a start at the modification time", running)
	}
	mustEnd(t, flag, t0.Add(time.Hour))
	closed := mustLoad(t, flag, t0.Add(2*time.Hour))
	if len(closed) != 1 || !closed[0].Start.Equal(t0) || !closed[0].End.Equal(t0.Add(time.Hour)) {
		t.Fatalf("closed = %+v", closed)
	}
}

func TestLoadRejectsACorruptLog(t *testing.T) {
	for name, content := range map[string]string{
		"garbage":       "not a timestamp\n",
		"missing end":   "2026-03-02T13:00:00Z\n",
		"bad end":       "2026-03-02T13:00:00Z tomorrow\n",
		"after a valid": "2026-03-02T13:00:00Z 2026-03-02T15:00:00Z\nbroken line\n",
	} {
		t.Run(name, func(t *testing.T) {
			flag := flagIn(t)
			if err := os.MkdirAll(filepath.Dir(flag), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(LogPath(flag), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(flag, t0)
			if err == nil || !strings.Contains(err.Error(), LogPath(flag)) {
				t.Fatalf("err = %v, want an error that names %s", err, LogPath(flag))
			}
		})
	}
}

func TestLoadRejectsACorruptFlag(t *testing.T) {
	flag := flagIn(t)
	if err := os.MkdirAll(filepath.Dir(flag), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(flag, []byte("yesterday\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(flag, t0); err == nil || !strings.Contains(err.Error(), flag) {
		t.Fatalf("err = %v, want an error that names %s", err, flag)
	}
}

func TestStateFilesArePrivate(t *testing.T) {
	flag := flagIn(t)
	mustBegin(t, flag, t0)
	assertMode := func(path string) {
		t.Helper()
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s has mode %v, want 0600", path, info.Mode().Perm())
		}
	}
	assertMode(flag)
	mustEnd(t, flag, t0.Add(time.Hour))
	assertMode(LogPath(flag))
}

func TestIsPausedIsFailClosed(t *testing.T) {
	dir := t.TempDir()
	flag := filepath.Join(dir, "paused")
	if IsPaused(flag) {
		t.Error("a missing flag file is not a pause")
	}
	if err := os.WriteFile(flag, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !IsPaused(flag) {
		t.Error("an existing flag file is a pause")
	}
	// A path below a regular file cannot be read (ENOTDIR): unknown means paused.
	if !IsPaused(filepath.Join(flag, "below")) {
		t.Error("an unreadable pause state must count as paused")
	}
	if _, err := Running(filepath.Join(flag, "below")); err == nil {
		t.Error("Running must report the read error")
	}
}

func TestOutsideDropsEventsInsideAPause(t *testing.T) {
	at := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	ivs := Intervals{{Start: at, End: at.Add(time.Hour)}}
	events := []event.Event{{At: at.Add(-time.Minute)}, {At: at}, {At: at.Add(time.Hour)}}
	got := ivs.Outside(events)
	if len(got) != 2 || !got[0].At.Equal(events[0].At) || !got[1].At.Equal(events[2].At) {
		t.Fatalf("got %+v", got)
	}
}

func TestEndRecoversAFlagWithAnUnparsableStart(t *testing.T) {
	flag := flagIn(t)
	if err := os.MkdirAll(filepath.Dir(flag), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(flag, []byte("yesterday\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(flag, t0, t0); err != nil {
		t.Fatal(err)
	}
	ended, err := End(flag, t0.Add(time.Hour))
	if err != nil {
		t.Fatalf("End must still end the pause: %v", err)
	}
	if ended.Recovered == nil || !strings.Contains(ended.Recovered.Error(), flag) {
		t.Fatalf("Recovered = %v, want the problem named with the flag path", ended.Recovered)
	}
	if running, err := Running(flag); err != nil || running {
		t.Fatalf("Running = %v, %v, want the flag removed", running, err)
	}
	got := mustLoad(t, flag, t0.Add(2*time.Hour))
	if len(got) != 1 || !got[0].Start.Equal(t0) || !got[0].End.Equal(t0.Add(time.Hour)) {
		t.Fatalf("intervals = %+v, want the pause from the modification time to the resume", got)
	}
}

func TestEndRecoversAnUnreadableFlag(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every file")
	}
	flag := flagIn(t)
	mustBegin(t, flag, t0)
	if err := os.Chmod(flag, 0); err != nil {
		t.Fatal(err)
	}
	ended, err := End(flag, t0.Add(time.Hour))
	if err != nil {
		t.Fatalf("End must still end the pause: %v", err)
	}
	if ended.Recovered == nil {
		t.Fatal("want the read problem reported")
	}
	if got := mustLoad(t, flag, t0.Add(2*time.Hour)); len(got) != 1 || !got[0].End.Equal(t0.Add(time.Hour)) {
		t.Fatalf("intervals = %+v", got)
	}
}

func TestLoadStaysFailClosedForAnUnparsableFlag(t *testing.T) {
	flag := flagIn(t)
	if err := os.MkdirAll(filepath.Dir(flag), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(flag, []byte("yesterday\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(flag, t0); err == nil {
		t.Fatal("Load must refuse a flag whose start is unknown")
	}
}

func gapEvent(at time.Time, idle time.Duration) event.Event {
	return event.Event{At: at, Kind: event.KindGap, IdleSeconds: int(idle / time.Second), Key: "gap"}
}

func TestOutsideClipsAGapThatReachesIntoAPause(t *testing.T) {
	pauseStart := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	pauseEnd := pauseStart.Add(time.Hour)
	midnight := time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		ivs      Intervals
		in       event.Event
		wantAt   time.Time
		wantIdle int
		dropped  bool
	}{
		{"ends inside the pause", Intervals{{pauseStart, pauseEnd}},
			gapEvent(pauseStart.Add(20*time.Minute), 50*time.Minute), pauseStart, 30 * 60, false},
		{"starts inside the pause", Intervals{{pauseStart, pauseEnd}},
			gapEvent(pauseEnd.Add(30*time.Minute), 50*time.Minute), pauseEnd.Add(30 * time.Minute), 30 * 60, false},
		{"spans the pause, the later part stays", Intervals{{pauseStart, pauseEnd}},
			gapEvent(pauseEnd.Add(10*time.Minute), 3*time.Hour), pauseEnd.Add(10 * time.Minute), 10 * 60, false},
		{"wholly inside the pause", Intervals{{pauseStart, pauseEnd}},
			gapEvent(pauseStart.Add(40*time.Minute), 20*time.Minute), time.Time{}, 0, true},
		{"outside every pause", Intervals{{pauseStart, pauseEnd}},
			gapEvent(pauseStart.Add(-time.Hour), 30*time.Minute), pauseStart.Add(-time.Hour), 30 * 60, false},
		{"pause across midnight", Intervals{{midnight.Add(-30 * time.Minute), midnight.Add(30 * time.Minute)}},
			gapEvent(midnight.Add(10*time.Minute), time.Hour), midnight.Add(-30 * time.Minute), 20 * 60, false},
		{"running pause until now", Intervals{{pauseStart, pauseStart.Add(2 * time.Hour)}},
			gapEvent(pauseStart.Add(2*time.Hour), 3*time.Hour), pauseStart, 60 * 60, false},
		{"overlapping pauses", Intervals{{pauseStart, pauseStart.Add(30 * time.Minute)}, {pauseStart.Add(20 * time.Minute), pauseEnd}},
			gapEvent(pauseEnd.Add(15*time.Minute), 80*time.Minute), pauseEnd.Add(15 * time.Minute), 15 * 60, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.ivs.Outside([]event.Event{tc.in})
			if tc.dropped {
				if len(got) != 0 {
					t.Fatalf("got %+v, want the gap dropped", got)
				}
				return
			}
			if len(got) != 1 || !got[0].At.Equal(tc.wantAt) || got[0].IdleSeconds != tc.wantIdle || got[0].Key != "gap" {
				t.Fatalf("got %+v, want At %s idle %ds with the same key", got, tc.wantAt, tc.wantIdle)
			}
			start := got[0].At.Add(-time.Duration(got[0].IdleSeconds) * time.Second)
			for _, iv := range tc.ivs {
				if start.Before(iv.End) && got[0].At.After(iv.Start) {
					t.Errorf("kept gap %s..%s still overlaps the pause %v", start, got[0].At, iv)
				}
			}
		})
	}
}

func TestOutsideLeavesEventsWithoutAnEndTimeAlone(t *testing.T) {
	at := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)
	ivs := Intervals{{Start: at.Add(30 * time.Minute), End: at.Add(time.Hour)}}
	events := []event.Event{
		{At: at, Kind: event.KindContext, IdleSeconds: 7200},
		{At: at, Kind: event.KindGap},
	}
	got := ivs.Outside(events)
	if len(got) != 2 || got[0].IdleSeconds != 7200 || got[1].IdleSeconds != 0 {
		t.Fatalf("got %+v, want both untouched", got)
	}
}

func damagedFlagWithMtime(t *testing.T, mtime time.Time) string {
	t.Helper()
	flag := flagIn(t)
	if err := os.MkdirAll(filepath.Dir(flag), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(flag, []byte("garbage\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(flag, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return flag
}

func TestEndClampsAFutureModificationTimeToNow(t *testing.T) {
	now := t0.Add(time.Hour)
	for name, mtime := range map[string]time.Time{"future": now.Add(48 * time.Hour), "exactly now": now} {
		t.Run(name, func(t *testing.T) {
			flag := damagedFlagWithMtime(t, mtime)
			ended, err := End(flag, now)
			if err != nil || ended.Recovered == nil {
				t.Fatalf("End = %+v, %v, want a recovered end", ended, err)
			}
			got := mustLoad(t, flag, now.Add(time.Hour))
			if len(got) != 1 || !got[0].Start.Equal(now) || !got[0].End.Equal(now) {
				t.Fatalf("intervals = %+v, want a valid interval at now", got)
			}
		})
	}
}

func TestLoadRejectsALogLineThatEndsBeforeItStarts(t *testing.T) {
	flag := flagIn(t)
	if err := os.MkdirAll(filepath.Dir(flag), 0o700); err != nil {
		t.Fatal(err)
	}
	line := t0.Add(time.Hour).Format(time.RFC3339Nano) + " " + t0.Format(time.RFC3339Nano) + "\n"
	if err := os.WriteFile(LogPath(flag), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(flag, t0); err == nil {
		t.Fatal("Load must fail closed on an inverted interval")
	}
}
