package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
	"github.com/JtheGunner/ticktack/internal/sink"
	"github.com/JtheGunner/ticktack/internal/store"
)

// autopilotDeps returns the auto test deps switched to the autopilot with the
// given lookback, and a clock on 2026-01-06 so that the seeded 2026-01-05 is a
// completed day. No day is reviewed: the autopilot has no trust gate.
func autopilotDeps(t *testing.T, lookback int) (syncDeps, *recordingSink) {
	t.Helper()
	deps, rec := autoTestDeps(t, true, 10)
	return asAutopilot(deps, lookback), rec
}

func asAutopilot(deps syncDeps, lookback int) syncDeps {
	load := deps.LoadConfig
	deps.LoadConfig = func() (config.Config, string, error) {
		cfg, path, err := load()
		cfg.Auto.Mode = config.AutoModeAutopilot
		cfg.Auto.AutopilotLookbackDays = lookback
		return cfg, path, err
	}
	deps.Now = func() time.Time { return time.Date(2026, 1, 6, 20, 0, 0, 0, time.UTC) }
	return deps
}

// seedHour gives day an explicit hour for ticket.
func seedHour(t *testing.T, deps syncDeps, day civil.Date, ticket string) {
	t.Helper()
	ctx := context.Background()
	st, err := deps.OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	start := time.Date(day.Year, day.Month, day.Day, 9, 0, 0, 0, time.UTC)
	if err := st.Append(ctx, event.Event{At: start, Source: "test", Kind: event.KindContext, Ticket: ticket}); err != nil {
		t.Fatal(err)
	}
	for m := 0; m <= 60; m++ {
		if err := st.Append(ctx, event.Event{At: start.Add(time.Duration(m) * time.Minute), Source: "test", Kind: event.KindPresence}); err != nil {
			t.Fatal(err)
		}
	}
}

func withStore(t *testing.T, deps syncDeps, fn func(ctx context.Context, st *store.Store)) {
	t.Helper()
	st, err := deps.OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	fn(context.Background(), st)
}

func runAutopilotByHand(t *testing.T, deps syncDeps, dayFlag string, dryRun bool) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := runAuto(context.Background(), &out, deps, dayFlag, dryRun)
	return out.String(), err
}

func countCalls(rec *recordingSink, call string) int {
	n := 0
	for _, c := range rec.calls {
		if c == call {
			n++
		}
	}
	return n
}

