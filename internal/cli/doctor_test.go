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
	"github.com/JtheGunner/ticktack/launchd"
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
	if !strings.Contains(text, "7 of 15 checks failed.") {
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
	installTestAgent(t, env)
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
	if !strings.Contains(text, "x  token:") || !strings.Contains(text, "1 of 15 checks failed.") {
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
	if strings.Contains(out.String(), testSecret) || !strings.Contains(out.String(), "All 15 checks passed.") {
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
	if res.Status != statusWarn || !strings.Contains(res.Detail, "no shell event") || res.Hint == "" {
		t.Errorf("block present, no event: %+v", res)
	}

	dbPath, _ := paths.DBPath()
	seedEvents(t, dbPath, event.Event{At: time.Now().Add(-48 * time.Hour), Source: "shell", Kind: event.KindPresence})
	if res := checkHook(context.Background(), env.deps.Doctor); res.Status != statusWarn {
		t.Errorf("a quiet day must warn, not fail: %+v", res)
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

// installTestAgent creates a stand-in binary under the test home, makes it the
// running binary of the doctor and writes a plist that runs it. It returns the
// path of the binary.
func installTestAgent(t *testing.T, env *testEnv) string {
	t.Helper()
	binary := filepath.Join(env.home, "bin", "ticktack")
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	env.deps.Doctor.Binary = func() (string, error) { return binary, nil }
	writeTestPlist(t, env, binary)
	return binary
}

// writeTestPlist writes the agent plist of the test home with program as the
// program to run.
func writeTestPlist(t *testing.T, env *testEnv, program string) {
	t.Helper()
	rendered, err := launchd.RenderAgentPlist(launchd.Params{Binary: program, LogPath: filepath.Join(env.home, "log")})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(agentPlistPath(env.home)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agentPlistPath(env.home), []byte(rendered), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCheckAgentVerifiesThePlistProgram(t *testing.T) {
	tests := []struct {
		name string
		// arrange changes the installed agent and returns the doctor to run.
		arrange    func(t *testing.T, env *testEnv, binary string)
		wantStatus checkStatus
		wantDetail string
		wantHint   string
	}{
		{
			name:       "program is the running binary",
			arrange:    func(*testing.T, *testEnv, string) {},
			wantStatus: statusOK,
		},
		{
			name: "program is a symlink to the running binary",
			arrange: func(t *testing.T, env *testEnv, binary string) {
				link := filepath.Join(env.home, "opt-link")
				if err := os.Symlink(binary, link); err != nil {
					t.Fatal(err)
				}
				writeTestPlist(t, env, link)
			},
			wantStatus: statusOK,
		},
		{
			name: "program does not exist",
			arrange: func(t *testing.T, env *testEnv, _ string) {
				writeTestPlist(t, env, filepath.Join(env.home, "Cellar", "ticktack", "0.3.4", "bin", "ticktack"))
			},
			wantStatus: statusFail,
			wantDetail: "does not exist",
			wantHint:   "ticktack init",
		},
		{
			name: "dangling symlink counts as missing",
			arrange: func(t *testing.T, env *testEnv, _ string) {
				link := filepath.Join(env.home, "dangling")
				if err := os.Symlink(filepath.Join(env.home, "gone"), link); err != nil {
					t.Fatal(err)
				}
				writeTestPlist(t, env, link)
			},
			wantStatus: statusFail,
			wantDetail: "does not exist",
			wantHint:   "ticktack init",
		},
		{
			name: "program is another binary",
			arrange: func(t *testing.T, env *testEnv, _ string) {
				other := filepath.Join(env.home, "other-ticktack")
				if err := os.WriteFile(other, []byte("old"), 0o755); err != nil {
					t.Fatal(err)
				}
				writeTestPlist(t, env, other)
			},
			wantStatus: statusFail,
			wantDetail: "not the binary you are running",
			wantHint:   "ticktack init",
		},
		{
			name: "plist without a program",
			arrange: func(t *testing.T, env *testEnv, _ string) {
				if err := os.WriteFile(agentPlistPath(env.home), []byte("plist"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			wantStatus: statusFail,
			wantDetail: "cannot be read",
			wantHint:   "ticktack init",
		},
		{
			name: "running binary unknown",
			arrange: func(_ *testing.T, env *testEnv, _ string) {
				env.deps.Doctor.Binary = func() (string, error) { return "", errors.New("no executable") }
			},
			wantStatus: statusFail,
			wantDetail: "no executable",
			wantHint:   "ticktack init",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newTestEnv(t, "", "")
			binary := installTestAgent(t, env)
			dbPath, _ := paths.DBPath()
			seedEvents(t, dbPath, event.Event{At: time.Now().Add(-5 * time.Second), Source: "presence", Kind: event.KindPresence})
			tt.arrange(t, env, binary)

			res := checkAgent(context.Background(), env.deps.Doctor)
			if res.Status != tt.wantStatus {
				t.Fatalf("status = %v, want %v: %+v", res.Status, tt.wantStatus, res)
			}
			if !strings.Contains(res.Detail, tt.wantDetail) {
				t.Errorf("detail %q lacks %q", res.Detail, tt.wantDetail)
			}
			if !strings.Contains(res.Hint, tt.wantHint) {
				t.Errorf("hint %q lacks %q", res.Hint, tt.wantHint)
			}
		})
	}
}

func TestCheckAgentNeedsRecentPresenceSample(t *testing.T) {
	env := newTestEnv(t, "", "")
	installTestAgent(t, env)
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

func TestDoctorTreatsPausedTrackingAsNoFailure(t *testing.T) {
	tests := []struct {
		name  string
		check func(context.Context, doctorDeps) checkResult
		setup func(t *testing.T, env *testEnv)
	}{
		{
			name:  "agent",
			check: checkAgent,
			setup: func(t *testing.T, env *testEnv) { installTestAgent(t, env) },
		},
		{
			name:  "hook",
			check: checkHook,
			setup: func(t *testing.T, env *testEnv) {
				script, _ := shellhook.Script("zsh")
				if err := os.WriteFile(filepath.Join(env.home, ".zshrc"), []byte(renderManagedBlock(script)), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newTestEnv(t, "", "")
			tt.setup(t, env)
			// Without the flag the missing events are a problem ...
			if res := tt.check(context.Background(), env.deps.Doctor); res.Status == statusOK {
				t.Fatalf("not paused, no events: %+v", res)
			}
			// ... and with the flag they are expected.
			pausePath, _ := paths.PausePath()
			if err := os.MkdirAll(filepath.Dir(pausePath), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(pausePath, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			res := tt.check(context.Background(), env.deps.Doctor)
			if res.Status != statusOK || !strings.Contains(res.Detail, "paused") {
				t.Errorf("paused: %+v", res)
			}
		})
	}
}

func TestCheckAgentStillFailsWithoutSamplesWhenNotPaused(t *testing.T) {
	env := newTestEnv(t, "", "")
	installTestAgent(t, env)
	if res := checkAgent(context.Background(), env.deps.Doctor); res.Status != statusFail {
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
	statusPath, _ := paths.TitleStatusPath()
	if err := (&title.StatusFile{Path: statusPath}).Record(title.ProbeDenied, time.Now()); err != nil {
		t.Fatal(err)
	}

	if res := checkTitleHint(context.Background(), doc); res.Status != statusSkip {
		t.Errorf("no config file: %+v", res)
	}
	writeTitleHintConfig(t, false)
	if res := checkTitleHint(context.Background(), doc); res.Status != statusSkip || !strings.Contains(res.Detail, "disabled") {
		t.Errorf("disabled: %+v", res)
	}
}

func TestCheckTitleHintReportsTheAgentsLastProbe(t *testing.T) {
	now := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		// state and age describe the status file; an empty state means no file.
		state      title.ProbeState
		age        time.Duration
		paused     bool
		garbage    bool
		wantStatus checkStatus
		wantDetail string
		wantHint   string
	}{
		{name: "no status file yet", wantStatus: statusSkip, wantDetail: "not checked yet"},
		{name: "probe worked", state: title.ProbeOK, age: time.Minute, wantStatus: statusOK, wantDetail: "could read window titles 1m0s ago"},
		{name: "probe denied", state: title.ProbeDenied, age: time.Minute, wantStatus: statusFail, wantDetail: "denied the agent", wantHint: "Accessibility"},
		{name: "probe failed", state: title.ProbeFailed, age: time.Minute, wantStatus: statusFail, wantDetail: "read failed", wantHint: "osascript"},
		{name: "stale probe means the agent is not running", state: title.ProbeOK, age: time.Hour, wantStatus: statusWarn, wantDetail: "may not be running", wantHint: "agent"},
		{name: "stale probe while paused is still reported", state: title.ProbeOK, age: time.Hour, paused: true, wantStatus: statusOK, wantDetail: "could read window titles"},
		{name: "unreadable status file", garbage: true, wantStatus: statusWarn, wantDetail: "unreadable", wantHint: "delete"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newTestEnv(t, "", "")
			writeTitleHintConfig(t, true)
			doc := env.deps.Doctor
			doc.Now = func() time.Time { return now }
			statusPath, _ := paths.TitleStatusPath()
			switch {
			case tt.garbage:
				if err := os.MkdirAll(filepath.Dir(statusPath), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(statusPath, []byte("{"), 0o600); err != nil {
					t.Fatal(err)
				}
			case tt.state != "":
				if err := (&title.StatusFile{Path: statusPath}).Record(tt.state, now.Add(-tt.age)); err != nil {
					t.Fatal(err)
				}
			}
			if tt.paused {
				pausePath, _ := paths.PausePath()
				if err := os.WriteFile(pausePath, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			res := checkTitleHint(context.Background(), doc)
			if res.Status != tt.wantStatus || !strings.Contains(res.Detail, tt.wantDetail) || !strings.Contains(res.Hint, tt.wantHint) {
				t.Errorf("result = %+v, want status %v, detail %q, hint %q", res, tt.wantStatus, tt.wantDetail, tt.wantHint)
			}
		})
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
	binary := installTestAgent(t, env)
	plist := "<plist><dict><key>ProgramArguments</key><array><string>" + binary +
		"</string><string>agent</string><string>--interval</string><string>2m</string></array></dict></plist>"
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
