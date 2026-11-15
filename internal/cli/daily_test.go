package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/collector/presence"
	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/paths"
	"github.com/JtheGunner/ticktack/internal/store"
)

var dailyNow = time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)

// newDailyEnv returns deps backed by a temp DB and temp XDG dirs.
func newDailyEnv(t *testing.T, input string) (dailyDeps, *store.Store, *bytes.Buffer) {
	t.Helper()
	stateDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateDir)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	st, err := store.Open(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	out := &bytes.Buffer{}
	deps := dailyDeps{
		// Close is a no-op so the shared test store stays usable.
		OpenStore: func() (dailyStore, error) { return nopCloseStore{st}, nil },
		LoadConfig: func() (config.Config, error) {
			cfg, err := config.LoadOrDefault(filepath.Join(t.TempDir(), "missing.toml"))
			cfg.Location = time.UTC // keep assertions independent of the host zone
			return cfg, err
		},
		Now:       func() time.Time { return dailyNow },
		PausePath: func() (string, error) { return paths.PausePath() },
		In:        strings.NewReader(input),
		Out:       out,
	}
	return deps, st, out
}

type nopCloseStore struct{ *store.Store }

func (nopCloseStore) Close() error { return nil }

func mustAppend(t *testing.T, st *store.Store, events ...event.Event) {
	t.Helper()
	for _, e := range events {
		if err := st.Append(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}
}

func countEvents(t *testing.T, st *store.Store) int {
	t.Helper()
	events, err := st.Events(context.Background(), time.Unix(0, 0), dailyNow.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return len(events)
}

func shellContext(at time.Time, repo, branch string) event.Event {
	return event.Event{At: at, Source: "shell", Kind: event.KindContext, Repo: repo, Branch: branch}
}

func TestStatusShowsRepoTicketAndTier(t *testing.T) {
	deps, st, out := newDailyEnv(t, "")
	mustAppend(t, st,
		shellContext(dailyNow.Add(-5*time.Minute), "/work/api", "ABC-7-fix"),
		event.Event{At: dailyNow.Add(-2 * time.Minute), Source: "presence", Kind: event.KindPresence},
	)
	if err := runStatus(context.Background(), deps); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/work/api", "ABC-7-fix", "ABC-7", "explicit", "Capture:   active", "last seen", "2m0s ago"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("status output lacks %q:\n%s", want, out)
		}
	}
}

func TestStatusReportsPausedAndEmpty(t *testing.T) {
	deps, st, out := newDailyEnv(t, "")
	if err := runStatus(context.Background(), deps); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Nothing recorded yet") {
		t.Fatalf("empty status = %q", out)
	}
	out.Reset()
	mustAppend(t, st, shellContext(dailyNow.Add(-time.Minute), "/work/api", "main"))
	if err := runPause(deps); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runStatus(context.Background(), deps); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "paused") || !strings.Contains(out.String(), "never seen") {
		t.Fatalf("status = %q", out)
	}
}

func TestStatusShowsManualSwitch(t *testing.T) {
	deps, st, out := newDailyEnv(t, "")
	mustAppend(t, st, shellContext(dailyNow.Add(-5*time.Minute), "/work/api", "ABC-7-fix"))
	if err := runSwitch(context.Background(), deps, []string{"proj-12"}, false); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runStatus(context.Background(), deps); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "PROJ-12") || !strings.Contains(out.String(), "manual switch to PROJ-12") {
		t.Fatalf("status = %q", out)
	}
}

func TestTodayListsBlocksAndTotals(t *testing.T) {
	deps, st, out := newDailyEnv(t, "")
	start := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)
	mustAppend(t, st, shellContext(start, "/work/api", "ABC-7-fix"))
	for m := 0; m <= 40; m++ {
		mustAppend(t, st, event.Event{At: start.Add(time.Duration(m) * time.Minute), Source: "presence", Kind: event.KindPresence})
	}
	if err := runToday(context.Background(), deps, ""); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Blocks for 2026-03-02", "08:00-08:40", "ABC-7", "explicit", "Totals:", "Total"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("today output lacks %q:\n%s", want, out)
		}
	}
}

