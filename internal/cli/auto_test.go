package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/auto"
	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/review"
	"github.com/JtheGunner/ticktack/internal/sink"
	"github.com/JtheGunner/ticktack/internal/store"
)

// autoTestDeps returns the sync test deps (one explicit ABC-1 item of 60
// minutes on 2026-01-05) with auto mode configured as given.
func autoTestDeps(t *testing.T, enabled bool, trustedDays int) (syncDeps, *recordingSink) {
	t.Helper()
	deps, rec := syncTestDeps(t)
	load := deps.LoadConfig
	deps.LoadConfig = func() (config.Config, string, error) {
		cfg, path, err := load()
		cfg.Auto = config.Auto{Enabled: enabled, TrustedDays: trustedDays}
		cfg.DailyCap = 10 * time.Hour
		return cfg, path, err
	}
	return deps, rec
}

// seedCleanDays stores n clean review records on the n days before 2026-01-05.
func seedCleanDays(t *testing.T, deps syncDeps, n int) {
	t.Helper()
	st, err := deps.OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	day := civil.Date{Year: 2026, Month: 1, Day: 5}
	for i := 1; i <= n; i++ {
		at := time.Date(2026, 1, 5-i, 18, 0, 0, 0, time.UTC)
		rec := store.ReviewRecord{Day: day.AddDays(-i).String(), SessionStart: at, FinishedAt: at, Confirmed: 1}
		if _, err := st.SaveReviewRecord(context.Background(), rec); err != nil {
			t.Fatal(err)
		}
	}
}

func runAutoFor(t *testing.T, deps syncDeps, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := runAuto(context.Background(), &out, deps, "2026-01-05", containsArg(args, "--dry-run"))
	return out.String(), err
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func failOnSink(t *testing.T, deps *syncDeps) {
	t.Helper()
	deps.NewSink = func(config.Config, string) (sink.TimeSink, error) {
		t.Fatal("the sink must not be built")
		return nil, nil
	}
}

func TestAutoModeLockedUntilTrackRecord(t *testing.T) {
	t.Run("N-1 clean days stay locked", func(t *testing.T) {
		deps, rec := autoTestDeps(t, true, 3)
		seedCleanDays(t, deps, 2)
		failOnSink(t, &deps)
		out, err := runAutoFor(t, deps)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"locked", "2 of 3", "Nothing was booked", "ticktack review"} {
			if !strings.Contains(out, want) {
				t.Errorf("output lacks %q:\n%s", want, out)
			}
		}
		if len(rec.calls) != 0 {
			t.Errorf("sink touched while locked: %v", rec.calls)
		}
	})

	t.Run("N clean days unlock", func(t *testing.T) {
		deps, rec := autoTestDeps(t, true, 3)
		seedCleanDays(t, deps, 3)
		out, err := runAutoFor(t, deps)
		if err != nil {
			t.Fatal(err)
		}
		if len(rec.items) != 1 || !strings.Contains(out, "ABC-1") {
			t.Fatalf("items = %v\n%s", rec.items, out)
		}
	})

	t.Run("a correction resets the unlock", func(t *testing.T) {
		deps, rec := autoTestDeps(t, true, 3)
		seedCleanDays(t, deps, 3)
		// A later review of 2026-01-05 discards the block: one correction.
		confirmDay(t, deps, "d 1")
		failOnSink(t, &deps)
		out, err := runAutoFor(t, deps)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "locked") || len(rec.calls) != 0 {
			t.Fatalf("a correction must lock auto mode again:\n%s", out)
		}
	})

	t.Run("reassign, quit, review again, quit leaves the day corrected", func(t *testing.T) {
		deps, rec := autoTestDeps(t, true, 3)
		seedCleanDays(t, deps, 3)
		reviewScripted(t, deps, "t 1 XYZ-9\nq\n")
		reviewScripted(t, deps, "q\n")
		reviewScripted(t, deps, "y\nq\n")
		failOnSink(t, &deps)
		out, err := runAutoFor(t, deps)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "locked") || len(rec.calls) != 0 {
			t.Fatalf("later clean sessions must not unlock auto mode:\n%s", out)
		}
	})

	t.Run("an auto-booked day does not count", func(t *testing.T) {
		deps, _ := autoTestDeps(t, true, 3)
		seedCleanDays(t, deps, 3)
		if _, err := runAutoFor(t, deps); err != nil {
			t.Fatal(err)
		}
		st, err := deps.OpenStore()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = st.Close() }()
		streak, err := review.TrackRecord(context.Background(), st, deps.Now())
		if err != nil || streak != 3 {
			t.Fatalf("streak = %d (err %v), want 3: auto-booking must not add a reviewed day", streak, err)
		}
		confirmed, err := st.Confirmations(context.Background(), "2026-01-05")
		if err != nil || len(confirmed) != 0 {
			t.Fatalf("auto mode must not record confirmations: %v %v", confirmed, err)
		}
	})

	t.Run("disabled config stays locked with a perfect record", func(t *testing.T) {
		deps, rec := autoTestDeps(t, false, 3)
		seedCleanDays(t, deps, 30)
		failOnSink(t, &deps)
		out, err := runAutoFor(t, deps)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "disabled") || len(rec.calls) != 0 {
			t.Fatalf("output:\n%s", out)
		}
	})

	t.Run("review streak unlocks with real reviews", func(t *testing.T) {
		deps, rec := autoTestDeps(t, true, 1)
		confirmDay(t, deps)
		out, err := runAutoFor(t, deps)
		if err != nil {
			t.Fatal(err)
		}
		if len(rec.items) != 1 {
			t.Fatalf("one clean reviewed day must unlock N=1:\n%s", out)
		}
	})
}

