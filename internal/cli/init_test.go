package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/event"
	"github.com/JtheGunner/ticktack/internal/paths"
	"github.com/JtheGunner/ticktack/internal/store"
	"github.com/JtheGunner/ticktack/internal/token"
)

const (
	testSecret = "perm:TOP-SECRET-xyz-123"
	testBinary = "/opt/ticktack/bin/ticktack"
)

// fakeLaunchctl records calls and models whether the job is loaded.
type fakeLaunchctl struct {
	calls  []string
	loaded bool
}

func (f *fakeLaunchctl) run(_ context.Context, args ...string) (string, error) {
	f.calls = append(f.calls, strings.Join(args, " "))
	switch args[0] {
	case "print":
		if !f.loaded {
			return "", errors.New("not loaded")
		}
	case "bootout":
		f.loaded = false
	case "bootstrap":
		f.loaded = true
	}
	return "", nil
}

type testEnv struct {
	home string
	fl   *fakeLaunchctl
	deps initDeps
}

// newTestEnv confines every file, keychain and launchctl access to temp
// directories and fakes.
func newTestEnv(t *testing.T, secret, answers string) *testEnv {
	t.Helper()
	keyring.MockInit()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg-config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "xdg-data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "xdg-state"))
	env := &testEnv{home: home, fl: &fakeLaunchctl{}}
	doc := defaultDoctorDeps()
	doc.Home = home
	doc.GOOS = "darwin"
	doc.UID = 501
	doc.LookPath = func(string) (string, error) { return "/usr/bin/x", nil }
	env.deps = initDeps{
		Doctor:     doc,
		In:         bufio.NewReader(strings.NewReader(answers)),
		ReadSecret: func() (string, error) { return secret, nil },
		SetToken:   token.Set,
		Launchctl:  env.fl.run,
		Binary:     func() (string, error) { return testBinary, nil },
		Getenv:     func(string) string { return "/bin/zsh" },
		LogPath:    paths.LogPath,
	}
	return env
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestInitIsIdempotent(t *testing.T) {
	env := newTestEnv(t, "", "")
	opts := initOptions{Yes: true, URL: "https://yt.example.com", SkipToken: true}
	cfgPath, _ := config.DefaultPath()
	rc := filepath.Join(env.home, ".zshrc")
	plist := agentPlistPath(env.home)

	var out bytes.Buffer
	if err := runInit(context.Background(), env.deps, opts, &out); err != nil {
		t.Fatal(err)
	}
	snapshot := map[string]string{}
	for _, p := range []string{cfgPath, rc, plist} {
		snapshot[p] = readFile(t, p)
	}
	if !strings.Contains(snapshot[rc], rcBlockBegin) || !strings.Contains(snapshot[plist], testBinary) {
		t.Fatalf("first run did not install hook and plist:\n%s\n%s", snapshot[rc], snapshot[plist])
	}
	if _, err := config.Load(cfgPath); err != nil {
		t.Fatalf("written config does not load: %v", err)
	}
	firstCalls := len(env.fl.calls)
	wantFirst := []string{"print gui/501/dev.ticktack.agent", "bootstrap gui/501 " + plist}
	if strings.Join(env.fl.calls, "|") != strings.Join(wantFirst, "|") {
		t.Fatalf("first launchctl sequence = %v", env.fl.calls)
	}

	out.Reset()
	if err := runInit(context.Background(), env.deps, opts, &out); err != nil {
		t.Fatal(err)
	}
	for p, before := range snapshot {
		if got := readFile(t, p); got != before {
			t.Errorf("%s changed on the second run", p)
		}
	}
	if strings.Count(readFile(t, rc), rcBlockBegin) != 1 {
		t.Error("managed block duplicated")
	}
	wantSecond := []string{"print gui/501/dev.ticktack.agent", "bootout gui/501/dev.ticktack.agent", "bootstrap gui/501 " + plist}
	if got := env.fl.calls[firstCalls:]; strings.Join(got, "|") != strings.Join(wantSecond, "|") {
		t.Errorf("second launchctl sequence = %v", got)
	}
	if !strings.Contains(out.String(), "exists, kept") {
		t.Errorf("second run should report the kept config:\n%s", out.String())
	}
	info, err := os.Stat(cfgPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("config mode = %v, %v", info.Mode(), err)
	}
}

func TestInitNeverEchoesToken(t *testing.T) {
	env := newTestEnv(t, testSecret+"\n", "")
	// A failing token setter must not leak the secret through its error path.
	failing := env.deps
	failing.SetToken = func(string) error { return errors.New("keychain locked") }

	var out bytes.Buffer
	opts := initOptions{Yes: true, URL: "https://yt.example.com"}
	if err := runInit(context.Background(), failing, opts, &out); err != nil {
		t.Fatal(err)
	}
	if err := runInit(context.Background(), env.deps, opts, &out); err != nil {
		t.Fatal(err)
	}
	if got, err := token.Get(); err != nil || got != testSecret {
		t.Fatalf("token not stored: %q, %v", got, err)
	}
	_ = filepath.Walk(env.home, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.Contains(readFile(t, path), testSecret) {
			t.Errorf("token found in %s", path)
		}
		return nil
	})
	if strings.Contains(out.String(), testSecret) {
		t.Errorf("token echoed in output:\n%s", out.String())
	}
}

