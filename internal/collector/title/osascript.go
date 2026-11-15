package title

import (
	"errors"
	"fmt"
	"strings"
)

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

// parseFrontmostOutput splits the script output into bundle ID and title.
// Errors never contain the output.
func parseFrontmostOutput(out string) (Window, error) {
	out = strings.TrimRight(out, "\r\n")
	app, title, _ := strings.Cut(out, "\n")
	app = strings.TrimSpace(app)
	if app == "" {
		return Window{}, errors.New("frontmost window script returned no bundle identifier")
	}
	return Window{App: app, Title: title}, nil
}

// classifyFailure maps the stderr of a failed osascript run to an error. The
// stderr text is inspected but never copied into the returned error.
func classifyFailure(stderr string, runErr error) error {
	for _, marker := range []string{"-25211", "-1743", "assistive access", "not authorized"} {
		if strings.Contains(stderr, marker) {
			return ErrPermission
		}
	}
	var exit interface{ ExitCode() int }
	if errors.As(runErr, &exit) {
		return fmt.Errorf("read frontmost window: osascript exited with status %d", exit.ExitCode())
	}
	return errors.New("read frontmost window: osascript could not run")
}