func TestAutoModeDryRunDoesNotMutate(t *testing.T) {
	deps, rec := autoTestDeps(t, true, 1)
	seedCleanDays(t, deps, 1)
	out, err := runAutoFor(t, deps, "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range rec.calls {
		if c == "create" || c == "update" || c == "delete" {
			t.Fatalf("dry run called %s", c)
		}
	}
	if !strings.Contains(out, "dry run") {
		t.Errorf("output:\n%s", out)
	}
	st, _ := deps.OpenStore()
	defer func() { _ = st.Close() }()
	if _, found, _ := st.LastDigest(context.Background()); found {
		t.Error("a dry run must not store a digest")
	}
}

// seedLowTierDay replaces the day with one explicit block of ABC-1 followed by
// a learned-rule block that also resolves to ABC-1, plus an explicit block of
// ABC-2.
func seedLowTierDeps(t *testing.T) (syncDeps, *recordingSink) {
	t.Helper()
	deps, rec := autoTestDeps(t, true, 1)
	seedCleanDays(t, deps, 1)
	st, err := deps.OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	start := time.Date(2026, 1, 5, 14, 0, 0, 0, time.UTC)
	if _, err := st.AddLearnedRule(ctx, store.LearnedRule{Repo: "/work/app", Ticket: "ABC-1", CreatedAt: start}); err != nil {
		t.Fatal(err)
	}
	// 14:00-14:30 ABC-2 explicit, 14:30-15:00 /work/app (learned, resolves to ABC-1).
	events := []event.Event{
		{At: start, Source: "test", Kind: event.KindContext, Ticket: "ABC-2"},
		{At: start.Add(30 * time.Minute), Source: "test", Kind: event.KindContext, Repo: "/work/app", Branch: "main"},
	}
	for _, e := range events {
		if err := st.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i <= 60; i++ {
		if err := st.Append(ctx, event.Event{At: start.Add(time.Duration(i) * time.Minute), Source: "test", Kind: event.KindPresence}); err != nil {
			t.Fatal(err)
		}
	}
	return deps, rec
}

func TestAutoModeSkipsLowTierBlocks(t *testing.T) {
	deps, rec := seedLowTierDeps(t)
	out, err := runAutoFor(t, deps)
	if err != nil {
		t.Fatal(err)
	}
	// ABC-2 is explicit only; ABC-1 has one explicit and one learned block and
	// is skipped as a whole.
	if _, ok := rec.items["ABC-2/1"]; !ok || len(rec.items) != 1 {
		t.Fatalf("booked items = %v\n%s", rec.items, out)
	}
	var skipLine string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "skipped") && strings.Contains(line, "ABC-1") {
			skipLine = line
		}
	}
	if skipLine == "" || !strings.Contains(skipLine, "learned") || !strings.Contains(skipLine, "needs review") {
		t.Fatalf("ABC-1 must be listed as skipped with its tier:\n%s", out)
	}
}

