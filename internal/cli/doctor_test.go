package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JtheGunner/ticktack/internal/collector/presence"
	"github.com/JtheGunner/ticktack/internal/collector/title"
	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/paths"
	"github.com/JtheGunner/ticktack/internal/shellhook"
	"github.com/JtheGunner/ticktack/internal/store"
	"github.com/JtheGunner/ticktack/internal/token"
)

func TestDoctorReportsEachCheckIndependently(t *testing.T) {
	env := newTestEnv(t, "", "")
	doc := env.deps.Doctor
	doc.LookPath = func(string) (string, error) { return "", errors.New("missing") }
	doc.OpenStore = func(string) (eventReader, error) { return nil, errors.New("disk full") }

	var out bytes.Buffer
	err := runDoctor(context.Background(), doc, &out)
	if !errors.Is(err, ErrChecksFailed) {
		t.Fatalf("err = %v", err)
	}
	text := out.String()
	for _, name := range []string{"config:", "shell hook:", "agent:", "token:", "youtrack:", "database:", "presence tools:"} {
		if !strings.Contains(text, "x  "+name) {
			t.Errorf("missing failing line for %s:\n%s", name, text)
		}
	}
	if got := strings.Count(text, "fix: "); got != 7 {
		t.Errorf("fix hints = %d, want 7:\n%s", got, text)
	}
	if !strings.Contains(text, "7 of 12 checks failed.") {
		t.Errorf("summary missing:\n%s", text)
	}
}

func TestDoctorFailureInOneCheckDoesNotHideOthers(t *testing.T) {
	env := newTestEnv(t, "", "")
	// Only the token is missing; everything else is set up.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"login":"jane"}`))
	}))
	t.Cleanup(srv.Close)
	cfgPath, _ := config.DefaultPath()
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(config.DefaultTOML(srv.URL)), 0o600); err != nil {
		t.Fatal(err)
	}
	script, _ := shellhook.Script("zsh")
	if err := os.WriteFile(filepath.Join(env.home, ".zshrc"), []byte(renderManagedBlock(script)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(agentPlistPath(env.home)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agentPlistPath(env.home), []byte("plist"), 0o644); err != nil {
		t.Fatal(err)
	}
	dbPath, _ := paths.DBPath()
	now := time.Now()
	seedEvents(t, dbPath,
		event.Event{At: now.Add(-time.Hour), Source: "shell", Kind: event.KindPresence},
		event.Event{At: now.Add(-10 * time.Second), Source: "presence", Kind: event.KindPresence},
	)

	var out bytes.Buffer
	err := runDoctor(context.Background(), env.deps.Doctor, &out)
	if !errors.Is(err, ErrChecksFailed) {
		t.Fatalf("err = %v\n%s", err, out.String())
	}
	text := out.String()
	for _, name := range []string{"config:", "shell hook:", "agent:", "youtrack:", "database:", "presence tools:"} {
		if !strings.Contains(text, "+  "+name) {
			t.Errorf("expected OK for %s:\n%s", name, text)
		}
	}
	if !strings.Contains(text, "x  token:") || !strings.Contains(text, "1 of 12 checks failed.") {
		t.Errorf("token failure not reported alone:\n%s", text)
	}

	// With a token stored, everything passes and the exit is clean.
	if err := token.Set(testSecret); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runDoctor(context.Background(), env.deps.Doctor, &out); err != nil {
		t.Fatalf("all checks should pass: %v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), testSecret) || !strings.Contains(out.String(), "All 12 checks passed.") {
		t.Errorf("unexpected output:\n%s", out.String())
	}
}

func TestDoctorExitCodeNonZeroOnFailure(t *testing.T) {
	newTestEnv(t, "", "") // temp HOME and mock keychain; nothing is set up
	root := NewRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"doctor"})
	err := root.Execute()
	if !errors.Is(err, ErrChecksFailed) {
		t.Fatalf("err = %v\n%s", err, out.String())
	}
	if ExitCode(err) == 0 {
		t.Error("exit code is 0 although checks failed")
	}
	if !IsSilent(err) {
		t.Error("main would print the error a second time")
	}
	if ExitCode(nil) != 0 {
		t.Error("exit code for success is not 0")
	}
}

