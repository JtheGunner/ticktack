package title

import (
	"errors"
	"fmt"
	"strings"
)

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
