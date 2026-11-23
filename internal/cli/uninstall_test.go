package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/logfile"
	"github.com/JtheGunner/ticktack/internal/paths"
	"github.com/JtheGunner/ticktack/internal/render"
	"github.com/JtheGunner/ticktack/internal/token"
)

const userRCLine = "export EDITOR=vim\n"

// installedEnv runs `init` inside the temp home of newTestEnv, so the
// uninstall tests remove exactly what init installs: plist, rc block, token
// and log, all confined to temp directories, the fake launchctl and the mock
// keychain.
type installedEnv struct {
	*testEnv
	rc, plist, log, config, db string
	out                        *bytes.Buffer
}

func newInstalledEnv(t *testing.T, answers string) (*installedEnv, uninstallDeps) {
	t.Helper()
	env := newTestEnv(t, "", "")
	rc := filepath.Join(env.home, ".zshrc")
	if err := os.WriteFile(rc, []byte(userRCLine), 0o644); err != nil {
		t.Fatal(err)
	}
	// The token step is skipped because it would validate the token over the
	// network; the token goes into the mock keychain the way init stores it.
	var initOut bytes.Buffer
	opts := initOptions{Yes: true, URL: "https://yt.example.com", SkipToken: true}
	if err := runInit(context.Background(), env.deps, opts, &initOut); err != nil {
		t.Fatal(err)
	}
	if err := token.Set(testSecret); err != nil {
		t.Fatal(err)
	}
	logPath, err := paths.LogPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte("agent: started\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgPath, err := config.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	dbPath, err := paths.DBPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dbPath, []byte("db"), 0o600); err != nil {
		t.Fatal(err)
	}
	installed := &installedEnv{
		testEnv: env, rc: rc, plist: agentPlistPath(env.home), log: logPath,
		config: cfgPath, db: dbPath, out: &bytes.Buffer{},
	}
	if !hasManagedBlock(readFile(t, rc)) || !fileExists(installed.plist) || !env.fl.loaded {
		t.Fatalf("init did not install hook, plist and agent:\n%s", initOut.String())
	}
	if _, err := token.Get(); err != nil {
		t.Fatalf("init did not store the token: %v", err)
	}
	env.fl.calls = nil
	deps := uninstallDeps{
		Home:        env.home,
		GOOS:        "darwin",
		UID:         501,
		Launchctl:   env.fl.run,
		DeleteToken: token.Delete,
		LogPath:     paths.LogPath,

		TitleStatusPath: paths.TitleStatusPath,
		In:              strings.NewReader(answers),
		Out:             installed.out,
		Render:          render.Plain(),
	}
	return installed, deps
}

// assertInstalled fails unless everything init set up is still in place.
func (e *installedEnv) assertInstalled(t *testing.T) {
	t.Helper()
	if !hasManagedBlock(readFile(t, e.rc)) {
		t.Error("the shell hook block is gone")
	}
	for _, path := range []string{e.plist, e.log} {
		if !fileExists(path) {
			t.Errorf("%s is gone", path)
		}
	}
	if got, err := token.Get(); err != nil || got != testSecret {
		t.Errorf("token = %v, want it untouched", err)
	}
	if !e.fl.loaded {
		t.Error("the agent was unloaded")
	}
}

func TestUninstallRemovesWhatInitInstalled(t *testing.T) {
	env, deps := newInstalledEnv(t, "")
	if err := runUninstall(context.Background(), deps, true); err != nil {
		t.Fatalf("uninstall: %v\n%s", err, env.out)
	}
	for _, path := range []string{env.plist, env.log} {
		if fileExists(path) {
			t.Errorf("%s still exists", path)
		}
	}
	if got := readFile(t, env.rc); got != userRCLine {
		t.Errorf("rc file = %q, want only the user's own line %q", got, userRCLine)
	}
	if _, err := token.Get(); !errors.Is(err, token.ErrNotSet) {
		t.Errorf("token after uninstall: %v, want ErrNotSet", err)
	}
	wantCalls := []string{"print gui/501/dev.ticktack.agent", "bootout gui/501/dev.ticktack.agent"}
	if strings.Join(env.fl.calls, "|") != strings.Join(wantCalls, "|") {
		t.Errorf("launchctl sequence = %v, want %v", env.fl.calls, wantCalls)
	}
	if env.fl.loaded {
		t.Error("the agent is still loaded")
	}
	for _, path := range []string{env.config, env.db} {
		if !fileExists(path) {
			t.Errorf("%s was removed; uninstall must leave config and data alone", path)
		}
	}
	if strings.Contains(env.out.String(), testSecret) {
		t.Errorf("output leaks the token:\n%s", env.out)
	}
}

