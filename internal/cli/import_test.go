package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/collector/activitywatch"
	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/paths"
)

// awTestServer serves one AFK and one window bucket and records the start
// parameter of every events request.
func awTestServer(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var starts []string
	day := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	stamp := func(d time.Duration) string { return day.Add(d).Format("2006-01-02T15:04:05.000000-07:00") }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/0/buckets/":
			_, _ = w.Write([]byte(`{"aw-watcher-afk_h":{},"aw-watcher-window_h":{}}`))
		case strings.Contains(r.URL.Path, "afk"):
			mu.Lock()
			starts = append(starts, r.URL.Query().Get("start"))
			mu.Unlock()
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"timestamp": stamp(0), "duration": 90, "data": map[string]any{"status": "not-afk"}},
			})
		default:
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"timestamp": stamp(0), "duration": 60, "data": map[string]any{"app": "Code", "title": "private-title"}},
			})
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), starts...)
	}
}

func awSourceFor(srv *httptest.Server, called *bool) newSourceFunc {
	return func(config.ActivityWatch) (activitywatch.EventSource, error) {
		*called = true
		return activitywatch.New(srv.URL)
	}
}

func enableActivityWatch(deps *dailyDeps) {
	load := deps.LoadConfig
	deps.LoadConfig = func() (config.Config, error) {
		cfg, err := load()
		cfg.ActivityWatch.Enabled = true
		return cfg, err
	}
}

