package title

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/store"
)

type recorder struct{ events []event.Event }

func (r *recorder) Append(_ context.Context, e event.Event) error {
	r.events = append(r.events, e)
	return nil
}

var testNow = time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)

func newCollector(src Source, sink Appender, projects map[string]string) *Collector {
	return &Collector{
		Source:   src,
		Sink:     sink,
		Projects: projects,
		Now:      func() time.Time { return testNow },
		Paused:   func() bool { return false },
	}
}

func TestHintMapsProjectNameToRepo(t *testing.T) {
	src := &Fake{}
	src.Push("com.jetbrains.goland", "ticktack – main.go")
	src.Push("com.jetbrains.goland", "ABC-9 – main.go")
	src.Push("com.google.Chrome", "DEF-3 Fix it - YouTrack")
	src.Push("com.jetbrains.goland", "other – main.go")
	rec := &recorder{}
	c := newCollector(src, rec, map[string]string{"ticktack": "/work/ticktack", "ABC-9": "/work/abc"})

	for range 4 {
		c.Tick(context.Background())
	}
	want := []event.Event{
		{At: testNow, Source: "title", Kind: event.KindContext, Repo: "/work/ticktack"},
		{At: testNow, Source: "title", Kind: event.KindContext, Repo: "/work/abc", Ticket: "ABC-9"},
		{At: testNow, Source: "title", Kind: event.KindContext, Ticket: "DEF-3"},
	}
	if !reflect.DeepEqual(rec.events, want) {
		t.Errorf("events = %+v\nwant %+v", rec.events, want)
	}
}

func TestCollectorEmitsNothingWhenNothingRecognised(t *testing.T) {
	src := &Fake{}
	src.Push("com.example.unknown", "ABC-1 whatever")
	src.Push("com.jetbrains.goland", "unmapped – x.go")
	src.PushError(errors.New("boom"))
	rec := &recorder{}
	c := newCollector(src, rec, map[string]string{"ticktack": "/work/ticktack"})
	for range 3 {
		c.Tick(context.Background())
	}
	if len(rec.events) != 0 {
		t.Errorf("unexpected events: %+v", rec.events)
	}
}

func TestCollectorStoresNothingWhilePaused(t *testing.T) {
	src := &Fake{}
	src.Push("com.jetbrains.goland", "ticktack – main.go")
	rec := &recorder{}
	c := newCollector(src, rec, map[string]string{"ticktack": "/work/ticktack"})
	c.Paused = func() bool { return true }
	c.Tick(context.Background())
	if len(rec.events) != 0 {
		t.Errorf("paused collector stored %+v", rec.events)
	}
}

func TestCollectorLogsNeverContainTitles(t *testing.T) {
	var logs bytes.Buffer
	src := &Fake{}
	src.PushError(ErrPermission)
	src.PushError(ErrPermission)
	src.Push("com.jetbrains.goland", "SECRET-TITLE-TEXT – main.go")
	rec := &recorder{}
	c := newCollector(src, rec, nil)
	c.Logf = func(format string, args ...any) { fmt.Fprintf(&logs, format+"\n", args...) }
	for range 3 {
		c.Tick(context.Background())
	}
	if strings.Contains(logs.String(), "SECRET") {
		t.Errorf("log leaks a title: %s", logs.String())
	}
	if got := strings.Count(logs.String(), "skipped"); got != 1 {
		t.Errorf("repeated failures should be logged once, got %d:\n%s", got, logs.String())
	}
}