func TestTodayEmptyDayAndBadDay(t *testing.T) {
	deps, _, out := newDailyEnv(t, "")
	if err := runToday(context.Background(), deps, "2026-03-01"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "No time recorded for 2026-03-01") {
		t.Fatalf("out = %q", out)
	}
	if err := runToday(context.Background(), deps, "yesterday"); err == nil {
		t.Fatal("expected error for a malformed --day")
	}
}

func TestSwitchOverridesContextFromNowOn(t *testing.T) {
	deps, st, _ := newDailyEnv(t, "")
	start := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)
	mustAppend(t, st, shellContext(start, "/work/api", "ABC-7-fix"))
	deps.Now = func() time.Time { return start.Add(20 * time.Minute) }
	if err := runSwitch(context.Background(), deps, []string{"proj-12"}, false); err != nil {
		t.Fatal(err)
	}
	// The shell hook keeps writing context events on every prompt.
	mustAppend(t, st, shellContext(start.Add(25*time.Minute), "/work/api", "ABC-7-fix"))
	for m := 0; m <= 40; m++ {
		mustAppend(t, st, event.Event{At: start.Add(time.Duration(m) * time.Minute), Source: "presence", Kind: event.KindPresence})
	}

	events, err := st.Events(context.Background(), start, start.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var manualEvents []event.Event
	for _, e := range events {
		if e.Source == "manual" {
			manualEvents = append(manualEvents, e)
		}
	}
	if len(manualEvents) != 1 || manualEvents[0].Ticket != "PROJ-12" || manualEvents[0].Kind != event.KindContext ||
		manualEvents[0].Repo != "" || manualEvents[0].Branch != "" {
		t.Fatalf("manual events = %+v", manualEvents)
	}

	out := &bytes.Buffer{}
	deps.Out = out
	if err := runToday(context.Background(), deps, "2026-03-02"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "manual switch to PROJ-12") {
		t.Fatalf("manual switch lost after later hook events:\n%s", out)
	}

	// Clearing returns to automatic detection.
	deps.Now = func() time.Time { return start.Add(30 * time.Minute) }
	if err := runSwitch(context.Background(), deps, []string{"auto"}, false); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runStatus(context.Background(), deps); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "ABC-7") || strings.Contains(out.String(), "PROJ-12") {
		t.Fatalf("status after clear:\n%s", out)
	}
}