func storedCount(t *testing.T, deps dailyDeps) int {
	t.Helper()
	st, err := deps.OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	events, err := st.Events(context.Background(), time.Unix(0, 0), dailyNow.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return len(events)
}

func TestImportRequiresOptIn(t *testing.T) {
	deps, _, out := newDailyEnv(t, "")
	srv, _ := awTestServer(t)
	var called bool
	err := runImportActivityWatch(context.Background(), deps, awSourceFor(srv, &called), "")
	if err == nil || !strings.Contains(err.Error(), "enabled = true") {
		t.Fatalf("err = %v, want a clear opt-in message", err)
	}
	if called || storedCount(t, deps) != 0 || out.Len() != 0 {
		t.Fatal("a disabled import must not contact the server or store anything")
	}
}

func TestImportStoresEventsAndIsIdempotent(t *testing.T) {
	deps, _, out := newDailyEnv(t, "")
	enableActivityWatch(&deps)
	srv, _ := awTestServer(t)
	var called bool

	if err := runImportActivityWatch(context.Background(), deps, awSourceFor(srv, &called), "2026-03-02"); err != nil {
		t.Fatal(err)
	}
	first := storedCount(t, deps)
	// 3 presence samples (9:00:00, 9:00:30, 9:01:00) and 1 context hint.
	if first != 4 || !strings.Contains(out.String(), "3 presence") || !strings.Contains(out.String(), "1 context hints") {
		t.Fatalf("stored = %d, output:\n%s", first, out.String())
	}
	if err := runImportActivityWatch(context.Background(), deps, awSourceFor(srv, &called), "2026-03-02"); err != nil {
		t.Fatal(err)
	}
	if got := storedCount(t, deps); got != first {
		t.Fatalf("rows after second run = %d, want %d", got, first)
	}
}

func TestImportHonoursPauseFlag(t *testing.T) {
	deps, _, out := newDailyEnv(t, "")
	enableActivityWatch(&deps)
	pausePath, _ := paths.PausePath()
	if err := os.MkdirAll(filepath.Dir(pausePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pausePath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	srv, _ := awTestServer(t)
	var called bool
	if err := runImportActivityWatch(context.Background(), deps, awSourceFor(srv, &called), ""); err != nil {
		t.Fatal(err)
	}
	if called || storedCount(t, deps) != 0 || !strings.Contains(out.String(), "paused") {
		t.Fatalf("paused import must store nothing; output: %q", out.String())
	}
}

func TestImportRejectsBadSince(t *testing.T) {
	deps, _, _ := newDailyEnv(t, "")
	enableActivityWatch(&deps)
	srv, _ := awTestServer(t)
	var called bool
	err := runImportActivityWatch(context.Background(), deps, awSourceFor(srv, &called), "yesterday")
	if err == nil || !strings.Contains(err.Error(), "YYYY-MM-DD") {
		t.Fatalf("err = %v", err)
	}
}

func TestImportCursorStartsShortlyBeforeNewestImportedEvent(t *testing.T) {
	deps, _, _ := newDailyEnv(t, "")
	enableActivityWatch(&deps)
	srv, starts := awTestServer(t)
	var called bool

	// First run: no history, so a day back from "now".
	if err := runImportActivityWatch(context.Background(), deps, awSourceFor(srv, &called), ""); err != nil {
		t.Fatal(err)
	}
	// Second run: one hour before the newest imported event (09:01:00, so 08:01:00).
	if err := runImportActivityWatch(context.Background(), deps, awSourceFor(srv, &called), ""); err != nil {
		t.Fatal(err)
	}
	got := starts()
	want := []string{dailyNow.Add(-24 * time.Hour).Format(time.RFC3339Nano), time.Date(2026, 3, 2, 8, 1, 0, 0, time.UTC).Format(time.RFC3339Nano)}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("start params = %v, want %v", got, want)
	}
}

func writeAWConfig(t *testing.T, enabled bool, url string) {
	t.Helper()
	cfgPath, _ := config.DefaultPath()
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatal(err)
	}
	body := config.DefaultTOML("") + "\n[activitywatch]\nenabled = " + map[bool]string{true: "true", false: "false"}[enabled] + "\nurl = \"" + url + "\"\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func runAWCheck(t *testing.T, enabled bool, url string) checkResult {
	t.Helper()
	env := newTestEnv(t, "", "")
	writeAWConfig(t, enabled, url)
	doc := env.deps.Doctor
	doc.NewActivityWatch = func(u string) (bucketLister, error) {
		return activitywatch.New(u, activitywatch.WithHTTPClient(&http.Client{Timeout: 2 * time.Second}))
	}
	return checkActivityWatch(context.Background(), doc)
}

func TestDoctorSkipsDisabledActivityWatch(t *testing.T) {
	res := runAWCheck(t, false, "http://localhost:5600")
	if res.Status != statusSkip {
		t.Fatalf("status = %v, want skip: %+v", res.Status, res)
	}
}

func TestDoctorActivityWatchReachable(t *testing.T) {
	srv, _ := awTestServer(t)
	res := runAWCheck(t, true, srv.URL)
	if res.Status != statusOK || !strings.Contains(res.Detail, "2 AFK and window buckets") {
		t.Fatalf("result = %+v", res)
	}
}

func TestDoctorActivityWatchUnreachableHasFix(t *testing.T) {
	srv, _ := awTestServer(t)
	url := srv.URL
	srv.Close()
	res := runAWCheck(t, true, url)
	if res.Status != statusFail || res.Hint == "" {
		t.Fatalf("result = %+v", res)
	}
}

func TestDoctorActivityWatchWithoutBucketsFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"aw-watcher-web":{}}`))
	}))
	t.Cleanup(srv.Close)
	res := runAWCheck(t, true, srv.URL)
	if res.Status != statusFail || !strings.Contains(res.Detail, "no aw-watcher") || res.Hint == "" {
		t.Fatalf("result = %+v", res)
	}
}

const escapeSequence = "\x1b]0;pwned\x07\x1b[31m"

func hasControlRune(s string) bool {
	for _, r := range s {
		if r < 0x20 && r != '\n' || r == 0x7f || r >= 0x80 && r < 0xa0 {
			return true
		}
	}
	return false
}

func maliciousAWServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/0/buckets/" {
			bucket, _ := json.Marshal("aw-watcher-afk_" + escapeSequence)
			_, _ = w.Write([]byte("{" + string(bucket) + ":{}}"))
			return
		}
		http.Error(w, escapeSequence+"error text", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestImportErrorsCarryNoControlRunesFromTheServer(t *testing.T) {
	deps, _, out := newDailyEnv(t, "")
	enableActivityWatch(&deps)
	srv := maliciousAWServer(t)
	var called bool
	err := runImportActivityWatch(context.Background(), deps, awSourceFor(srv, &called), "")
	if err == nil {
		t.Fatal("expected an error")
	}
	if hasControlRune(err.Error()) || hasControlRune(out.String()) {
		t.Fatalf("control runes reached the terminal: %q / %q", err.Error(), out.String())
	}
	if !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("error lost its meaning: %v", err)
	}
}

func TestDoctorOutputCarriesNoControlRunesFromTheServer(t *testing.T) {
	env := newTestEnv(t, "", "")
	srv := maliciousAWServer(t)
	writeAWConfig(t, true, srv.URL)
	doc := env.deps.Doctor
	doc.NewActivityWatch = func(u string) (bucketLister, error) { return activitywatch.New(u) }
	var out strings.Builder
	_ = runDoctor(context.Background(), doc, &out)
	if hasControlRune(strings.ReplaceAll(out.String(), "\n", "")) {
		t.Fatalf("control runes in doctor output: %q", out.String())
	}
	if !strings.Contains(out.String(), "activitywatch:") {
		t.Fatalf("no activitywatch line:\n%s", out.String())
	}
}
