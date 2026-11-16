package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/JtheGunner/ticktack/internal/config"
	"github.com/JtheGunner/ticktack/internal/paths"
	"github.com/JtheGunner/ticktack/internal/shellhook"
	"github.com/JtheGunner/ticktack/internal/token"
	"github.com/JtheGunner/ticktack/launchd"
)

// initOptions are the flags of `ticktack init`.
type initOptions struct {
	Yes        bool
	URL        string
	Shell      string
	SkipConfig bool
	SkipToken  bool
	SkipHook   bool
	SkipAgent  bool
}

// initDeps holds everything init touches outside of pure logic.
type initDeps struct {
	Doctor doctorDeps
	// In supplies answers to prompts.
	In *bufio.Reader
	// ReadSecret reads the token without echoing it.
	ReadSecret func() (string, error)
	SetToken   func(secret string) error
	// Launchctl runs launchctl with args and returns its combined output.
	Launchctl func(ctx context.Context, args ...string) (string, error)
	// Binary returns the absolute path of the running executable.
	Binary  func() (string, error)
	Getenv  func(key string) string
	LogPath func() (string, error)
}

func newInitCommand() *cobra.Command {
	var opts initOptions
	var printSnippet string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Set up config, token, shell hook and launchd agent",
		Long: "Walks through four steps: config file, YouTrack token (hidden input), " +
			"shell hook and launchd agent. Every step is explained and can be skipped; " +
			"rerunning is safe.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if printSnippet != "" {
				script, err := shellhook.Script(printSnippet)
				if err != nil {
					return err
				}
				_, err = io.WriteString(cmd.OutOrStdout(), script)
				return err
			}
			deps := defaultInitDeps(bufio.NewReader(cmd.InOrStdin()))
			return runInit(cmd.Context(), deps, opts, cmd.OutOrStdout())
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&opts.Yes, "yes", "y", false, "accept every step without asking")
	f.StringVar(&opts.URL, "url", "", "YouTrack base URL for the new config file")
	f.StringVar(&opts.Shell, "shell", "", "shell to hook (zsh, bash or fish; default: from $SHELL)")
	f.BoolVar(&opts.SkipConfig, "skip-config", false, "skip the config step")
	f.BoolVar(&opts.SkipToken, "skip-token", false, "skip the token step")
	f.BoolVar(&opts.SkipHook, "skip-hook", false, "skip the shell hook step")
	f.BoolVar(&opts.SkipAgent, "skip-agent", false, "skip the launchd agent step")
	f.StringVar(&printSnippet, "print-snippet", "", "print the hook script for a shell and exit")
	return cmd
}

func defaultInitDeps(in *bufio.Reader) initDeps {
	return initDeps{
		Doctor: defaultDoctorDeps(),
		In:     in,
		ReadSecret: func() (string, error) {
			fd := int(os.Stdin.Fd())
			if term.IsTerminal(fd) {
				b, err := term.ReadPassword(fd)
				return string(b), err
			}
			line, err := in.ReadString('\n')
			if err != nil && !errors.Is(err, io.EOF) {
				return "", err
			}
			return line, nil
		},
		SetToken:  token.Set,
		Launchctl: runLaunchctl,
		Binary:    currentBinary,
		Getenv:    os.Getenv,
		LogPath:   paths.LogPath,
	}
}

func runLaunchctl(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "launchctl", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func currentBinary() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("resolve executable symlinks: %w", err)
	}
	return resolved, nil
}

// initRun carries the state shared by the init steps.
type initRun struct {
	deps initDeps
	opts initOptions
	out  io.Writer
}

func (r *initRun) say(format string, args ...any) {
	_, _ = fmt.Fprintf(r.out, format+"\n", args...)
}

// confirm asks a [Y/n] question. --yes accepts; end of input declines.
func (r *initRun) confirm(question string) bool {
	if r.opts.Yes {
		return true
	}
	_, _ = fmt.Fprintf(r.out, "%s [Y/n] ", question)
	line, err := r.deps.In.ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	if err != nil && answer == "" {
		_, _ = fmt.Fprintln(r.out)
		return false
	}
	return answer == "" || answer == "y" || answer == "yes"
}

// runInit performs the four setup steps. Step failures are reported and do
// not stop the remaining steps.
func runInit(ctx context.Context, deps initDeps, opts initOptions, out io.Writer) error {
	r := &initRun{deps: deps, opts: opts, out: out}
	r.configStep()
	r.tokenStep(ctx)
	r.hookStep()
	r.agentStep(ctx)
	r.say("%s", msgInitFinished)
	return nil
}

func (r *initRun) configStep() {
	path, err := r.deps.Doctor.ConfigPath()
	if err != nil {
		r.say(msgInitConfigFailed, SanitizeForTerminal(err.Error()))
		return
	}
	r.say(msgInitConfigIntro, SanitizeForTerminal(path))
	if r.opts.SkipConfig {
		r.say("%s", msgInitSkipped)
		return
	}
	if fileExists(path) {
		r.say(msgInitConfigKept, SanitizeForTerminal(path))
		return
	}
	if !r.confirm(msgInitConfirm) {
		r.say("%s", msgInitSkipped)
		return
	}
	address := strings.TrimSpace(r.opts.URL)
	if address == "" && !r.opts.Yes {
		_, _ = fmt.Fprint(r.out, msgInitConfigURL)
		line, _ := r.deps.In.ReadString('\n')
		address = strings.TrimSpace(line)
	}
	if address != "" {
		if u, err := url.Parse(address); err != nil || u.Scheme == "" || u.Host == "" {
			r.say(msgInitConfigBadURL, SanitizeForTerminal(address))
			return
		}
	}
	if err := writeNewConfig(path, config.DefaultTOML(address)); err != nil {
		r.say(msgInitConfigFailed, SanitizeForTerminal(err.Error()))
		return
	}
	r.say(msgInitConfigWritten, SanitizeForTerminal(path))
}