func TestAutopilotBooksACompletedDayWithoutATrustGate(t *testing.T) {
	deps, rec := autopilotDeps(t, 7)
	out, err := runAutopilotByHand(t, deps, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rec.items["ABC-1/1"]; !ok || len(rec.items) != 1 {
		t.Fatalf("items = %v\n%s", rec.items, out)
	}
	withStore(t, deps, func(ctx context.Context, st *store.Store) {
		if confirmed, err := st.ConfirmedDays(ctx); err != nil || len(confirmed) != 0 {
			t.Errorf("the autopilot must record no confirmations: %v %v", confirmed, err)
		}
		if d, found, err := st.LastDigest(ctx); err != nil || !found || !strings.Contains(d.Text, "ABC-1") {
			t.Errorf("digest = %+v %v %v", d, found, err)
		}
	})
}

func TestAutopilotRerunOfItsOwnDayIsANoOp(t *testing.T) {
	deps, rec := autopilotDeps(t, 7)
	if _, err := runAutopilotByHand(t, deps, "", false); err != nil {
		t.Fatal(err)
	}
	creates, updates := countCalls(rec, "create"), countCalls(rec, "update")
	if _, err := runAutopilotByHand(t, deps, "", false); err != nil {
		t.Fatal(err)
	}
	if countCalls(rec, "create") != creates || countCalls(rec, "update") != updates || len(rec.items) != 1 {
		t.Fatalf("a second pass changed the booking: calls = %v", rec.calls)
	}
	withStore(t, deps, func(ctx context.Context, st *store.Store) {
		// Only the first pass changed something, so only it wrote a digest.
		last, _, _ := st.LastDigest(ctx)
		if last.ID != 1 {
			t.Errorf("an idle pass stored a digest (id %d)", last.ID)
		}
	})
}

func TestAutopilotNeverBooksTodayOrAFutureDay(t *testing.T) {
	deps, _ := autopilotDeps(t, 7)
	deps.Now = func() time.Time { return time.Date(2026, 1, 5, 20, 0, 0, 0, time.UTC) }
	failOnSink(t, &deps)
	if out, err := runAutopilotByHand(t, deps, "", false); err != nil || !strings.Contains(out, "nothing to book") {
		t.Fatalf("pass on the day itself: %v\n%s", err, out)
	}
	for _, day := range []string{"2026-01-05", "2026-01-09"} {
		if _, err := runAutopilotByHand(t, deps, day, false); err == nil || !strings.Contains(err.Error(), "not a completed day") {
			t.Errorf("--day %s: err = %v", day, err)
		}
	}
}

func TestAutopilotRespectsTheLookbackWindow(t *testing.T) {
	tests := []struct {
		name     string
		lookback int
		want     []string
	}{
		{"one day", 1, []string{"ABC-1"}},
		{"two days", 2, []string{"DAY-4", "ABC-1"}},
		{"wide enough for all", 7, []string{"DAY-2", "DAY-4", "ABC-1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps, rec := autopilotDeps(t, tt.lookback)
			// The seeded 2026-01-05 is ABC-1; the other days get their own ticket.
			seedHour(t, deps, civil.Date{Year: 2026, Month: 1, Day: 4}, "DAY-4")
			seedHour(t, deps, civil.Date{Year: 2026, Month: 1, Day: 2}, "DAY-2")
			// Today has time too and must stay untouched.
			seedHour(t, deps, civil.Date{Year: 2026, Month: 1, Day: 6}, "DAY-6")
			if _, err := runAutopilotByHand(t, deps, "", false); err != nil {
				t.Fatal(err)
			}
			var booked []string
			for _, key := range []string{"DAY-2", "DAY-4", "ABC-1", "DAY-6"} {
				if _, ok := rec.items[key+"/1"]; ok {
					booked = append(booked, key)
				}
			}
			if strings.Join(booked, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("booked %v, want %v", booked, tt.want)
			}
		})
	}
}

func TestAutopilotLeavesDaysOfTheUserAlone(t *testing.T) {
	item := func(day string) store.Confirmation {
		return store.Confirmation{Day: day, Issue: "ABC-1", Type: sessionizer.DefaultWorkItemType, Minutes: 60, ConfirmedAt: time.Date(2026, 1, 5, 21, 0, 0, 0, time.UTC)}
	}
	tests := []struct {
		name       string
		prepare    func(ctx context.Context, t *testing.T, st *store.Store)
		wantBooked bool
	}{
		{"a day confirmed in review", func(ctx context.Context, t *testing.T, st *store.Store) {
			if err := st.Confirm(ctx, item("2026-01-05")); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"a day with a correction in review", func(ctx context.Context, t *testing.T, st *store.Store) {
			r := store.ReviewRecord{Day: "2026-01-05", SessionStart: time.Date(2026, 1, 5, 21, 0, 0, 0, time.UTC), FinishedAt: time.Date(2026, 1, 5, 21, 5, 0, 0, time.UTC), Corrections: 1}
			if _, err := st.SaveReviewRecord(ctx, r); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"a day a review session only looked at", func(ctx context.Context, t *testing.T, st *store.Store) {
			r := store.ReviewRecord{Day: "2026-01-05", SessionStart: time.Date(2026, 1, 5, 21, 0, 0, 0, time.UTC), FinishedAt: time.Date(2026, 1, 5, 21, 5, 0, 0, time.UTC)}
			if _, err := st.SaveReviewRecord(ctx, r); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"a confirmation on another day", func(ctx context.Context, t *testing.T, st *store.Store) {
			if err := st.Confirm(ctx, item("2026-01-04")); err != nil {
				t.Fatal(err)
			}
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps, rec := autopilotDeps(t, 7)
			withStore(t, deps, func(ctx context.Context, st *store.Store) { tt.prepare(ctx, t, st) })
			if _, err := runAutopilotByHand(t, deps, "", false); err != nil {
				t.Fatal(err)
			}
			if _, booked := rec.items["ABC-1/1"]; booked != tt.wantBooked {
				t.Fatalf("booked = %v, want %v (calls %v)", booked, tt.wantBooked, rec.calls)
			}
		})
	}
}

func TestAutopilotCorrectionInReviewDoesNotLockItForOtherDays(t *testing.T) {
	deps, rec := autopilotDeps(t, 7)
	seedHour(t, deps, civil.Date{Year: 2026, Month: 1, Day: 4}, "DAY-4")
	withStore(t, deps, func(ctx context.Context, st *store.Store) {
		r := store.ReviewRecord{Day: "2026-01-04", SessionStart: time.Date(2026, 1, 4, 21, 0, 0, 0, time.UTC), FinishedAt: time.Date(2026, 1, 4, 21, 5, 0, 0, time.UTC), Corrections: 2}
		if _, err := st.SaveReviewRecord(ctx, r); err != nil {
			t.Fatal(err)
		}
	})
	if _, err := runAutopilotByHand(t, deps, "", false); err != nil {
		t.Fatal(err)
	}
	if _, ok := rec.items["ABC-1/1"]; !ok || len(rec.items) != 1 {
		t.Fatalf("items = %v", rec.items)
	}
}

func TestAutopilotLeavesALowerTierItemForReview(t *testing.T) {
	deps, rec := seedLowTierDeps(t)
	deps = asAutopilot(deps, 7)
	out, err := runAutopilotByHand(t, deps, "", false)
	if err != nil {
		t.Fatal(err)
	}
	// ABC-2 is explicit only; ABC-1 mixes an explicit and a learned block and
	// is skipped as a whole.
	if _, ok := rec.items["ABC-2/1"]; !ok || len(rec.items) != 1 {
		t.Fatalf("booked items = %v\n%s", rec.items, out)
	}
	if !strings.Contains(out, "needs review") {
		t.Errorf("the skipped item must be listed with its reason:\n%s", out)
	}
}

// failingSink fails the creation of the tickets in fail.
type failingSink struct {
	*recordingSink
	mu   sync.Mutex
	fail map[string]bool
	// msg is the failure text; empty means "youtrack unreachable".
	msg string
}

func (f *failingSink) Create(ctx context.Context, w sink.WorkItem) (string, error) {
	f.mu.Lock()
	failing, msg := f.fail[w.Issue], f.msg
	f.mu.Unlock()
	if failing {
		if msg == "" {
			msg = "youtrack unreachable"
		}
		return "", errors.New(msg)
	}
	return f.recordingSink.Create(ctx, w)
}

func TestAutopilotErrorOnOneDayDoesNotStopOthersAndIsRetried(t *testing.T) {
	deps, rec := autopilotDeps(t, 7)
	// 2026-01-04 (DAY-4) is processed first and fails; 2026-01-05 is ABC-1.
	seedHour(t, deps, civil.Date{Year: 2026, Month: 1, Day: 4}, "DAY-4")
	flaky := &failingSink{recordingSink: rec, fail: map[string]bool{"DAY-4": true}}
	deps.NewSink = func(config.Config, string) (sink.TimeSink, error) { return flaky, nil }

	_, err := runAutopilotByHand(t, deps, "", false)
	if err == nil || !strings.Contains(err.Error(), "youtrack unreachable") {
		t.Fatalf("err = %v, want the failure reported", err)
	}
	if _, ok := rec.items["ABC-1/1"]; !ok {
		t.Errorf("the healthy day must still be booked: %v", rec.items)
	}
	if _, ok := rec.items["DAY-4/1"]; ok {
		t.Errorf("nothing of the failed day may be booked: %v", rec.items)
	}
	withStore(t, deps, func(ctx context.Context, st *store.Store) {
		rows, err := st.LedgerRowsForDate(ctx, "2026-01-04")
		if err != nil || len(rows) != 0 {
			t.Errorf("the failed day left ledger rows: %v %v", rows, err)
		}
	})

	flaky.mu.Lock()
	flaky.fail = nil
	flaky.mu.Unlock()
	if _, err := runAutopilotByHand(t, deps, "", false); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if len(rec.items) != 2 || countCalls(rec, "create") != 2 {
		t.Fatalf("after the retry items = %v, calls = %v: want each booked exactly once", rec.items, rec.calls)
	}
}

func TestAutopilotUnavailableSinkIsReportedOnceAndBooksNothing(t *testing.T) {
	deps, rec := autopilotDeps(t, 7)
	seedHour(t, deps, civil.Date{Year: 2026, Month: 1, Day: 4}, "DAY-4")
	builds := 0
	deps.NewSink = func(config.Config, string) (sink.TimeSink, error) {
		builds++
		return nil, errors.New(msgNoToken)
	}
	_, err := runAutopilotByHand(t, deps, "", false)
	if err == nil || strings.Count(err.Error(), msgNoToken) != 1 {
		t.Fatalf("err = %v, want the token error once", err)
	}
	if builds != 1 || len(rec.calls) != 0 {
		t.Fatalf("builds = %d, calls = %v", builds, rec.calls)
	}
}

func heldDays(t *testing.T, deps syncDeps) []string {
	t.Helper()
	var held []string
	withStore(t, deps, func(ctx context.Context, st *store.Store) {
		var err error
		if held, err = st.HeldDays(ctx); err != nil {
			t.Fatal(err)
		}
	})
	return held
}

func TestAutopilotUndoRevertsItsRunAndHoldsTheDay(t *testing.T) {
	deps, rec := autopilotDeps(t, 7)
	if _, err := runAutopilotByHand(t, deps, "", false); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runUndo(context.Background(), &out, deps); err != nil {
		t.Fatal(err)
	}
	if len(rec.items) != 0 {
		t.Fatalf("undo left items: %v", rec.items)
	}
	if held := heldDays(t, deps); len(held) != 1 || held[0] != "2026-01-05" {
		t.Fatalf("held = %v, want the undone day", held)
	}
	if !strings.Contains(out.String(), "held 2026-01-05") || !strings.Contains(out.String(), "auto --day") {
		t.Errorf("undo must name the held day and how to release it:\n%s", out.String())
	}
}

func TestAutopilotSkipsAHeldDayAndBooksTheOthers(t *testing.T) {
	deps, rec := autopilotDeps(t, 7)
	seedHour(t, deps, civil.Date{Year: 2026, Month: 1, Day: 4}, "DAY-4")
	// The undone run is the last one: the pass books 01-04 first, then 01-05.
	if _, err := runAutopilotByHand(t, deps, "", false); err != nil {
		t.Fatal(err)
	}
	if err := runUndo(context.Background(), &bytes.Buffer{}, deps); err != nil {
		t.Fatal(err)
	}
	if _, ok := rec.items["ABC-1/1"]; ok || len(heldDays(t, deps)) != 1 {
		t.Fatalf("expected ABC-1 undone and held: %v, held %v", rec.items, heldDays(t, deps))
	}
	// A later pass leaves the held day alone, names it, and books new time.
	seedHour(t, deps, civil.Date{Year: 2026, Month: 1, Day: 3}, "DAY-3")
	out, err := runAutopilotByHand(t, deps, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rec.items["ABC-1/1"]; ok {
		t.Fatalf("the autopilot rebooked a held day: %v", rec.items)
	}
	if _, ok := rec.items["DAY-3/1"]; !ok {
		t.Errorf("other days must still be booked: %v", rec.items)
	}
	if !strings.Contains(out, "held (undone by you)") || !strings.Contains(out, "2026-01-05") {
		t.Errorf("the held day must be reported:\n%s", out)
	}
}

func TestAutopilotHoldIsLogged(t *testing.T) {
	deps, _ := autopilotDeps(t, 7)
	withStore(t, deps, func(ctx context.Context, st *store.Store) {
		if err := st.HoldDay(ctx, "2026-01-05", deps.Now()); err != nil {
			t.Fatal(err)
		}
	})
	logs := strings.Join(runAutopilotJobTwice(t, deps, false), "\n")
	if !strings.Contains(logs, "held (undone by you)") {
		t.Fatalf("logs = %q", logs)
	}
}

func TestAutoDayReleasesTheHoldAndBooksTheDay(t *testing.T) {
	deps, rec := autopilotDeps(t, 7)
	withStore(t, deps, func(ctx context.Context, st *store.Store) {
		if err := st.HoldDay(ctx, "2026-01-05", deps.Now()); err != nil {
			t.Fatal(err)
		}
	})
	// A dry run changes nothing, so the hold stays.
	if _, err := runAutopilotByHand(t, deps, "2026-01-05", true); err != nil {
		t.Fatal(err)
	}
	if held := heldDays(t, deps); len(held) != 1 {
		t.Fatalf("a dry run lifted the hold: %v", held)
	}
	if _, err := runAutopilotByHand(t, deps, "2026-01-05", false); err != nil {
		t.Fatal(err)
	}
	if held := heldDays(t, deps); len(held) != 0 {
		t.Fatalf("held = %v, want the hold lifted", held)
	}
	if _, ok := rec.items["ABC-1/1"]; !ok {
		t.Fatalf("the day was not booked again: %v", rec.items)
	}
	// With the hold gone the autopilot keeps maintaining the day.
	if _, err := runAutopilotByHand(t, deps, "", false); err != nil {
		t.Fatal(err)
	}
	if len(rec.items) != 1 {
		t.Fatalf("items = %v", rec.items)
	}
}

func TestAHeldDayThatYouConfirmedIsSimplyYours(t *testing.T) {
	deps, rec := autopilotDeps(t, 7)
	withStore(t, deps, func(ctx context.Context, st *store.Store) {
		if err := st.HoldDay(ctx, "2026-01-05", deps.Now()); err != nil {
			t.Fatal(err)
		}
		c := store.Confirmation{Day: "2026-01-05", Issue: "ABC-1", Type: sessionizer.DefaultWorkItemType, Minutes: 60, ConfirmedAt: deps.Now()}
		if err := st.Confirm(ctx, c); err != nil {
			t.Fatal(err)
		}
	})
	out, err := runAutopilotByHand(t, deps, "", false)
	if err != nil || len(rec.items) != 0 || strings.Contains(out, "held (undone") {
		t.Fatalf("err = %v, items = %v\n%s", err, rec.items, out)
	}
}

func TestUndoHoldsNothingOutsideAutopilotOrForOpenDays(t *testing.T) {
	t.Run("gated mode", func(t *testing.T) {
		deps, _ := autoTestDeps(t, true, 1)
		seedCleanDays(t, deps, 1)
		deps.Now = func() time.Time { return time.Date(2026, 1, 6, 20, 0, 0, 0, time.UTC) }
		if _, err := runAutoFor(t, deps); err != nil {
			t.Fatal(err)
		}
		if err := runUndo(context.Background(), &bytes.Buffer{}, deps); err != nil {
			t.Fatal(err)
		}
		if held := heldDays(t, deps); len(held) != 0 {
			t.Fatalf("gated undo held %v", held)
		}
	})
	t.Run("a run of today", func(t *testing.T) {
		deps, _ := autoTestDeps(t, true, 1)
		seedCleanDays(t, deps, 1)
		if _, err := runAutoFor(t, deps); err != nil {
			t.Fatal(err)
		}
		deps = asAutopilot(deps, 7)
		deps.Now = func() time.Time { return time.Date(2026, 1, 5, 20, 0, 0, 0, time.UTC) }
		if err := runUndo(context.Background(), &bytes.Buffer{}, deps); err != nil {
			t.Fatal(err)
		}
		if held := heldDays(t, deps); len(held) != 0 {
			t.Fatalf("undo of an open day held %v", held)
		}
	})
}

func TestAutopilotExplicitDayBooksThatCompletedDay(t *testing.T) {
	deps, rec := autopilotDeps(t, 1)
	if _, err := runAutopilotByHand(t, deps, "2026-01-05", false); err != nil {
		t.Fatal(err)
	}
	if len(rec.items) != 1 {
		t.Fatalf("items = %v", rec.items)
	}
}

func TestAutopilotDryRunBooksNothing(t *testing.T) {
	deps, rec := autopilotDeps(t, 7)
	if _, err := runAutopilotByHand(t, deps, "", true); err != nil {
		t.Fatal(err)
	}
	if len(rec.items) != 0 {
		t.Fatalf("dry run booked %v", rec.items)
	}
	withStore(t, deps, func(ctx context.Context, st *store.Store) {
		if _, found, _ := st.LastDigest(ctx); found {
			t.Error("a dry run must not store a digest")
		}
	})
}

func TestModeAutopilotWithoutEnabledStaysInTheGatedFlow(t *testing.T) {
	deps, rec := autoTestDeps(t, false, 3)
	deps = asAutopilot(deps, 7)
	failOnSink(t, &deps)
	out, err := runAutopilotByHand(t, deps, "2026-01-05", false)
	if err != nil || !strings.Contains(out, "disabled") || len(rec.calls) != 0 {
		t.Fatalf("err = %v\n%s", err, out)
	}
}

func TestGatedModeStillNeedsTheTrackRecordForACompletedDay(t *testing.T) {
	deps, rec := autoTestDeps(t, true, 3)
	load := deps.LoadConfig
	deps.LoadConfig = func() (config.Config, string, error) {
		cfg, path, err := load()
		cfg.Auto.Mode = config.AutoModeGated
		return cfg, path, err
	}
	deps.Now = func() time.Time { return time.Date(2026, 1, 6, 20, 0, 0, 0, time.UTC) }
	failOnSink(t, &deps)
	out, err := runAutopilotByHand(t, deps, "2026-01-05", false)
	if err != nil || !strings.Contains(out, "locked") || len(rec.calls) != 0 {
		t.Fatalf("err = %v\n%s", err, out)
	}
}

// runAutopilotJobTwice runs the agent job until it ran twice (the second
// pause check proves the first run is over) and returns what it logged.
func runAutopilotJobTwice(t *testing.T, deps syncDeps, paused bool) []string {
	t.Helper()
	var mu sync.Mutex
	var logs []string
	logf := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		logs = append(logs, strings.TrimSpace(strings.ReplaceAll(format, "%v", "<err>")))
		for _, a := range args {
			switch v := a.(type) {
			case error:
				logs = append(logs, v.Error())
			case string:
				logs = append(logs, v)
			}
		}
	}
	checks := make(chan struct{}, 10)
	pausedCheck := func() bool {
		checks <- struct{}{}
		return paused
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		newAutopilotJob(deps, pausedCheck, time.Millisecond, 5*time.Millisecond, logf)(ctx)
	}()
	<-checks
	<-checks
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	return append([]string(nil), logs...)
}

func TestAutopilotJobRunsWhenActiveAndRereadsTheConfig(t *testing.T) {
	deps, rec := autopilotDeps(t, 7)
	loads := 0
	load := deps.LoadConfig
	deps.LoadConfig = func() (config.Config, string, error) {
		loads++
		return load()
	}
	if logs := runAutopilotJobTwice(t, deps, false); len(logs) != 0 {
		t.Fatalf("logs = %v", logs)
	}
	if len(rec.items) != 1 || loads < 2 {
		t.Fatalf("items = %v, config loads = %d", rec.items, loads)
	}
}

func TestAutopilotJobDoesNothingWhilePaused(t *testing.T) {
	deps, _ := autopilotDeps(t, 7)
	deps.LoadConfig = func() (config.Config, string, error) {
		t.Error("a paused run must not even read the config")
		return config.Config{}, "", nil
	}
	failOnSink(t, &deps)
	if logs := runAutopilotJobTwice(t, deps, true); len(logs) != 0 {
		t.Fatalf("logs = %v", logs)
	}
}

func TestAutopilotJobDoesNothingUnlessActive(t *testing.T) {
	tests := []struct {
		name string
		auto config.Auto
	}{
		{"gated", config.Auto{Enabled: true, Mode: config.AutoModeGated}},
		{"autopilot mode but disabled", config.Auto{Enabled: false, Mode: config.AutoModeAutopilot}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps, _ := autopilotDeps(t, 7)
			load := deps.LoadConfig
			deps.LoadConfig = func() (config.Config, string, error) {
				cfg, path, err := load()
				cfg.Auto = tt.auto
				return cfg, path, err
			}
			deps.AcquireLock = func() (func(), error) {
				t.Error("an inactive autopilot must not take the booking lock")
				return func() {}, nil
			}
			failOnSink(t, &deps)
			if logs := runAutopilotJobTwice(t, deps, false); len(logs) != 0 {
				t.Fatalf("logs = %v", logs)
			}
		})
	}
}

func TestAutopilotJobLogsFailuresOncePerRunWithoutBooking(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(deps *syncDeps)
		want    string
	}{
		{"config not loadable", func(deps *syncDeps) {
			deps.LoadConfig = func() (config.Config, string, error) { return config.Config{}, "", errors.New("bad toml") }
		}, "bad toml"},
		{"token missing", func(deps *syncDeps) {
			deps.NewSink = func(config.Config, string) (sink.TimeSink, error) { return nil, errors.New(msgNoToken) }
		}, msgNoToken},
		{"another booking run is active", func(deps *syncDeps) {
			deps.AcquireLock = func() (func(), error) { return nil, ErrBookingRunActive }
		}, "another booking run is active"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps, rec := autopilotDeps(t, 7)
			tt.prepare(&deps)
			logs := strings.Join(runAutopilotJobTwice(t, deps, false), "\n")
			if !strings.Contains(logs, tt.want) || !strings.Contains(logs, "autopilot:") {
				t.Fatalf("logs = %q, want a line with %q", logs, tt.want)
			}
			if len(rec.items) != 0 {
				t.Fatalf("booked %v", rec.items)
			}
		})
	}
}

func digestIDOfDay(t *testing.T, deps syncDeps, day string) int64 {
	t.Helper()
	var id int64
	withStore(t, deps, func(ctx context.Context, st *store.Store) {
		d, _, err := st.LastDigestForDay(ctx, day)
		if err != nil {
			t.Fatal(err)
		}
		id = d.ID
	})
	return id
}

func TestAutopilotStoresARepeatedIdenticalFailureOnce(t *testing.T) {
	deps, rec := autopilotDeps(t, 7)
	seedHour(t, deps, civil.Date{Year: 2026, Month: 1, Day: 4}, "DAY-4")
	flaky := &failingSink{recordingSink: rec, fail: map[string]bool{"DAY-4": true}}
	deps.NewSink = func(config.Config, string) (sink.TimeSink, error) { return flaky, nil }
	const failedDay = "2026-01-04"

	if _, err := runAutopilotByHand(t, deps, "", false); err == nil {
		t.Fatal("the first run must report the failure")
	}
	first := digestIDOfDay(t, deps, failedDay)
	if first == 0 {
		t.Fatal("the first failure must be stored")
	}
	for i := 0; i < 2; i++ {
		if _, err := runAutopilotByHand(t, deps, "", false); err == nil {
			t.Fatal("the failure must still be reported every run")
		}
	}
	if got := digestIDOfDay(t, deps, failedDay); got != first {
		t.Fatalf("a repeated identical failure stored digest %d after %d", got, first)
	}

	// A changed failure is new content.
	flaky.mu.Lock()
	flaky.msg = "youtrack answered HTTP 503"
	flaky.mu.Unlock()
	if _, err := runAutopilotByHand(t, deps, "", false); err == nil {
		t.Fatal("the failure must be reported")
	}
	changed := digestIDOfDay(t, deps, failedDay)
	if changed == first {
		t.Fatal("a changed failure must write a new digest")
	}

	// A later success is news too.
	flaky.mu.Lock()
	flaky.fail = nil
	flaky.mu.Unlock()
	if _, err := runAutopilotByHand(t, deps, "", false); err != nil {
		t.Fatal(err)
	}
	if got := digestIDOfDay(t, deps, failedDay); got == changed {
		t.Fatal("the success after the failure must write a new digest")
	}
}

func TestAutopilotJobSanitizesLoggedErrors(t *testing.T) {
	deps, _ := autopilotDeps(t, 7)
	deps.LoadConfig = func() (config.Config, string, error) {
		return config.Config{}, "", errors.New("bad\x1b[31m toml\nforged: line\r")
	}
	logs := strings.Join(runAutopilotJobTwice(t, deps, false), "\n")
	if !strings.Contains(logs, "bad") || strings.ContainsAny(logs, "\x1b\r") || strings.Contains(logs, "toml\nforged") {
		t.Fatalf("logs = %q: control characters must be removed", logs)
	}

	deps, _ = autopilotDeps(t, 7)
	deps.NewSink = func(config.Config, string) (sink.TimeSink, error) {
		return nil, errors.New("boom\x1b[2J\nforged: line")
	}
	logs = strings.Join(runAutopilotJobTwice(t, deps, false), "\n")
	if !strings.Contains(logs, "boom") || strings.ContainsAny(logs, "\x1b") || strings.Contains(logs, "boom\x1b") || strings.Contains(logs, "\nforged") {
		t.Fatalf("logs = %q: control characters must be removed", logs)
	}
}
