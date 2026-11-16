//go:build darwin

package title

import (
	"bytes"
	"context"
	"os/exec"
	"time"
)

// callTimeout bounds one osascript call. A first-run permission prompt would
// otherwise block until the user answers it.
const callTimeout = 5 * time.Second

// frontmostScript asks System Events for the bundle ID and the title of the
// frontmost window. It prints the bundle ID, a line feed and the title. A
// frontmost app without windows yields an empty title. Reading a window name
// is what requires the Accessibility permission; System Events itself
// requires the Automation permission.
var frontmostScript = []string{
	`tell application "System Events"`,
	`set frontProc to first application process whose frontmost is true`,
	`set bundleId to bundle identifier of frontProc`,
	`set windowTitle to ""`,
	`if (count of windows of frontProc) > 0 then set windowTitle to name of window 1 of frontProc`,
	`end tell`,
	`return bundleId & linefeed & windowTitle`,
}

// osascriptSource reads the frontmost window through System Events.
//
// It needs two macOS permissions for the process that runs it (the terminal
// or the launchd-started ticktack binary): Automation, to control System
// Events, and Accessibility, to read window names. The less invasive
// alternatives do not expose titles without Screen Recording permission or
// CGO.
type osascriptSource struct{}

// NewSource returns the platform title source.
func NewSource() Source { return osascriptSource{} }

// Frontmost implements Source. The osascript output is returned in memory
// only; failures never carry it.
func (osascriptSource) Frontmost(ctx context.Context) (Window, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	args := make([]string, 0, 2*len(frontmostScript))
	for _, line := range frontmostScript {
		args = append(args, "-e", line)
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "osascript", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return Window{}, classifyFailure(stderr.String(), err)
	}
	return parseFrontmostOutput(stdout.String())
}