func TestCheckTokenMapsStatusCodesToHints(t *testing.T) {
	tests := []struct {
		status int
		want   string
	}{
		{http.StatusUnauthorized, "rejected the token"},
		{http.StatusForbidden, "lacks permission"},
	}
	for _, tc := range tests {
		env := newTestEnv(t, "", "")
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
		}))
		cfgPath, _ := config.DefaultPath()
		_ = os.MkdirAll(filepath.Dir(cfgPath), 0o700)
		_ = os.WriteFile(cfgPath, []byte(config.DefaultTOML(srv.URL)), 0o600)
		_ = token.Set(testSecret)
		res := checkToken(context.Background(), env.deps.Doctor)
		srv.Close()
		if res.Status != statusFail || !strings.Contains(res.Detail, tc.want) || res.Hint == "" {
			t.Errorf("status %d: %+v", tc.status, res)
		}
		if strings.Contains(res.Detail+res.Hint, testSecret) {
			t.Error("token leaked into the result")
		}
	}
}

func TestCheckTokenReportsUnreachableServer(t *testing.T) {
	env := newTestEnv(t, "", "")
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	cfgPath, _ := config.DefaultPath()
	_ = os.MkdirAll(filepath.Dir(cfgPath), 0o700)
	_ = os.WriteFile(cfgPath, []byte(config.DefaultTOML(url)), 0o600)
	_ = token.Set(testSecret)
	res := checkToken(context.Background(), env.deps.Doctor)
	if res.Status != statusFail || !strings.Contains(res.Detail, "request failed") {
		t.Errorf("%+v", res)
	}
	if strings.Contains(res.Detail, testSecret) {
		t.Error("token leaked")
	}
	if reach := checkReachable(context.Background(), env.deps.Doctor); reach.Status != statusFail {
		t.Errorf("reachability: %+v", reach)
	}
}