func TestSwitchValidation(t *testing.T) {
	deps, st, _ := newDailyEnv(t, "")
	tests := []struct {
		name  string
		args  []string
		clear bool
	}{
		{"invalid ticket", []string{"not a ticket"}, false},
		{"no argument", nil, false},
		{"clear with ticket", []string{"ABC-1"}, true},
		{"ticket with trailing text", []string{"ABC-1 extra"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := runSwitch(context.Background(), deps, tt.args, tt.clear); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
	if n := countEvents(t, st); n != 0 {
		t.Fatalf("invalid switches wrote %d events", n)
	}
	if err := runSwitch(context.Background(), deps, nil, true); err != nil {
		t.Fatal(err)
	}
	if n := countEvents(t, st); n != 1 {
		t.Fatalf("--clear wrote %d events, want 1", n)
	}
}

type fakeSampler struct{}

func (fakeSampler) Sample(context.Context) (time.Duration, string, error) {
	return time.Second, "com.example.app", nil
}

func TestPauseStopsAllCollectorsWrites(t *testing.T) {
	deps, st, out := newDailyEnv(t, "")
	ctx := context.Background()

	hook := defaultHookDeps()
	hook.OpenStore = func() (eventSink, error) { return nopCloseStore{st}, nil }
	hook.Git = func(context.Context, string) (string, error) { return "/work/api\nABC-7-fix\n", nil }
	hook.Now = func() time.Time { return dailyNow }
	pausePath, err := paths.PausePath()
	if err != nil {
		t.Fatal(err)
	}
	// Production wiring of the agent; only the clock is replaced.
	agent := newPresenceAgent(fakeSampler{}, st, presence.DefaultInterval, pausePath, nil)
	agent.Now = func() time.Time { return dailyNow }

	if err := runPause(deps); err != nil {
		t.Fatal(err)
	}
	if err := runPause(deps); err != nil {
		t.Fatalf("pause must be idempotent: %v", err)
	}
	if !strings.Contains(out.String(), "paused") {
		t.Errorf("pause output = %q", out)
	}
	if err := runHook(ctx, hook, "/work/api"); err != nil {
		t.Fatal(err)
	}
	agent.Tick(ctx)
	if n := countEvents(t, st); n != 0 {
		t.Fatalf("%d events written while paused", n)
	}

	out.Reset()
	if err := runResume(deps); err != nil {
		t.Fatal(err)
	}
	if err := runResume(deps); err != nil {
		t.Fatalf("resume must be idempotent: %v", err)
	}
	if !strings.Contains(out.String(), "resumed") {
		t.Errorf("resume output = %q", out)
	}
	if err := runHook(ctx, hook, "/work/api"); err != nil {
		t.Fatal(err)
	}
	agent.Tick(ctx)
	if n := countEvents(t, st); n != 3 {
		t.Fatalf("%d events after resume, want 3 (hook context+presence, agent presence)", n)
	}
}

func TestPurgeRequiresConfirmation(t *testing.T) {
	ctx := context.Background()
	seed := func(t *testing.T, input string) (dailyDeps, *store.Store) {
		deps, st, _ := newDailyEnv(t, input)
		mustAppend(t, st, shellContext(dailyNow, "/work/api", "ABC-7"))
		return deps, st
	}

	t.Run("answer n deletes nothing", func(t *testing.T) {
		deps, st := seed(t, "n\n")
		if err := runPurge(ctx, deps, false); err != nil {
			t.Fatal(err)
		}
		if n := countEvents(t, st); n != 1 {
			t.Fatalf("events = %d, want 1", n)
		}
	})
	t.Run("empty answer deletes nothing", func(t *testing.T) {
		deps, st := seed(t, "\n")
		if err := runPurge(ctx, deps, false); err != nil {
			t.Fatal(err)
		}
		if n := countEvents(t, st); n != 1 {
			t.Fatalf("events = %d, want 1", n)
		}
	})
	t.Run("yes flag deletes", func(t *testing.T) {
		deps, st := seed(t, "")
		if err := runPurge(ctx, deps, true); err != nil {
			t.Fatal(err)
		}
		if n := countEvents(t, st); n != 0 {
			t.Fatalf("events = %d, want 0", n)
		}
	})
	t.Run("interactive yes deletes", func(t *testing.T) {
		deps, st := seed(t, "Yes\n")
		if err := runPurge(ctx, deps, false); err != nil {
			t.Fatal(err)
		}
		if n := countEvents(t, st); n != 0 {
			t.Fatalf("events = %d, want 0", n)
		}
	})
	t.Run("EOF without flag aborts with an error", func(t *testing.T) {
		deps, st := seed(t, "")
		err := runPurge(ctx, deps, false)
		if err == nil || !strings.Contains(err.Error(), "--yes") {
			t.Fatalf("err = %v, want a hint about --yes", err)
		}
		if n := countEvents(t, st); n != 1 {
			t.Fatalf("events = %d, want 1", n)
		}
	})
}

func TestDailyCommandsAreRegistered(t *testing.T) {
	root := NewRootCommand()
	for _, name := range []string{"status", "today", "switch", "pause", "resume", "purge"} {
		if cmd, _, err := root.Find([]string{name}); err != nil || cmd.Name() != name {
			t.Errorf("command %q not registered (err=%v)", name, err)
		}
	}
}
