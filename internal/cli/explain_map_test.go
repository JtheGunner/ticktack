package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/review"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
	"github.com/JtheGunner/ticktack/internal/store"
)

func seedPresence(t *testing.T, st *store.Store, start time.Time, minutes int) {
	t.Helper()
	for m := 0; m <= minutes; m++ {
		mustAppend(t, st, event.Event{At: start.Add(time.Duration(m) * time.Minute), Source: "presence", Kind: event.KindPresence})
	}
}

func runExplainOut(t *testing.T, deps dailyDeps, out *bytes.Buffer, target string) string {
	t.Helper()
	out.Reset()
	if err := runExplain(context.Background(), deps, "", target); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestExplainNamesDecidingSignal(t *testing.T) {
	deps, st, out := newDailyEnv(t, "")
	day := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)
	ctx := context.Background()
	if _, err := st.AddLearnedRule(ctx, store.LearnedRule{Repo: "/work/api", BranchPattern: "dev", Ticket: "LRN-5", CreatedAt: day}); err != nil {
		t.Fatal(err)
	}
	// Block 1 (08:00-08:30): ticket in branch. Block 2 (09:00-09:30): learned rule.
	mustAppend(t, st, shellContext(day, "/work/api", "ABC-7-fix"), shellContext(day.Add(time.Hour), "/work/api", "dev"))
	seedPresence(t, st, day, 30)
	seedPresence(t, st, day.Add(time.Hour), 30)

	first := runExplainOut(t, deps, out, "1")
	for _, want := range []string{"Block 1", "08:00-08:30", "ABC-7", "explicit", "shell", "/work/api", "ABC-7-fix", "found in the branch name", "Presence:"} {
		if !strings.Contains(first, want) {
			t.Errorf("explain 1 lacks %q:\n%s", want, first)
		}
	}
	second := runExplainOut(t, deps, out, "09:10")
	for _, want := range []string{"Block 2", "LRN-5", "learned", "learned rule #1", "repo /work/api, branch dev", "no ticket ID in branch dev", "no configured rule"} {
		if !strings.Contains(second, want) {
			t.Errorf("explain 09:10 lacks %q:\n%s", want, second)
		}
	}
}

func TestExplainNamesOverrideAndSanitizes(t *testing.T) {
	deps, st, out := newDailyEnv(t, "")
	day := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)
	mustAppend(t, st, shellContext(day, "/work/\x1b[31mevil", "dev"))
	seedPresence(t, st, day, 30)
	got := runExplainOut(t, deps, out, "1")
	if strings.Contains(got, "\x1b") {
		t.Fatalf("output contains an escape byte: %q", got)
	}
	if err := st.AddOverride(context.Background(), event.Override{Start: day, End: day.Add(30 * time.Minute), Ticket: "OVR-1"}); err != nil {
		t.Fatal(err)
	}
	got = runExplainOut(t, deps, out, "1")
	if !strings.Contains(got, "manual override") || !strings.Contains(got, "OVR-1") {
		t.Fatalf("override not named:\n%s", got)
	}
}

func TestExplainTargetErrors(t *testing.T) {
	deps, st, _ := newDailyEnv(t, "")
	ctx := context.Background()
	if err := runExplain(ctx, deps, "", "1"); err == nil {
		t.Fatal("expected error on an empty day")
	}
	day := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)
	mustAppend(t, st, shellContext(day, "/work/api", "ABC-7"))
	seedPresence(t, st, day, 30)
	for _, target := range []string{"0", "2", "abc", "23:59", "25:61"} {
		if err := runExplain(ctx, deps, "", target); err == nil {
			t.Errorf("target %q: expected an error", target)
		}
	}
}

func TestTodayNumbersBlocks(t *testing.T) {
	deps, st, out := newDailyEnv(t, "")
	day := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)
	mustAppend(t, st, shellContext(day, "/work/api", "ABC-7"))
	seedPresence(t, st, day, 30)
	if err := runToday(context.Background(), deps, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), " 1. 08:00-08:30") {
		t.Fatalf("blocks are not numbered:\n%s", out)
	}
}

// mapEnv is a daily environment on a file DB that can be reopened.
func mapEnv(t *testing.T) (deps dailyDeps, dbPath string, out *bytes.Buffer) {
	t.Helper()
	dbPath = filepath.Join(t.TempDir(), "events.db")
	out = &bytes.Buffer{}
	deps = dailyDeps{
		OpenStore: func() (dailyStore, error) { return store.Open(dbPath) },
		LoadConfig: func() (config.Config, error) {
			cfg, err := config.LoadOrDefault(filepath.Join(t.TempDir(), "missing.toml"))
			cfg.Location = time.UTC
			cfg.Rules = []config.Rule{{Repo: "/cfg/repo", Ticket: "CFG-1"}}
			return cfg, err
		},
		ConfigPath: func() (string, error) { return "/home/x/config.toml", nil },
		Now:        func() time.Time { return dailyNow },
		Out:        out,
	}
	return deps, dbPath, out
}