func TestUninstallAsksAndListsWhatItRemoves(t *testing.T) {
	t.Run("the question names every item", func(t *testing.T) {
		env, deps := newInstalledEnv(t, "n\n")
		if err := runUninstall(context.Background(), deps, false); err != nil {
			t.Fatal(err)
		}
		out := env.out.String()
		for _, want := range []string{env.plist, env.rc, env.log, "keychain", "[y/N]"} {
			if !strings.Contains(out, want) {
				t.Errorf("question does not mention %q:\n%s", want, out)
			}
		}
		if !strings.Contains(out, msgUninstallAborted) {
			t.Errorf("no abort message:\n%s", out)
		}
		env.assertInstalled(t)
		if len(env.fl.calls) != 0 {
			t.Errorf("launchctl ran before confirmation: %v", env.fl.calls)
		}
	})
	t.Run("an empty answer removes nothing", func(t *testing.T) {
		env, deps := newInstalledEnv(t, "\n")
		if err := runUninstall(context.Background(), deps, false); err != nil {
			t.Fatal(err)
		}
		env.assertInstalled(t)
	})
	t.Run("yes removes", func(t *testing.T) {
		env, deps := newInstalledEnv(t, "yes\n")
		if err := runUninstall(context.Background(), deps, false); err != nil {
			t.Fatal(err)
		}
		if fileExists(env.plist) || fileExists(env.log) || hasManagedBlock(readFile(t, env.rc)) {
			t.Errorf("uninstall left files behind:\n%s", env.out)
		}
	})
	t.Run("closed input is an error and removes nothing", func(t *testing.T) {
		env, deps := newInstalledEnv(t, "")
		err := runUninstall(context.Background(), deps, false)
		if err == nil || !strings.Contains(err.Error(), "--yes") {
			t.Fatalf("err = %v, want a hint about --yes", err)
		}
		env.assertInstalled(t)
	})
}

func TestUninstallTwiceIsSafe(t *testing.T) {
	env, deps := newInstalledEnv(t, "")
	ctx := context.Background()
	if err := runUninstall(ctx, deps, true); err != nil {
		t.Fatal(err)
	}
	env.out.Reset()
	env.fl.calls = nil
	if err := runUninstall(ctx, deps, true); err != nil {
		t.Fatalf("second uninstall: %v\n%s", err, env.out)
	}
	if got := readFile(t, env.rc); got != userRCLine {
		t.Errorf("rc file changed on the second run: %q", got)
	}
	for _, call := range env.fl.calls {
		if strings.HasPrefix(call, "bootout") {
			t.Errorf("bootout of an agent that is not loaded: %v", env.fl.calls)
		}
	}
}