func TestCollectorRunStopsOnCancel(t *testing.T) {
	ticks := make(chan time.Time)
	c := newCollector(&Fake{}, &recorder{}, nil)
	c.Ticks = ticks
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- c.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestTitleNeverReachesStore(t *testing.T) {
	secrets := []string{"hunter2-PASSWORD", "TOPSECRETPLAN", "Quarterly layoffs memo", "ünïcödé-geheim"}
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "ticktack.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	src := &Fake{}
	// Mapped project, ticket, unmapped project, unknown app, browser tab: all
	// with distinctive secret text in the title.
	src.Push("com.jetbrains.goland", "ticktack – hunter2-PASSWORD.go")
	src.Push("com.jetbrains.goland", "ABC-5 [~/TOPSECRETPLAN] – notes.go")
	src.Push("com.jetbrains.goland", "ünïcödé-geheim – x.go")
	src.Push("com.example.mail", "Quarterly layoffs memo ABC-6")
	src.Push("com.google.Chrome", "ABC-7 Quarterly layoffs memo - YouTrack")
	src.Push("com.apple.dt.Xcode", "ticktack — TOPSECRETPLAN.swift")
	c := &Collector{
		Source:   src,
		Sink:     st,
		Projects: map[string]string{"ticktack": "/work/ticktack"},
		Now:      time.Now,
		Paused:   func() bool { return false },
	}
	for range 6 {
		c.Tick(context.Background())
	}

	events, err := st.Events(context.Background(), time.Time{}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("expected hint events to be stored")
	}
	for _, e := range events {
		for _, field := range []string{e.Source, e.Repo, e.Branch, e.Ticket, e.App} {
			for _, secret := range secrets {
				if strings.Contains(field, secret) {
					t.Errorf("event %+v carries %q", e, secret)
				}
			}
		}
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	// Scan every file of the database directory (main file, WAL, journal) as raw bytes.
	for _, leak := range secretsInDir(t, dir, secrets) {
		t.Errorf("persisted bytes contain %s", leak)
	}
}

// secretsInDir scans every file of dir as raw bytes and names each file and
// secret pair that matches.
func secretsInDir(t *testing.T, dir string, secrets []string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var leaks []string
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range secrets {
			if bytes.Contains(data, []byte(secret)) {
				leaks = append(leaks, fmt.Sprintf("%q in %s", secret, entry.Name()))
			}
		}
	}
	return leaks
}

// TestTitleHintIsExtractedInMemoryButNotPersisted is the positive control of
// TestTitleNeverReachesStore: the hint path does see the title and extracts
// the project and ticket, yet the raw title text stays out of the store.
func TestTitleHintIsExtractedInMemoryButNotPersisted(t *testing.T) {
	const title = "ABC-5 [~/TOPSECRETPLAN] – notes.go"
	hint := Parse("com.jetbrains.goland", title, nil)
	if hint.Ticket != "ABC-5" {
		t.Fatalf("hint ticket = %q, want ABC-5: the title never reached the hint path", hint.Ticket)
	}

	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "ticktack.db"))
	if err != nil {
		t.Fatal(err)
	}
	src := &Fake{}
	src.Push("com.jetbrains.goland", title)
	c := &Collector{Source: src, Sink: st, Now: time.Now, Paused: func() bool { return false }}
	c.Tick(context.Background())
	events, err := st.Events(context.Background(), time.Time{}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Ticket != hint.Ticket {
		t.Fatalf("events = %+v, want one event carrying the extracted ticket %q", events, hint.Ticket)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if leaks := secretsInDir(t, dir, []string{"TOPSECRETPLAN", "notes.go"}); len(leaks) != 0 {
		t.Errorf("raw title persisted: %v", leaks)
	}
}

// TestTitleLeakDetectionCanFail proves the persistence checks are able to fail:
// a store that does receive the raw title is reported.
func TestTitleLeakDetectionCanFail(t *testing.T) {
	const secret = "TOPSECRETPLAN"
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "ticktack.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	leaky := event.Event{At: time.Now(), Source: EventSource, Kind: event.KindContext, Ticket: "ABC-5", Branch: "ABC-5 [~/" + secret + "] - notes.go"}
	if err := st.Append(context.Background(), leaky); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if leaks := secretsInDir(t, dir, []string{secret}); len(leaks) == 0 {
		t.Error("raw byte scan did not detect a persisted title")
	}
	if dump := dumpAllTables(t, dbPath); !strings.Contains(dump, secret) {
		t.Error("table dump did not detect a persisted title")
	}
}

func TestTitleNeverReachesEveryTable(t *testing.T) {
	// Scan all rows of all tables, not only events.
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "ticktack.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	src := &Fake{}
	src.Push("com.jetbrains.goland", "ticktack – hunter2-PASSWORD.go")
	c := &Collector{Source: src, Sink: st, Projects: map[string]string{"ticktack": "/work/ticktack"}, Now: time.Now, Paused: func() bool { return false }}
	c.Tick(context.Background())

	dump := dumpAllTables(t, dbPath)
	if !strings.Contains(dump, "/work/ticktack") {
		t.Fatalf("expected the repo to be stored, dump:\n%s", dump)
	}
	if strings.Contains(dump, "hunter2-PASSWORD") {
		t.Errorf("a table contains the title text")
	}
}

func TestEventAndHintCarryNoTitleField(t *testing.T) {
	for _, typ := range []reflect.Type{reflect.TypeOf(event.Event{}), reflect.TypeOf(Hint{})} {
		for i := 0; i < typ.NumField(); i++ {
			name := strings.ToLower(typ.Field(i).Name)
			if strings.Contains(name, "title") || strings.Contains(name, "text") || strings.Contains(name, "window") {
				t.Errorf("%s has a title-like field %q", typ.Name(), typ.Field(i).Name)
			}
		}
	}
}
