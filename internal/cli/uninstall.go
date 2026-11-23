package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/JtheGunner/ticktack/internal/collector/title"
	"github.com/JtheGunner/ticktack/internal/logfile"
	"github.com/JtheGunner/ticktack/internal/paths"
	"github.com/JtheGunner/ticktack/internal/render"
	"github.com/JtheGunner/ticktack/internal/token"
	"github.com/JtheGunner/ticktack/launchd"
)

// ErrUninstallIncomplete is returned by `uninstall` when at least one step
// failed. The failures are already explained on stdout.
var ErrUninstallIncomplete = errors.New("uninstall incomplete")

// uninstallDeps holds everything uninstall touches outside of pure logic.
type uninstallDeps struct {
	Home string
	// ZDotDir is the value of $ZDOTDIR, where `init` put the zsh hook.
	ZDotDir string
	GOOS    string
	UID     int
	// Launchctl runs launchctl with args and returns its combined output.
	Launchctl func(ctx context.Context, args ...string) (string, error)
	// DeleteToken removes the token from the keychain; token.ErrNotSet means
	// there was none.
	DeleteToken func() error
	LogPath     func() (string, error)
	// TitleStatusPath returns the path of the title probe status file.
	TitleStatusPath func() (string, error)
	In              io.Reader
	Out             io.Writer
	// Render styles the output; the zero value renders plain text.
	Render render.Renderer
}

func newUninstallCommand() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: msgUninstallUninstallShort,
		Long:  msgUninstallUninstallLong,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUninstall(cmd.Context(), defaultUninstallDeps(cmd), yes)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, msgUninstallYesFlag)
	return cmd
}

func defaultUninstallDeps(cmd *cobra.Command) uninstallDeps {
	doctor := defaultDoctorDeps(cmd)
	return uninstallDeps{
		Home:        doctor.Home,
		ZDotDir:     doctor.ZDotDir,
		GOOS:        doctor.GOOS,
		UID:         doctor.UID,
		Launchctl:   runLaunchctl,
		DeleteToken: token.Delete,
		LogPath:     paths.LogPath,

		TitleStatusPath: paths.TitleStatusPath,
		In:              cmd.InOrStdin(),
		Out:             cmd.OutOrStdout(),
		Render:          doctor.Render,
	}
}

// uninstallRun carries the state shared by the uninstall steps.
type uninstallRun struct {
	deps uninstallDeps
	// failed is set once any step reported a failure.
	failed bool
}

// say prints one result line behind the gutter symbol of status.
func (r *uninstallRun) say(status render.Status, format string, args ...any) {
	if status == render.StatusFail {
		r.failed = true
	}
	_, _ = fmt.Fprintln(r.deps.Out, r.deps.Render.Gutter(status, fmt.Sprintf(format, args...)))
}

// runUninstall removes what `init` installed after confirmation. Without
// --yes it asks on deps.In; anything but y/yes aborts, and a closed input is
// an error so scripts never remove by accident. A failing step is reported
// and does not stop the remaining steps.
func runUninstall(ctx context.Context, deps uninstallDeps, yes bool) error {
	if deps.Home == "" {
		return errors.New(msgUninstallNoHome)
	}
	r := &uninstallRun{deps: deps}
	if !yes {
		confirmed, err := confirmDeletion(deps.In, deps.Out, uninstallPrompt(deps))
		if err != nil {
			return err
		}
		if !confirmed {
			r.say(render.StatusInfo, "%s", msgUninstallAborted)
			return nil
		}
	}
	r.agentStep(ctx)
	r.hookStep()
	r.tokenStep()
	r.logStep()
	r.titleStatusStep()
	if r.failed {
		r.say(render.StatusWarn, "%s", msgUninstallIncomplete)
		return ErrUninstallIncomplete
	}
	r.say(render.StatusOK, "%s", msgUninstallFinished)
	return nil
}

// uninstallPrompt lists what is about to be removed: the files that exist
// right now and the keychain entry.
func uninstallPrompt(deps uninstallDeps) string {
	lines := []string{msgUninstallPromptIntro}
	if plist := agentPlistPath(deps.Home); fileExists(plist) {
		lines = append(lines, fmt.Sprintf(msgUninstallPromptAgent, SanitizeForTerminal(plist)))
	}
	for _, rc := range rcFileCandidates(deps.Home, deps.ZDotDir) {
		if data, err := os.ReadFile(rc); err == nil && strings.Contains(string(data), rcBlockBegin) {
			lines = append(lines, fmt.Sprintf(msgUninstallPromptHook, SanitizeForTerminal(rc)))
		}
	}
	lines = append(lines, msgUninstallPromptToken)
	if logPath, err := deps.LogPath(); err == nil {
		for _, path := range logfile.Files(logPath) {
			if fileExists(path) {
				lines = append(lines, fmt.Sprintf(msgUninstallPromptLog, SanitizeForTerminal(path)))
			}
		}
	}
	if statusPath, err := deps.TitleStatusPath(); err == nil {
		for _, path := range title.StatusFiles(statusPath) {
			if fileExists(path) {
				lines = append(lines, fmt.Sprintf(msgUninstallPromptStatus, SanitizeForTerminal(path)))
			}
		}
	}
	lines = append(lines, msgUninstallPromptKept)
	return strings.Join(lines, "\n") + "\n" + msgUninstallPromptAsk
}