func TestAutoModeRespectsDailyCap(t *testing.T) {
	deps, rec := autoTestDeps(t, true, 1)
	load := deps.LoadConfig
	deps.LoadConfig = func() (config.Config, string, error) {
		cfg, path, err := load()
		cfg.DailyCap = 30 * time.Minute // the seeded day has 60 minutes
		return cfg, path, err
	}
	seedCleanDays(t, deps, 1)
	failOnSink(t, &deps)
	out, err := runAutoFor(t, deps)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != 0 || !strings.Contains(out, "daily cap") || !strings.Contains(out, "nothing was booked") {
		t.Fatalf("a capped day must not be booked:\n%s", out)
	}
}

func TestDigestListsEveryBookedItem(t *testing.T) {
	deps, rec := seedLowTierDeps(t)
	out, err := runAutoFor(t, deps)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.items) == 0 {
		t.Fatal("nothing booked")
	}
	for id, w := range rec.items {
		line := lineWith(out, w.Issue)
		for _, want := range []string{"created", w.Issue, w.Date.String(), "30 min", "tier explicit"} {
			if !strings.Contains(line, want) {
				t.Errorf("digest line for %s (%s) lacks %q: %q", w.Issue, id, want, line)
			}
		}
	}
	for _, want := range []string{"Totals: created 1", "skipped 1", "ticktack undo"} {
		if !strings.Contains(out, want) {
			t.Errorf("digest lacks %q:\n%s", want, out)
		}
	}
	// The digest is stored and can be shown again.
	var again bytes.Buffer
	if err := runDigest(context.Background(), &again, deps); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(again.String()) != strings.TrimSpace(out) {
		t.Errorf("stored digest differs:\n%s\nvs\n%s", again.String(), out)
	}
}

func lineWith(text, needle string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, needle) && !strings.HasPrefix(line, "skipped") {
			return line
		}
	}
	return ""
}

func TestDigestWithoutAutoRun(t *testing.T) {
	deps, _ := autoTestDeps(t, true, 1)
	var out bytes.Buffer
	if err := runDigest(context.Background(), &out, deps); err != nil || !strings.Contains(out.String(), "No digest yet") {
		t.Fatalf("out = %q err %v", out.String(), err)
	}
}

func TestAutoDigestSanitizesExternalText(t *testing.T) {
	d := auto.Digest{Day: civil.Date{Year: 2026, Month: 1, Day: 5}, Entries: []auto.DigestEntry{
		{Ticket: "ABC-1\x1b[31m", Date: civil.Date{Year: 2026, Month: 1, Day: 5}, Minutes: 15, Status: "failed", Reason: "bad\nreason\x1b]0;x\x07"},
	}}
	text := renderDigest(d)
	if strings.ContainsAny(text, "\x1b\x07") || strings.Count(text, "\n") != 2 {
		t.Fatalf("unsanitised digest: %q", text)
	}
}

func TestAutoUndoRevertsLastAutoRun(t *testing.T) {
	deps, rec := autoTestDeps(t, true, 1)
	seedCleanDays(t, deps, 1)
	if _, err := runAutoFor(t, deps); err != nil {
		t.Fatal(err)
	}
	if len(rec.items) != 1 {
		t.Fatalf("items = %v", rec.items)
	}
	var out bytes.Buffer
	if err := runUndo(context.Background(), &out, deps); err != nil {
		t.Fatal(err)
	}
	if len(rec.items) != 0 {
		t.Fatalf("undo left items: %v", rec.items)
	}
}

func TestAutoCommandIsRegisteredAndNeedsConfig(t *testing.T) {
	root := NewRootCommand()
	for _, name := range []string{"auto", "digest"} {
		if c, _, err := root.Find([]string{name}); err != nil || c.Name() != name {
			t.Errorf("command %s not registered: %v", name, err)
		}
	}
	deps, _ := autoTestDeps(t, true, 1)
	deps.LoadConfig = func() (config.Config, string, error) { return config.Config{}, "", errors.New("no config") }
	if err := runAuto(context.Background(), &bytes.Buffer{}, deps, "", false); err == nil {
		t.Error("missing config must be an error")
	}
}

// reviewScripted runs one review session of 2026-01-05 with the given input.
func reviewScripted(t *testing.T, deps syncDeps, input string) {
	t.Helper()
	st, err := deps.OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	cfg, _, _ := deps.LoadConfig()
	rd := reviewDeps{
		OpenStore:  func() (reviewStore, error) { return nopCloseStore{st}, nil },
		LoadConfig: func() (config.Config, error) { return cfg, nil },
		Now:        deps.Now,
		In:         strings.NewReader(input),
		Out:        &bytes.Buffer{},
	}
	if err := runReview(context.Background(), rd, "2026-01-05"); err != nil {
		t.Fatal(err)
	}
}