func TestUninstallRemovesTheBlockFromEveryShell(t *testing.T) {
	env, deps := newInstalledEnv(t, "")
	block := renderManagedBlock("ticktack hook")
	others := []string{
		filepath.Join(env.home, ".bashrc"),
		filepath.Join(env.home, ".bash_profile"),
		filepath.Join(env.home, ".config", "fish", "config.fish"),
	}
	for _, path := range others {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("before\n\n"+block+"after\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := runUninstall(context.Background(), deps, true); err != nil {
		t.Fatal(err)
	}
	for _, path := range others {
		if got, want := readFile(t, path), "before\n\nafter\n"; got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
}

func TestUninstallRemovesTheBlockFromZDOTDIRAndEveryBashLoginFile(t *testing.T) {
	env, deps := newInstalledEnv(t, "")
	zdot := filepath.Join(env.home, "zdot")
	deps.ZDotDir = zdot
	block := renderManagedBlock("ticktack hook")
	rcFiles := []string{
		filepath.Join(zdot, ".zshrc"),
		filepath.Join(env.home, ".bash_login"),
		filepath.Join(env.home, ".profile"),
	}
	for _, path := range rcFiles {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("before\n\n"+block+"after\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := runUninstall(context.Background(), deps, true); err != nil {
		t.Fatal(err)
	}
	for _, path := range rcFiles {
		if got, want := readFile(t, path), "before\n\nafter\n"; got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
}

func TestUninstallKeepsGoingAndFailsOnABrokenStep(t *testing.T) {
	t.Run("unterminated block", func(t *testing.T) {
		env, deps := newInstalledEnv(t, "")
		broken := userRCLine + rcBlockBegin + "\nticktack hook\n"
		if err := os.WriteFile(env.rc, []byte(broken), 0o644); err != nil {
			t.Fatal(err)
		}
		err := runUninstall(context.Background(), deps, true)
		if !errors.Is(err, ErrUninstallIncomplete) {
			t.Fatalf("err = %v, want ErrUninstallIncomplete\n%s", err, env.out)
		}
		if got := readFile(t, env.rc); got != broken {
			t.Errorf("a broken rc file was rewritten: %q", got)
		}
		if fileExists(env.plist) || fileExists(env.log) {
			t.Error("the remaining steps did not run")
		}
		if _, err := token.Get(); !errors.Is(err, token.ErrNotSet) {
			t.Errorf("token step did not run: %v", err)
		}
	})
	t.Run("keychain failure", func(t *testing.T) {
		env, deps := newInstalledEnv(t, "")
		deps.DeleteToken = func() error { return errors.New("keychain locked") }
		err := runUninstall(context.Background(), deps, true)
		if !errors.Is(err, ErrUninstallIncomplete) {
			t.Fatalf("err = %v, want ErrUninstallIncomplete", err)
		}
		if !strings.Contains(env.out.String(), "keychain locked") {
			t.Errorf("the failure is not explained:\n%s", env.out)
		}
		if fileExists(env.log) {
			t.Error("the log step did not run after the token failure")
		}
	})
	t.Run("bootout failure still removes the plist", func(t *testing.T) {
		env, deps := newInstalledEnv(t, "")
		deps.Launchctl = func(_ context.Context, args ...string) (string, error) {
			if args[0] == "bootout" {
				return "Boot-out failed", errors.New("exit status 5")
			}
			return "", nil
		}
		err := runUninstall(context.Background(), deps, true)
		if !errors.Is(err, ErrUninstallIncomplete) {
			t.Fatalf("err = %v, want ErrUninstallIncomplete", err)
		}
		if fileExists(env.plist) {
			t.Error("the plist stayed, so the agent would start again at the next login")
		}
	})
}

func TestUninstallAgentStepReadsLaunchctlPrintResult(t *testing.T) {
	tests := []struct {
		name        string
		printOut    string
		printErr    error
		wantFail    bool
		wantBootout bool
	}{
		{name: "service not found by text", printOut: `Could not find service "dev.ticktack.agent" in domain for user gui: 501`, printErr: errors.New("exit status 113")},
		{name: "no such process", printOut: "Boot-out failed: 3: No such process", printErr: errors.New("exit status 3")},
		{name: "other error fails", printOut: "Operation not permitted", printErr: errors.New("exit status 1"), wantFail: true},
		{name: "error without output fails", printErr: errors.New("signal: killed"), wantFail: true},
		{name: "loaded service is booted out", wantBootout: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, deps := newInstalledEnv(t, "")
			var calls []string
			deps.Launchctl = func(_ context.Context, args ...string) (string, error) {
				calls = append(calls, args[0])
				if args[0] == "print" {
					return tt.printOut, tt.printErr
				}
				return "", nil
			}
			err := runUninstall(context.Background(), deps, true)
			if tt.wantFail {
				if !errors.Is(err, ErrUninstallIncomplete) {
					t.Fatalf("err = %v, want ErrUninstallIncomplete\n%s", err, env.out)
				}
			} else if err != nil {
				t.Fatalf("err = %v, want success\n%s", err, env.out)
			}
			if gotBootout := strings.Contains(strings.Join(calls, " "), "bootout"); gotBootout != tt.wantBootout {
				t.Errorf("bootout called = %v, want %v (calls %v)", gotBootout, tt.wantBootout, calls)
			}
			if fileExists(env.plist) {
				t.Error("the plist stayed, so the agent would start again at the next login")
			}
		})
	}
}

func TestUninstallRemovesEveryLogFile(t *testing.T) {
	t.Run("log, backup, state and temp files", func(t *testing.T) {
		env, deps := newInstalledEnv(t, "")
		files := logfile.Files(env.log)
		for _, path := range files {
			if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := runUninstall(context.Background(), deps, true); err != nil {
			t.Fatalf("uninstall: %v\n%s", err, env.out)
		}
		for _, path := range files {
			if fileExists(path) {
				t.Errorf("%s still exists", path)
			}
		}
	})
	t.Run("only some exist", func(t *testing.T) {
		env, deps := newInstalledEnv(t, "")
		if err := os.WriteFile(env.log+".throttle", []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := runUninstall(context.Background(), deps, true); err != nil {
			t.Fatalf("uninstall: %v\n%s", err, env.out)
		}
		if fileExists(env.log) || fileExists(env.log+".throttle") {
			t.Error("log files left behind")
		}
	})
	t.Run("the question lists the files that exist", func(t *testing.T) {
		env, deps := newInstalledEnv(t, "n\n")
		if err := os.WriteFile(env.log+".1", []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := runUninstall(context.Background(), deps, false); err != nil {
			t.Fatal(err)
		}
		out := env.out.String()
		if !strings.Contains(out, env.log+".1") {
			t.Errorf("question does not mention the backup:\n%s", out)
		}
		if strings.Contains(out, env.log+".throttle") {
			t.Errorf("question mentions a file that does not exist:\n%s", out)
		}
	})
	t.Run("one failing removal is reported and the rest goes", func(t *testing.T) {
		env, deps := newInstalledEnv(t, "")
		// A non-empty directory cannot be removed with os.Remove.
		stuck := env.log + ".1"
		if err := os.MkdirAll(filepath.Join(stuck, "child"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(env.log+".throttle", []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		err := runUninstall(context.Background(), deps, true)
		if !errors.Is(err, ErrUninstallIncomplete) {
			t.Fatalf("err = %v, want ErrUninstallIncomplete\n%s", err, env.out)
		}
		if fileExists(env.log) || fileExists(env.log+".throttle") {
			t.Error("the other log files were not removed")
		}
		if !fileExists(stuck) {
			t.Error("the stuck path vanished unexpectedly")
		}
	})
}

func TestUninstallRemovesTheTitleStatusFile(t *testing.T) {
	statusPath := func(t *testing.T) string {
		t.Helper()
		p, err := paths.TitleStatusPath()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		return p
	}
	t.Run("removed together with stray temp files", func(t *testing.T) {
		env, deps := newInstalledEnv(t, "")
		status := statusPath(t)
		stray := filepath.Join(filepath.Dir(status), ".title-status-987654")
		unrelated := filepath.Join(filepath.Dir(status), "keep-me.txt")
		for _, p := range []string{status, stray, unrelated} {
			if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := runUninstall(context.Background(), deps, true); err != nil {
			t.Fatalf("uninstall: %v\n%s", err, env.out)
		}
		if fileExists(status) || fileExists(stray) {
			t.Error("the status file or its temp file is left behind")
		}
		if !fileExists(unrelated) {
			t.Error("a file outside the writer's names was removed")
		}
	})
	t.Run("the question lists it only when it exists", func(t *testing.T) {
		env, deps := newInstalledEnv(t, "n\n")
		status := statusPath(t)
		if err := runUninstall(context.Background(), deps, false); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(env.out.String(), status) {
			t.Errorf("question mentions a missing status file:\n%s", env.out)
		}
		env, deps = newInstalledEnv(t, "n\n")
		status = statusPath(t)
		if err := os.WriteFile(status, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := runUninstall(context.Background(), deps, false); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(env.out.String(), status) {
			t.Errorf("question does not mention the status file:\n%s", env.out)
		}
		if !fileExists(status) {
			t.Error("declining removed the status file")
		}
	})
	t.Run("a missing file is a skip", func(t *testing.T) {
		env, deps := newInstalledEnv(t, "")
		if err := runUninstall(context.Background(), deps, true); err != nil {
			t.Fatalf("uninstall: %v\n%s", err, env.out)
		}
		if !strings.Contains(env.out.String(), "no title status file") {
			t.Errorf("the missing status file is not reported as a skip:\n%s", env.out)
		}
	})
	t.Run("a failing removal ends incomplete", func(t *testing.T) {
		env, deps := newInstalledEnv(t, "")
		status := statusPath(t)
		if err := os.MkdirAll(filepath.Join(status, "child"), 0o700); err != nil {
			t.Fatal(err)
		}
		err := runUninstall(context.Background(), deps, true)
		if !errors.Is(err, ErrUninstallIncomplete) {
			t.Fatalf("err = %v, want ErrUninstallIncomplete\n%s", err, env.out)
		}
		if fileExists(env.log) {
			t.Error("the other steps did not run")
		}
	})
	t.Run("a path error ends incomplete", func(t *testing.T) {
		env, deps := newInstalledEnv(t, "")
		deps.TitleStatusPath = func() (string, error) { return "", errors.New("no state dir") }
		err := runUninstall(context.Background(), deps, true)
		if !errors.Is(err, ErrUninstallIncomplete) {
			t.Fatalf("err = %v, want ErrUninstallIncomplete\n%s", err, env.out)
		}
	})
}

func TestUninstallOffMacSkipsLaunchctl(t *testing.T) {
	env, deps := newInstalledEnv(t, "")
	deps.GOOS = "linux"
	if err := os.Remove(env.plist); err != nil {
		t.Fatal(err)
	}
	if err := runUninstall(context.Background(), deps, true); err != nil {
		t.Fatal(err)
	}
	if len(env.fl.calls) != 0 {
		t.Errorf("launchctl called off macOS: %v", env.fl.calls)
	}
	if hasManagedBlock(readFile(t, env.rc)) || fileExists(env.log) {
		t.Error("hook or log left behind off macOS")
	}
}

func TestUninstallCommandIsRegistered(t *testing.T) {
	cmd, _, err := NewRootCommand().Find([]string{"uninstall"})
	if err != nil || cmd.Name() != "uninstall" {
		t.Fatalf("command not registered (err=%v)", err)
	}
	if cmd.Flags().Lookup("yes") == nil {
		t.Error("uninstall has no --yes flag")
	}
}

func TestRemoveManagedBlock(t *testing.T) {
	block := renderManagedBlock("ticktack hook")
	tests := []struct {
		name    string
		content string
		want    string
		removed bool
		wantErr bool
	}{
		{name: "no block", content: "a\n", want: "a\n"},
		{name: "empty file", content: "", want: ""},
		{name: "only the block", content: block, want: "", removed: true},
		{name: "appended by init", content: "a\n\n" + block, want: "a\n", removed: true},
		{name: "block in the middle", content: "a\n" + block + "b\n", want: "a\nb\n", removed: true},
		{name: "blank line before later content stays", content: "a\n\n" + block + "b\n", want: "a\n\nb\n", removed: true},
		{name: "end marker without newline", content: "a\n" + strings.TrimSuffix(block, "\n"), want: "a\n", removed: true},
		{name: "begin without end", content: "a\n" + rcBlockBegin + "\nhook\n", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, removed, err := removeManagedBlock(tt.content)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got != tt.want || removed != tt.removed {
				t.Errorf("got %q (removed %v), want %q (removed %v)", got, removed, tt.want, tt.removed)
			}
		})
	}
}

// Removing the block is the exact reverse of init's upsert.
func TestRemoveManagedBlockReversesUpsert(t *testing.T) {
	for _, original := range []string{"", "a\n", "a", "a\n\n"} {
		installed, err := upsertManagedBlock(original, renderManagedBlock("ticktack hook"))
		if err != nil {
			t.Fatal(err)
		}
		got, removed, err := removeManagedBlock(installed)
		if err != nil || !removed {
			t.Fatalf("remove from %q: removed %v, err %v", installed, removed, err)
		}
		want := original
		if want != "" && !strings.HasSuffix(want, "\n") {
			want += "\n" // init terminates the last line before appending
		}
		if got != want {
			t.Errorf("original %q: after install and uninstall %q, want %q", original, got, want)
		}
	}
}