func TestInitValidatesTokenAfterStoringIt(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"login":"jane"}`))
	}))
	t.Cleanup(srv.Close)
	env := newTestEnv(t, testSecret, "")
	var out bytes.Buffer
	opts := initOptions{Yes: true, URL: srv.URL, SkipHook: true, SkipAgent: true}
	if err := runInit(context.Background(), env.deps, opts, &out); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer "+testSecret || !strings.Contains(out.String(), "valid (user jane)") {
		t.Errorf("auth header sent=%v output:\n%s", gotAuth != "", out.String())
	}
	if strings.Contains(out.String(), testSecret) {
		t.Error("token echoed")
	}
}

func TestInitStepsAreSkippableByAnswer(t *testing.T) {
	env := newTestEnv(t, testSecret, "n\nn\nn\nn\n")
	var out bytes.Buffer
	if err := runInit(context.Background(), env.deps, initOptions{}, &out); err != nil {
		t.Fatal(err)
	}
	cfgPath, _ := config.DefaultPath()
	for _, p := range []string{cfgPath, filepath.Join(env.home, ".zshrc"), agentPlistPath(env.home)} {
		if fileExists(p) {
			t.Errorf("%s created despite answer n", p)
		}
	}
	if _, err := token.Get(); !errors.Is(err, token.ErrNotSet) {
		t.Errorf("token stored despite answer n: %v", err)
	}
	if len(env.fl.calls) != 0 {
		t.Errorf("launchctl called: %v", env.fl.calls)
	}
}

func TestInitReportsLaunchctlFailureWithHintAndContinues(t *testing.T) {
	env := newTestEnv(t, "", "")
	env.deps.Launchctl = func(context.Context, ...string) (string, error) {
		return "boom", errors.New("exit status 5")
	}
	var out bytes.Buffer
	opts := initOptions{Yes: true, SkipToken: true, SkipConfig: true}
	if err := runInit(context.Background(), env.deps, opts, &out); err != nil {
		t.Fatalf("launchctl failure must not be fatal: %v", err)
	}
	if !strings.Contains(out.String(), "launchctl bootstrap gui/501") {
		t.Errorf("missing hint:\n%s", out.String())
	}
}

func TestInitKeepsExistingConfig(t *testing.T) {
	env := newTestEnv(t, "", "")
	cfgPath, _ := config.DefaultPath()
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("# mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	opts := initOptions{Yes: true, SkipToken: true, SkipHook: true, SkipAgent: true}
	if err := runInit(context.Background(), env.deps, opts, &out); err != nil {
		t.Fatal(err)
	}
	if readFile(t, cfgPath) != "# mine\n" {
		t.Error("existing config overwritten")
	}
}

func TestInitHookReplacesBlockInPlaceAndKeepsModeAndSymlink(t *testing.T) {
	env := newTestEnv(t, "", "")
	real := filepath.Join(env.home, "dotfiles-zshrc")
	old := "export A=1\n" + rcBlockBegin + "\nold\n" + rcBlockEnd + "\nexport B=2\n"
	if err := os.WriteFile(real, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	rc := filepath.Join(env.home, ".zshrc")
	if err := os.Symlink(real, rc); err != nil {
		t.Fatal(err)
	}
	opts := initOptions{Yes: true, SkipConfig: true, SkipToken: true, SkipAgent: true}
	if err := runInit(context.Background(), env.deps, opts, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, real)
	if !strings.HasPrefix(got, "export A=1\n"+rcBlockBegin+"\n") || !strings.HasSuffix(got, rcBlockEnd+"\nexport B=2\n") || strings.Contains(got, "\nold\n") {
		t.Errorf("block not replaced in place:\n%s", got)
	}
	if info, _ := os.Lstat(rc); info.Mode()&os.ModeSymlink == 0 {
		t.Error("symlink replaced by a regular file")
	}
	if info, _ := os.Stat(real); info.Mode().Perm() != 0o600 {
		t.Errorf("mode changed to %v", info.Mode().Perm())
	}
}

func TestInitHookRefusesBrokenBlock(t *testing.T) {
	env := newTestEnv(t, "", "")
	rc := filepath.Join(env.home, ".zshrc")
	broken := rcBlockBegin + "\nno end marker\n"
	if err := os.WriteFile(rc, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := initOptions{Yes: true, SkipConfig: true, SkipToken: true, SkipAgent: true}
	if err := runInit(context.Background(), env.deps, opts, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if readFile(t, rc) != broken {
		t.Error("file with a broken block was modified")
	}
}

func TestRCFileForPicksBashProfileOnMacOnlyWhenBashrcMissing(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".bash_profile"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got, _ := rcFileFor(home, "darwin", "bash")
	if filepath.Base(got) != ".bash_profile" {
		t.Errorf("got %s", got)
	}
	got, _ = rcFileFor(home, "linux", "bash")
	if filepath.Base(got) != ".bashrc" {
		t.Errorf("got %s", got)
	}
}

func TestInitPrintSnippetOutputsHookScript(t *testing.T) {
	root := NewRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"init", "--print-snippet", "zsh"})
	if err := root.Execute(); err != nil || !strings.Contains(out.String(), "ticktack") {
		t.Fatalf("err=%v out=%q", err, out.String())
	}
	root.SetArgs([]string{"init", "--print-snippet", "tcsh"})
	if err := root.Execute(); err == nil {
		t.Error("unsupported shell accepted")
	}
}

func seedEvents(t *testing.T, dbPath string, events ...event.Event) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	for _, e := range events {
		if err := st.Append(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}
}