func TestMapEditPersistsAndRederives(t *testing.T) {
	ctx := context.Background()
	deps, dbPath, out := mapEnv(t)
	cfg, err := deps.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)
	civilDay := civil.Date{Year: 2026, Month: 3, Day: 2}

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	mustAppend(t, st, shellContext(day, "/work/api", "dev"))
	seedPresence(t, st, day, 60)
	if err := runMapAdd(ctx, deps, "/work/api", "", "old-1"); err != nil {
		t.Fatal(err)
	}
	before, err := review.LoadDay(ctx, st, cfg, civilDay)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Items) != 1 || before.Items[0].Ticket != "OLD-1" {
		t.Fatalf("items before = %+v", before.Items)
	}
	if err := st.Confirm(ctx, store.Confirmation{Day: civilDay.String(), Issue: "OLD-1", Type: sessionizer.DefaultWorkItemType, Minutes: before.Items[0].Minutes, ConfirmedAt: day}); err != nil {
		t.Fatal(err)
	}
	if items, err := review.ConfirmedItems(ctx, st, cfg, civilDay); err != nil || len(items) != 1 {
		t.Fatalf("confirmed before = %v, %v", items, err)
	}
	_ = st.Close()

	newTicket := "NEW-2"
	if err := runMapEdit(ctx, deps, "1", mapChanges{ticket: &newTicket}); err != nil {
		t.Fatal(err)
	}

	st, err = store.Open(dbPath) // reopen: the edit must be persisted
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	rules, err := st.LearnedRules(ctx)
	if err != nil || len(rules) != 1 || rules[0].Ticket != "NEW-2" {
		t.Fatalf("rules after reopen = %+v, %v", rules, err)
	}
	after, err := review.LoadDay(ctx, st, cfg, civilDay)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Items) != 1 || after.Items[0].Ticket != "NEW-2" {
		t.Fatalf("items after = %+v", after.Items)
	}
	items, err := review.ConfirmedItems(ctx, st, cfg, civilDay)
	if err != nil || len(items) != 0 {
		t.Fatalf("moved item still confirmed: %v, %v", items, err)
	}
	if !strings.Contains(out.String(), "re-derived") {
		t.Errorf("no re-derive note:\n%s", out)
	}
}

func TestMapListAddRemoveAndValidation(t *testing.T) {
	ctx := context.Background()
	deps, _, out := mapEnv(t)
	if err := runMapAdd(ctx, deps, "/work/api", "dev", "abc-1"); err != nil {
		t.Fatal(err)
	}
	if err := runMapList(ctx, deps); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/home/x/config.toml", "/cfg/repo -> CFG-1", "#1", "/work/api", "dev", "ABC-1", "2026-03-02"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("list lacks %q:\n%s", want, out)
		}
	}
	bad := []struct {
		name         string
		repo, branch string
		ticket       string
	}{
		{"empty repo and branch", "", "", "ABC-1"},
		{"bad ticket", "/work/api", "", "nope"},
		{"relative repo", "work/api", "", "ABC-1"},
	}
	for _, tc := range bad {
		if err := runMapAdd(ctx, deps, tc.repo, tc.branch, tc.ticket); err == nil {
			t.Errorf("%s: expected an error", tc.name)
		}
	}
	if err := runMapEdit(ctx, deps, "99", mapChanges{ticket: ptrIf(true, "ABC-2")}); err == nil || !strings.Contains(err.Error(), "no learned rule with id 99") {
		t.Errorf("edit unknown id: %v", err)
	}
	if err := runMapEdit(ctx, deps, "1", mapChanges{repo: ptrIf(true, ""), branch: ptrIf(true, "")}); err == nil {
		t.Error("edit leaving repo and branch empty must fail")
	}
	if err := runMapEdit(ctx, deps, "1", mapChanges{}); err == nil {
		t.Error("edit without changes must fail")
	}
	if err := runMapEdit(ctx, deps, "1", mapChanges{ticket: ptrIf(true, "bad")}); err == nil {
		t.Error("edit with invalid ticket must fail")
	}
	if err := runMapRemove(ctx, deps, "99"); err == nil || !strings.Contains(err.Error(), "no learned rule with id 99") {
		t.Errorf("rm unknown id: %v", err)
	}
	if err := runMapRemove(ctx, deps, "x"); err == nil {
		t.Error("rm with a non-numeric id must fail")
	}
	if err := runMapRemove(ctx, deps, "1"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runMapList(ctx, deps); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "#1 ") {
		t.Errorf("rule 1 still listed:\n%s", out)
	}
}

