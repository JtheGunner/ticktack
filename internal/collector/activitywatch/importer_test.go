package activitywatch

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/resolver"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
	"github.com/JtheGunner/ticktack/internal/store"

	_ "modernc.org/sqlite"
)

const (
	afkBucket    = "aw-watcher-afk_my-mac"
	windowBucket = "aw-watcher-window_my-mac"
)

var day = time.Date(2025, 3, 10, 10, 0, 0, 0, time.UTC)

type awEvent map[string]any

func ev(at time.Time, seconds float64, data map[string]any) awEvent {
	return awEvent{"timestamp": at.Format("2006-01-02T15:04:05.000000-07:00"), "duration": seconds, "data": data}
}

func afk(at time.Time, seconds float64, status string) awEvent {
	return ev(at, seconds, map[string]any{"status": status})
}

func win(at time.Time, seconds float64, app, title string) awEvent {
	return ev(at, seconds, map[string]any{"app": app, "title": title})
}

// fakeServer serves the given buckets (id -> events) like ActivityWatch.
func fakeServer(t *testing.T, buckets map[string][]awEvent) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/0/buckets/", func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/api/0/buckets/")
		if rest == "" {
			out := map[string]any{}
			for id := range buckets {
				out[id] = map[string]any{"id": id, "type": "x", "hostname": "my-mac"}
			}
			_ = json.NewEncoder(w).Encode(out)
			return
		}
		id := strings.TrimSuffix(rest, "/events")
		events, ok := buckets[id]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(events)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newImporter(t *testing.T, srv *httptest.Server, now time.Time) *Importer {
	t.Helper()
	c, err := New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return &Importer{Source: c, Now: func() time.Time { return now }}
}

func standardBuckets() map[string][]awEvent {
	return map[string][]awEvent{
		afkBucket: {
			// Newest first, like the server.
			afk(day.Add(5*time.Minute), 60, "not-afk"),
			afk(day.Add(2*time.Minute), 180, "afk"),
			afk(day, 120, "not-afk"),
		},
		windowBucket: {
			win(day.Add(5*time.Minute), 60, "Code", "secret-title-one"),
			win(day, 300, "Safari", "secret-title-two"),
		},
	}
}