// writeNewConfig creates the config file with mode 0600 and never overwrites.
func writeNewConfig(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create config file: %w", err)
	}
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		return fmt.Errorf("write config file: %w", err)
	}
	return f.Close()
}

func (r *initRun) tokenStep(ctx context.Context) {
	r.say("%s", msgInitTokenIntro)
	if r.opts.SkipToken {
		r.say("%s", msgInitSkipped)
		return
	}
	if !r.confirm(msgInitConfirm) {
		r.say("%s", msgInitSkipped)
		return
	}
	_, _ = fmt.Fprint(r.out, msgInitTokenPrompt)
	secret, err := r.deps.ReadSecret()
	_, _ = fmt.Fprintln(r.out)
	secret = strings.TrimSpace(secret)
	if err != nil {
		// The reader error carries no input; the token is never formatted.
		r.say(msgInitTokenFailed, SanitizeForTerminal(err.Error()))
		return
	}
	if secret == "" {
		r.say("%s", msgInitTokenSkipped)
		return
	}
	if err := r.deps.SetToken(secret); err != nil {
		r.say(msgInitTokenFailed, SanitizeForTerminal(err.Error()))
		return
	}
	r.say("%s", msgInitTokenStored)
	if r.confirm(msgInitTokenValidate) {
		printResult(r.out, checkToken(ctx, r.deps.Doctor))
	}
}

func (r *initRun) hookStep() {
	shell := r.opts.Shell
	if shell == "" {
		shell = filepath.Base(r.deps.Getenv("SHELL"))
	}
	script, err := shellhook.Script(shell)
	if err != nil {
		r.say(msgInitHookUnsupported, SanitizeForTerminal(shell))
		return
	}
	rc, err := rcFileFor(r.deps.Doctor.Home, r.deps.Doctor.GOOS, shell)
	if err != nil {
		r.say(msgInitHookFailed, SanitizeForTerminal(err.Error()))
		return
	}
	r.say(msgInitHookIntro, SanitizeForTerminal(rc))
	if r.opts.SkipHook || !r.confirm(msgInitConfirm) {
		r.say("%s", msgInitSkipped)
		return
	}
	changed, err := upsertHook(rc, script)
	if err != nil {
		r.say(msgInitHookFailed, SanitizeForTerminal(err.Error()))
		return
	}
	if changed {
		r.say(msgInitHookWritten, SanitizeForTerminal(rc))
		return
	}
	r.say(msgInitHookUnchanged, SanitizeForTerminal(rc))
}

// upsertHook writes the managed block and reports whether the file changed.
func upsertHook(rc, script string) (bool, error) {
	existing, err := os.ReadFile(rc)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("read %s: %w", rc, err)
	}
	updated, err := upsertManagedBlock(string(existing), renderManagedBlock(script))
	if err != nil {
		return false, fmt.Errorf("%s: %w", rc, err)
	}
	if updated == string(existing) {
		return false, nil
	}
	if err := writeFileAtomic(rc, []byte(updated), 0o644); err != nil {
		return false, fmt.Errorf("%s: %w", rc, err)
	}
	return true, nil
}

func (r *initRun) agentStep(ctx context.Context) {
	plist := agentPlistPath(r.deps.Doctor.Home)
	r.say(msgInitAgentIntro, SanitizeForTerminal(plist))
	if r.opts.SkipAgent {
		r.say("%s", msgInitSkipped)
		return
	}
	if r.deps.Doctor.GOOS != "darwin" {
		r.say("%s", msgInitAgentNotMac)
		return
	}
	if !r.confirm(msgInitConfirm) {
		r.say("%s", msgInitSkipped)
		return
	}
	uid := r.deps.Doctor.UID
	if err := r.installAgent(ctx, plist, uid); err != nil {
		r.say(msgInitAgentFailed, SanitizeForTerminal(err.Error()))
		r.say(msgInitAgentHint, uid, SanitizeForTerminal(plist))
	}
}

func (r *initRun) installAgent(ctx context.Context, plist string, uid int) error {
	binary, err := r.deps.Binary()
	if err != nil {
		return err
	}
	logPath, err := r.deps.LogPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return fmt.Errorf("create log directory: %w", err)
	}
	rendered, err := launchd.RenderAgentPlist(launchd.Params{Binary: binary, LogPath: logPath})
	if err != nil {
		return err
	}
	existing, readErr := os.ReadFile(plist)
	if readErr == nil && string(existing) == rendered {
		r.say(msgInitAgentKept, SanitizeForTerminal(plist))
	} else {
		if err := writeFileAtomic(plist, []byte(rendered), 0o644); err != nil {
			return err
		}
		r.say(msgInitAgentWritten, SanitizeForTerminal(plist))
	}

	domain := fmt.Sprintf("gui/%d", uid)
	service := domain + "/" + launchd.Label
	if _, err := r.deps.Launchctl(ctx, "print", service); err == nil {
		if out, err := r.deps.Launchctl(ctx, "bootout", service); err != nil {
			return fmt.Errorf("launchctl bootout: %w: %s", err, out)
		}
	}
	if out, err := r.deps.Launchctl(ctx, "bootstrap", domain, plist); err != nil {
		return fmt.Errorf("launchctl bootstrap: %w: %s", err, out)
	}
	r.say("%s", msgInitAgentLoaded)
	return nil
}