func TestExplainAndMapAreRegistered(t *testing.T) {
	root := NewRootCommand()
	for _, name := range []string{"explain", "map"} {
		if c, _, err := root.Find([]string{name}); err != nil || c.Name() != name {
			t.Errorf("command %q not registered", name)
		}
	}
	if c, _, err := root.Find([]string{"map", "rm"}); err != nil || c.Name() != "rm" {
		t.Error("map rm not registered")
	}
}

func TestMapStoresTrimmedValuesAndKeepsRelativeRepoOnTicketEdit(t *testing.T) {
	ctx := context.Background()
	deps, dbPath, _ := mapEnv(t)
	if err := runMapAdd(ctx, deps, "  /work/api ", "  dev ", "abc-1"); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	rules, err := st.LearnedRules(ctx)
	if err != nil || len(rules) != 1 || rules[0].Repo != "/work/api" || rules[0].BranchPattern != "dev" {
		t.Fatalf("stored = %+v, %v", rules, err)
	}
	legacy, err := st.AddLearnedRule(ctx, store.LearnedRule{Repo: "rel/path", Ticket: "OLD-1", CreatedAt: dailyNow})
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	id := strconv.FormatInt(legacy.ID, 10)
	if err := runMapEdit(ctx, deps, id, mapChanges{ticket: ptrIf(true, "new-2")}); err != nil {
		t.Fatalf("ticket-only edit of a relative repo failed: %v", err)
	}
	if err := runMapEdit(ctx, deps, id, mapChanges{repo: ptrIf(true, "still/rel")}); err == nil {
		t.Error("changing the repo to a relative path must fail")
	}
	if err := runMapEdit(ctx, deps, id, mapChanges{repo: ptrIf(true, "  /abs/path ")}); err != nil {
		t.Fatal(err)
	}
	st, err = store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	rules, _ = st.LearnedRules(ctx)
	if got := rules[len(rules)-1]; got.Repo != "/abs/path" || got.Ticket != "NEW-2" {
		t.Fatalf("rule = %+v", got)
	}
}

func TestMapListSanitizesStoredRules(t *testing.T) {
	ctx := context.Background()
	deps, dbPath, out := mapEnv(t)
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddLearnedRule(ctx, store.LearnedRule{Repo: "/w/\x1b[31mred", BranchPattern: "b\x07\rranch", Ticket: "AB\x1bC-1", CreatedAt: dailyNow}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	if err := runMapList(ctx, deps); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"\x1b", "\x07", "\r"} {
		if strings.Contains(out.String(), bad) {
			t.Fatalf("output contains %q:\n%q", bad, out.String())
		}
	}
}

func TestExplainClockTimeOnDSTDays(t *testing.T) {
	zurich, err := time.LoadLocation("Europe/Zurich")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	explainAt := func(t *testing.T, day string, events func(st *store.Store), target string) string {
		t.Helper()
		deps, st, out := newDailyEnv(t, "")
		deps.LoadConfig = func() (config.Config, error) {
			cfg, err := config.LoadOrDefault(filepath.Join(t.TempDir(), "missing.toml"))
			cfg.Location = zurich
			return cfg, err
		}
		events(st)
		if err := runExplain(context.Background(), deps, day, target); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	// Spring forward 2026-03-29: 02:00-03:00 does not exist; 02:15 means 03:15.
	spring := explainAt(t, "2026-03-29", func(st *store.Store) {
		start := time.Date(2026, 3, 29, 3, 0, 0, 0, zurich)
		mustAppend(t, st, shellContext(start, "/work/api", "ABC-7"))
		seedPresence(t, st, start, 30)
	}, "02:15")
	if !strings.Contains(spring, "Block 1") || !strings.Contains(spring, "03:00-03:30") {
		t.Errorf("spring-forward target 02:15 not normalised to 03:15:\n%s", spring)
	}
	// Fall back 2026-10-25: 02:00-03:00 occurs twice; 02:10 means the first.
	fall := explainAt(t, "2026-10-25", func(st *store.Store) {
		first := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC) // 02:00 CEST
		mustAppend(t, st, shellContext(first, "/work/api", "AAA-1"), shellContext(first.Add(time.Hour), "/work/api", "BBB-2"))
		seedPresence(t, st, first, 20)
		seedPresence(t, st, first.Add(time.Hour), 20)
	}, "02:10")
	if !strings.Contains(fall, "AAA-1") || strings.Contains(fall, "BBB-2") {
		t.Errorf("repeated 02:10 must mean the first occurrence:\n%s", fall)
	}
}