func byKind(events []event.Event, kind event.Kind) []event.Event {
	var out []event.Event
	for _, e := range events {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

func TestImportMapsAfkToPresence(t *testing.T) {
	srv := fakeServer(t, standardBuckets())
	imp := newImporter(t, srv, day.Add(time.Hour))

	events, err := imp.Collect(context.Background(), day.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	presence := byKind(events, event.KindPresence)
	var got []string
	for _, e := range presence {
		if e.Source != Source || e.IdleSeconds != 0 {
			t.Errorf("unexpected presence event %+v", e)
		}
		got = append(got, e.At.Format("15:04:05"))
	}
	want := "10:00:00 10:00:30 10:01:00 10:01:30 10:05:00 10:05:30"
	if strings.Join(got, " ") != want {
		t.Errorf("presence samples = %v, want %s (none inside the afk interval)", got, want)
	}

	gaps := byKind(events, event.KindGap)
	if len(gaps) != 1 || gaps[0].At != day.Add(5*time.Minute) || gaps[0].IdleSeconds != 180 {
		t.Errorf("gap markers = %+v, want one at 10:05:00 with idle 180", gaps)
	}

	contexts := byKind(events, event.KindContext)
	if len(contexts) != 2 || contexts[0].App != "Safari" || contexts[1].App != "Code" {
		t.Errorf("context events = %+v", contexts)
	}
	for _, c := range contexts {
		if c.Repo != "" || c.Branch != "" || c.Ticket != "" || c.Source != Source {
			t.Errorf("context event carries more than the app: %+v", c)
		}
	}
}

func TestImportedPresenceDerivesExpectedBlockMinutes(t *testing.T) {
	srv := fakeServer(t, standardBuckets())
	imp := newImporter(t, srv, day.Add(time.Hour))
	events, err := imp.Collect(context.Background(), day.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	// A git context event before the window events must stay in effect: an
	// app-only window event carries no ticket signal.
	events = append(events, event.Event{At: day.Add(-time.Minute), Source: "git", Kind: event.KindContext, Repo: "/work/app", Ticket: "APP-1"})

	cfg := sessionizer.Config{GapThreshold: 5 * time.Minute}
	blocks := sessionizer.Derive(events, nil, cfg, resolver.Rules{})

	if len(blocks) != 2 {
		t.Fatalf("blocks = %+v, want two (the afk time must not be bridged)", blocks)
	}
	total := time.Duration(0)
	for _, b := range blocks {
		total += b.Duration()
		if b.Ticket != "APP-1" {
			t.Errorf("block %+v lost the git context", b)
		}
	}
	// A block ends at its last presence sample: 10:00-10:01:30 and
	// 10:05-10:05:30. The afk time is excluded.
	if total != 2*time.Minute {
		t.Errorf("total = %v, want 2m (1m30s + 30s, afk time excluded)", total)
	}
	if !blocks[0].Start.Equal(day) || !blocks[0].End.Equal(day.Add(90*time.Second)) {
		t.Errorf("first block = %v-%v", blocks[0].Start, blocks[0].End)
	}
}

func openStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st, path
}

func importInto(t *testing.T, imp *Importer, st *store.Store, since time.Time) {
	t.Helper()
	events, err := imp.Collect(context.Background(), since)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if err := st.Append(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}
}

func countEvents(t *testing.T, st *store.Store) int {
	t.Helper()
	events, err := st.Events(context.Background(), time.Unix(0, 0), day.Add(48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return len(events)
}

func TestImportIsIdempotent(t *testing.T) {
	srv := fakeServer(t, standardBuckets())
	imp := newImporter(t, srv, day.Add(time.Hour))
	st, _ := openStore(t)

	importInto(t, imp, st, day.Add(-time.Hour))
	first := countEvents(t, st)
	if first == 0 {
		t.Fatal("nothing imported")
	}
	importInto(t, imp, st, day.Add(-time.Hour))
	if got := countEvents(t, st); got != first {
		t.Fatalf("rows after identical re-import = %d, want %d", got, first)
	}
	// An overlapping, later range yields the same events.
	importInto(t, imp, st, day.Add(time.Minute))
	if got := countEvents(t, st); got != first {
		t.Fatalf("rows after overlapping re-import = %d, want %d", got, first)
	}
}

func TestImportIsIdempotentWhenAfkIntervalGrows(t *testing.T) {
	buckets := map[string][]awEvent{afkBucket: {afk(day, 60, "not-afk")}}
	srv := fakeServer(t, buckets)
	imp := newImporter(t, srv, day.Add(time.Hour))
	st, _ := openStore(t)

	importInto(t, imp, st, day.Add(-time.Hour))
	before := countEvents(t, st) // 2 samples
	// ActivityWatch extends the same event by heartbeat.
	buckets[afkBucket] = []awEvent{afk(day, 120, "not-afk")}
	importInto(t, imp, st, day.Add(-time.Hour))
	if got := countEvents(t, st); got != before+2 {
		t.Fatalf("rows = %d, want %d (only the two new samples)", got, before+2)
	}
}

func TestImportDropsTitles(t *testing.T) {
	titles := []string{"secret-title-one", "secret-title-two", "Quarterly Salary Review.xlsx"}
	buckets := standardBuckets()
	buckets[windowBucket] = append(buckets[windowBucket], win(day.Add(time.Minute), 30, "Excel", titles[2]))
	srv := fakeServer(t, buckets)
	imp := newImporter(t, srv, day.Add(time.Hour))
	st, path := openStore(t)

	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	events, err := imp.Collect(context.Background(), day.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if err := st.Append(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	// The in-memory events carry no title either.
	for _, e := range events {
		for _, field := range []string{e.Repo, e.Branch, e.Ticket, e.App, e.Key} {
			for _, title := range titles {
				if strings.Contains(field, title) {
					t.Fatalf("event %+v contains a title", e)
				}
			}
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	tables, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table'`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for tables.Next() {
		var n string
		if err := tables.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	_ = tables.Close()
	for _, table := range names {
		rows, err := db.Query(`SELECT * FROM "` + table + `"`)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			for _, v := range vals {
				text, _ := json.Marshal(v)
				for _, title := range titles {
					if strings.Contains(string(text), title) {
						t.Fatalf("table %s contains title %q", table, title)
					}
				}
			}
		}
		_ = rows.Close()
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, title := range titles {
		if bytes.Contains(raw, []byte(title)) {
			t.Errorf("database bytes contain title %q", title)
		}
		if strings.Contains(logs.String(), title) {
			t.Errorf("log output contains title %q", title)
		}
	}
	if !bytes.Contains(raw, []byte("Excel")) {
		t.Error("app name should have been stored")
	}
}

func TestImportClampsOpenIntervalToNow(t *testing.T) {
	srv := fakeServer(t, map[string][]awEvent{afkBucket: {afk(day, 3600, "not-afk")}})
	imp := newImporter(t, srv, day.Add(95*time.Second))
	events, err := imp.Collect(context.Background(), day.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(byKind(events, event.KindPresence)); got != 4 {
		t.Fatalf("samples = %d, want 4 (10:00:00 to 10:01:30)", got)
	}
}

func TestImportHostnameFiltersBuckets(t *testing.T) {
	srv := fakeServer(t, map[string][]awEvent{
		afkBucket:                   {afk(day, 60, "not-afk")},
		"aw-watcher-afk_other-host": {afk(day, 600, "not-afk")},
	})
	imp := newImporter(t, srv, day.Add(time.Hour))
	imp.Hostname = "my-mac"
	events, err := imp.Collect(context.Background(), day.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(byKind(events, event.KindPresence)); got != 2 {
		t.Fatalf("samples = %d, want 2 (other host ignored)", got)
	}
	imp.Hostname = "nope"
	if _, err := imp.Collect(context.Background(), day); !errors.Is(err, ErrNoBuckets) {
		t.Fatalf("err = %v, want ErrNoBuckets", err)
	}
}

func TestImportServerNotRunning(t *testing.T) {
	srv := fakeServer(t, nil)
	imp := newImporter(t, srv, day)
	srv.Close()
	_, err := imp.Collect(context.Background(), day.Add(-time.Hour))
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("err = %v, want ErrUnreachable", err)
	}
}

func TestImportNoBuckets(t *testing.T) {
	srv := fakeServer(t, map[string][]awEvent{"aw-watcher-web-chrome": nil})
	imp := newImporter(t, srv, day)
	if _, err := imp.Collect(context.Background(), day.Add(-time.Hour)); !errors.Is(err, ErrNoBuckets) {
		t.Fatalf("err = %v, want ErrNoBuckets", err)
	}
}

func TestImportMalformedJSONIsAnErrorWithoutBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"aw-watcher-afk_x": SECRET-BODY`))
	}))
	t.Cleanup(srv.Close)
	imp := newImporter(t, srv, day)
	_, err := imp.Collect(context.Background(), day.Add(-time.Hour))
	if !errors.Is(err, ErrBadResponse) || strings.Contains(err.Error(), "SECRET-BODY") {
		t.Fatalf("err = %v", err)
	}
}

func TestImportServerErrorReportsStatusOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "stack trace SECRET-BODY", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	imp := newImporter(t, srv, day)
	_, err := imp.Collect(context.Background(), day.Add(-time.Hour))
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") || strings.Contains(err.Error(), "SECRET-BODY") {
		t.Fatalf("err = %v", err)
	}
}

func TestImportBucketFailureDoesNotAbortOthers(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/0/buckets/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/0/buckets/":
			_, _ = w.Write([]byte(`{"` + afkBucket + `":{},"` + windowBucket + `":{}}`))
		case strings.Contains(r.URL.Path, "afk"):
			http.Error(w, "boom", http.StatusInternalServerError)
		default:
			_ = json.NewEncoder(w).Encode([]awEvent{win(day, 10, "Code", "t")})
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	imp := newImporter(t, srv, day.Add(time.Hour))

	events, err := imp.Collect(context.Background(), day.Add(-time.Hour))
	if err == nil || !strings.Contains(err.Error(), afkBucket) {
		t.Fatalf("err = %v, want the failing bucket named", err)
	}
	if len(events) != 1 || events[0].App != "Code" {
		t.Fatalf("events = %+v, want the window bucket imported", events)
	}
}

func TestImportRejectsOversizedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		chunk := bytes.Repeat([]byte(" "), 1<<20)
		for range maxBodyBytes/len(chunk) + 2 {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	imp := newImporter(t, srv, day)
	_, err := imp.Collect(context.Background(), day.Add(-time.Hour))
	if !errors.Is(err, ErrBadResponse) || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("err = %v", err)
	}
}

func TestImportPagesThroughLargeBuckets(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/0/buckets/" {
			_, _ = w.Write([]byte(`{"` + windowBucket + `":{}}`))
			return
		}
		calls++
		end, err := time.Parse(time.RFC3339Nano, r.URL.Query().Get("end"))
		if err != nil {
			http.Error(w, "bad end", http.StatusBadRequest)
			return
		}
		// Serve pageLimit events strictly older than end, until 2*pageLimit exist.
		var page []awEvent
		for i := 0; i < pageLimit; i++ {
			at := end.Add(-time.Duration(i+1) * time.Second)
			if at.Before(day.Add(-2 * pageLimit * time.Second)) {
				break
			}
			page = append(page, win(at, 1, "Code", "t"))
		}
		_ = json.NewEncoder(w).Encode(page)
	}))
	t.Cleanup(srv.Close)
	imp := newImporter(t, srv, day)
	events, err := imp.Collect(context.Background(), day.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if calls < 2 || len(events) < pageLimit+1 {
		t.Fatalf("calls = %d, events = %d: paging did not continue", calls, len(events))
	}
}