func TestCheckReachableAcceptsAnyHTTPResponse(t *testing.T) {
	env := newTestEnv(t, "", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("reachability check must be unauthenticated")
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	cfgPath, _ := config.DefaultPath()
	_ = os.MkdirAll(filepath.Dir(cfgPath), 0o700)
	_ = os.WriteFile(cfgPath, []byte(config.DefaultTOML(srv.URL)), 0o600)
	if res := checkReachable(context.Background(), env.deps.Doctor); res.Status != statusOK {
		t.Errorf("%+v", res)
	}
}

func TestCheckHookNamesTheMissingHalf(t *testing.T) {
	env := newTestEnv(t, "", "")
	script, _ := shellhook.Script("zsh")
	if err := os.WriteFile(filepath.Join(env.home, ".zshrc"), []byte(renderManagedBlock(script)), 0o644); err != nil {
		t.Fatal(err)
	}
	res := checkHook(context.Background(), env.deps.Doctor)
	if res.Status != statusFail || !strings.Contains(res.Detail, "no shell event") {
		t.Errorf("block present, no event: %+v", res)
	}

	dbPath, _ := paths.DBPath()
	seedEvents(t, dbPath, event.Event{At: time.Now().Add(-48 * time.Hour), Source: "shell", Kind: event.KindPresence})
	if res := checkHook(context.Background(), env.deps.Doctor); res.Status != statusFail {
		t.Errorf("stale event accepted: %+v", res)
	}

	if err := os.Remove(filepath.Join(env.home, ".zshrc")); err != nil {
		t.Fatal(err)
	}
	seedEvents(t, dbPath, event.Event{At: time.Now(), Source: "shell", Kind: event.KindPresence})
	res = checkHook(context.Background(), env.deps.Doctor)
	if res.Status != statusFail || !strings.Contains(res.Detail, "no managed block") {
		t.Errorf("event present, no block: %+v", res)
	}
}

func TestCheckAgentNeedsRecentPresenceSample(t *testing.T) {
	env := newTestEnv(t, "", "")
	if err := os.MkdirAll(filepath.Dir(agentPlistPath(env.home)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agentPlistPath(env.home), []byte("plist"), 0o644); err != nil {
		t.Fatal(err)
	}
	dbPath, _ := paths.DBPath()
	seedEvents(t, dbPath, event.Event{At: time.Now().Add(-5 * time.Minute), Source: "presence", Kind: event.KindPresence})
	if res := checkAgent(context.Background(), env.deps.Doctor); res.Status != statusFail {
		t.Errorf("stale sample accepted: %+v", res)
	}
	seedEvents(t, dbPath, event.Event{At: time.Now().Add(-5 * time.Second), Source: "presence", Kind: event.KindPresence})
	if res := checkAgent(context.Background(), env.deps.Doctor); res.Status != statusOK {
		t.Errorf("%+v", res)
	}
}

func TestCheckToolsAndAgentAreSkippedOffMac(t *testing.T) {
	env := newTestEnv(t, "", "")
	doc := env.deps.Doctor
	doc.GOOS = "linux"
	if res := checkTools(context.Background(), doc); res.Status != statusSkip {
		t.Errorf("%+v", res)
	}
	if res := checkAgent(context.Background(), doc); res.Status != statusSkip {
		t.Errorf("%+v", res)
	}
}

func TestCheckDBDoesNotAddEvents(t *testing.T) {
	env := newTestEnv(t, "", "")
	if res := checkDB(context.Background(), env.deps.Doctor); res.Status != statusOK {
		t.Fatalf("%+v", res)
	}
	dbPath, _ := paths.DBPath()
	st, err := env.deps.Doctor.OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	events, err := st.Events(context.Background(), time.Unix(0, 0), time.Now().Add(time.Hour))
	if err != nil || len(events) != 0 {
		t.Errorf("events=%v err=%v", events, err)
	}
}

func writeTitleHintConfig(t *testing.T, enabled bool) {
	t.Helper()
	cfgPath, _ := config.DefaultPath()
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf("[titlehint]\nenabled = %t\n", enabled)
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCheckTitleHintIsSkippedWhenDisabled(t *testing.T) {
	env := newTestEnv(t, "", "")
	doc := env.deps.Doctor
	doc.TitleSource = func() title.Source { t.Fatal("source must not be read when disabled"); return nil }

	if res := checkTitleHint(context.Background(), doc); res.Status != statusSkip {
		t.Errorf("no config file: %+v", res)
	}
	writeTitleHintConfig(t, false)
	if res := checkTitleHint(context.Background(), doc); res.Status != statusSkip || !strings.Contains(res.Detail, "disabled") {
		t.Errorf("disabled: %+v", res)
	}
}

func TestCheckTitleHintReportsPermissionState(t *testing.T) {
	env := newTestEnv(t, "", "")
	writeTitleHintConfig(t, true)
	doc := env.deps.Doctor

	granted := &title.Fake{}
	granted.Push("com.jetbrains.goland", "SECRET-TITLE – main.go")
	doc.TitleSource = func() title.Source { return granted }
	res := checkTitleHint(context.Background(), doc)
	if res.Status != statusOK || strings.Contains(res.Detail, "SECRET") {
		t.Errorf("granted: %+v", res)
	}

	denied := &title.Fake{}
	denied.PushError(title.ErrPermission)
	doc.TitleSource = func() title.Source { return denied }
	res = checkTitleHint(context.Background(), doc)
	if res.Status != statusFail || !strings.Contains(res.Hint, "Accessibility") {
		t.Errorf("denied: %+v", res)
	}

	broken := &title.Fake{}
	broken.PushError(errors.New("osascript could not run"))
	doc.TitleSource = func() title.Source { return broken }
	if res = checkTitleHint(context.Background(), doc); res.Status != statusFail || res.Hint == "" {
		t.Errorf("broken: %+v", res)
	}
}

func TestCheckTitleHintSkippedOffMac(t *testing.T) {
	env := newTestEnv(t, "", "")
	writeTitleHintConfig(t, true)
	doc := env.deps.Doctor
	doc.GOOS = "linux"
	if res := checkTitleHint(context.Background(), doc); res.Status != statusSkip {
		t.Errorf("%+v", res)
	}
}

func TestDoctorRunsTitleHintCheck(t *testing.T) {
	env := newTestEnv(t, "", "")
	var out bytes.Buffer
	_ = runDoctor(context.Background(), env.deps.Doctor, &out)
	if !strings.Contains(out.String(), "-  window title hints:") {
		t.Errorf("doctor output lacks the title hint line:\n%s", out.String())
	}
}

func TestParseAgentInterval(t *testing.T) {
	plist := func(args ...string) string {
		var b strings.Builder
		b.WriteString("<plist><dict><key>ProgramArguments</key><array>")
		for _, a := range args {
			b.WriteString("<string>" + a + "</string>")
		}
		b.WriteString("</array></dict></plist>")
		return b.String()
	}
	tests := []struct {
		name string
		data string
		want time.Duration
	}{
		{"no flag uses the default", plist("/bin/ticktack", "agent"), presence.DefaultInterval},
		{"separate value", plist("/bin/ticktack", "agent", "--interval", "2m"), 2 * time.Minute},
		{"equals form", plist("/bin/ticktack", "agent", "--interval=45s"), 45 * time.Second},
		{"invalid value uses the default", plist("/bin/ticktack", "agent", "--interval", "soon"), presence.DefaultInterval},
		{"non-positive value uses the default", plist("/bin/ticktack", "agent", "--interval", "0s"), presence.DefaultInterval},
		{"missing value uses the default", plist("/bin/ticktack", "agent", "--interval"), presence.DefaultInterval},
		{"overflowing value uses the default", plist("/bin/ticktack", "agent", "--interval", "2000000h"), presence.DefaultInterval},
		{"value above the cap uses the default", plist("/bin/ticktack", "agent", "--interval", "25h"), presence.DefaultInterval},
		{"value at the cap is accepted", plist("/bin/ticktack", "agent", "--interval", "24h"), 24 * time.Hour},
		{"garbage uses the default", "plist", presence.DefaultInterval},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseAgentInterval([]byte(tt.data)); got != tt.want {
				t.Errorf("interval = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCheckAgentHonoursTheInstalledInterval(t *testing.T) {
	env := newTestEnv(t, "", "")
	if err := os.MkdirAll(filepath.Dir(agentPlistPath(env.home)), 0o755); err != nil {
		t.Fatal(err)
	}
	plist := "<plist><array><string>/bin/ticktack</string><string>agent</string><string>--interval</string><string>2m</string></array></plist>"
	if err := os.WriteFile(agentPlistPath(env.home), []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
	dbPath, _ := paths.DBPath()
	// 90 s old: stale for the 30 s default (window 60 s), fresh for 2 m (window 4 m).
	seedEvents(t, dbPath, event.Event{At: time.Now().Add(-90 * time.Second), Source: "presence", Kind: event.KindPresence})
	if res := checkAgent(context.Background(), env.deps.Doctor); res.Status != statusOK {
		t.Errorf("sample within the configured interval rejected: %+v", res)
	}
}

func TestCheckDBFailsOnReadOnlyDatabase(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions")
	}
	env := newTestEnv(t, "", "")
	if res := checkDB(context.Background(), env.deps.Doctor); res.Status != statusOK {
		t.Fatalf("fresh database: %+v", res)
	}
	dbPath, _ := paths.DBPath()
	if err := os.Chmod(dbPath, 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dbPath, 0o600) })
	res := checkDB(context.Background(), env.deps.Doctor)
	if res.Status != statusFail || !strings.Contains(res.Detail, "read-only") || res.Hint == "" {
		t.Errorf("read-only database not reported with a fix: %+v", res)
	}
}

func TestCheckDBReportsABusyDatabaseAsOKNotReadOnly(t *testing.T) {
	env := newTestEnv(t, "", "")
	if res := checkDB(context.Background(), env.deps.Doctor); res.Status != statusOK {
		t.Fatalf("fresh database: %+v", res)
	}
	dbPath, _ := paths.DBPath()
	holder, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Close() }()
	ctx := context.Background()
	var res checkResult
	// An open write transaction holds the lock, as the running agent does.
	err = holder.InTx(ctx, func(tx store.Tx) error {
		if err := tx.AddOverride(ctx, event.Override{Start: time.Now(), End: time.Now().Add(time.Minute), Discard: true}); err != nil {
			return err
		}
		res = checkDB(ctx, env.deps.Doctor)
		return errors.New("roll back")
	})
	if err == nil {
		t.Fatal("expected the rollback error")
	}
	if res.Status != statusOK || !strings.Contains(res.Detail, "in use by another process") {
		t.Errorf("busy database = %+v, want OK with a note", res)
	}
	if strings.Contains(res.Detail+res.Hint, "chmod") {
		t.Errorf("busy database must not get the read-only hint: %+v", res)
	}
}