// agentStep unloads the launchd agent and removes its plist. The plist goes
// even when unloading failed, so the agent does not start at the next login.
func (r *uninstallRun) agentStep(ctx context.Context) {
	if r.deps.GOOS == "darwin" {
		service := fmt.Sprintf("gui/%d/%s", r.deps.UID, launchd.Label)
		printOut, printErr := r.deps.Launchctl(ctx, "print", service)
		switch {
		case printErr == nil:
			if out, err := r.deps.Launchctl(ctx, "bootout", service); err != nil {
				r.say(render.StatusFail, msgUninstallAgentUnloadFailed,
					SanitizeForTerminal(fmt.Sprintf("launchctl bootout: %v: %s", err, out)))
			} else {
				r.say(render.StatusOK, "%s", msgUninstallAgentUnloaded)
			}
		case isServiceNotFound(printOut):
			// Not loaded: nothing to unload.
		default:
			// The state is unknown, so the agent may still be running.
			r.say(render.StatusFail, msgUninstallAgentUnloadFailed,
				SanitizeForTerminal(fmt.Sprintf("launchctl print: %v: %s", printErr, printOut)))
		}
	}
	plist := agentPlistPath(r.deps.Home)
	r.removeFile(plist, msgUninstallPlistRemoved, msgUninstallPlistMissing, msgUninstallPlistFailed)
}

// isServiceNotFound reports whether launchctl output says the service is not
// loaded. launchctl signals that with a non-zero exit whose code differs by
// macOS version (113, 3, 36), so the output text is matched instead:
// "Could not find service ..." or "... No such process".
func isServiceNotFound(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "could not find service") || strings.Contains(lower, "no such process")
}

// hookStep removes the managed block from every shell startup file holding
// one.
func (r *uninstallRun) hookStep() {
	found := false
	for _, rc := range rcFileCandidates(r.deps.Home, r.deps.ZDotDir) {
		removed, err := removeHook(rc)
		if err != nil {
			found = true
			r.say(render.StatusFail, msgUninstallHookFailed, SanitizeForTerminal(err.Error()))
			continue
		}
		if removed {
			found = true
			r.say(render.StatusOK, msgUninstallHookRemoved, SanitizeForTerminal(rc))
		}
	}
	if !found {
		r.say(render.StatusSkip, "%s", msgUninstallHookMissing)
	}
}

// removeHook cuts the managed block out of rc and reports whether the file
// changed. A missing file holds no block.
func removeHook(rc string) (bool, error) {
	existing, err := os.ReadFile(rc)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", rc, err)
	}
	updated, removed, err := removeManagedBlock(string(existing))
	if err != nil {
		return false, fmt.Errorf("%s: %w", rc, err)
	}
	if !removed {
		return false, nil
	}
	if err := writeFileAtomic(rc, []byte(updated), 0o644); err != nil {
		return false, fmt.Errorf("%s: %w", rc, err)
	}
	return true, nil
}

func (r *uninstallRun) tokenStep() {
	err := r.deps.DeleteToken()
	switch {
	case errors.Is(err, token.ErrNotSet):
		r.say(render.StatusSkip, "%s", msgUninstallTokenMissing)
	case err != nil:
		r.say(render.StatusFail, msgUninstallTokenFailed, SanitizeForTerminal(err.Error()))
	default:
		r.say(render.StatusOK, "%s", msgUninstallTokenRemoved)
	}
}

func (r *uninstallRun) logStep() {
	logPath, err := r.deps.LogPath()
	if err != nil {
		r.say(render.StatusFail, msgUninstallLogFailed, SanitizeForTerminal(err.Error()))
		return
	}
	// Backup, throttle state and temp files are reported only when they exist.
	for _, path := range logfile.Files(logPath) {
		if path == logPath {
			r.removeFile(path, msgUninstallLogRemoved, msgUninstallLogMissing, msgUninstallLogFailed)
			continue
		}
		if !fileExists(path) {
			continue
		}
		r.removeFile(path, msgUninstallLogRemoved, msgUninstallLogMissing, msgUninstallLogFailed)
	}
}

// titleStatusStep removes the title probe status file. Stray temp files of an
// interrupted write are reported only when they exist.
func (r *uninstallRun) titleStatusStep() {
	statusPath, err := r.deps.TitleStatusPath()
	if err != nil {
		r.say(render.StatusFail, msgUninstallStatusFailed, SanitizeForTerminal(err.Error()))
		return
	}
	for _, path := range title.StatusFiles(statusPath) {
		if path != statusPath && !fileExists(path) {
			continue
		}
		r.removeFile(path, msgUninstallStatusRemoved, msgUninstallStatusMissing, msgUninstallStatusFailed)
	}
}

// removeFile deletes path and reports the outcome with the given messages. A
// missing file is skipped, not a failure.
func (r *uninstallRun) removeFile(path, removedMsg, missingMsg, failedMsg string) {
	err := os.Remove(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		r.say(render.StatusSkip, missingMsg, SanitizeForTerminal(path))
	case err != nil:
		r.say(render.StatusFail, failedMsg, SanitizeForTerminal(err.Error()))
	default:
		r.say(render.StatusOK, removedMsg, SanitizeForTerminal(path))
	}
}
