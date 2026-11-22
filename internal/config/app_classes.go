package config

import (
	"errors"
	"fmt"
	"strings"
)

// AppClass says how presence in a foreground app counts for time attribution.
type AppClass string

// App classes. An app that is not listed is ClassWork.
const (
	// ClassWork keeps the ticket of the current context at its own tier.
	ClassWork AppClass = "work"
	// ClassNeutral keeps the ticket, but only as tier suggested.
	ClassNeutral AppClass = "neutral"
	// ClassPersonal ends the presence span: the time is not counted.
	ClassPersonal AppClass = "personal"
)

// buildAppClasses validates the [app_classes] table, which maps a macOS bundle
// ID to a class. The result is nil when the table is absent or empty.
func buildAppClasses(raw map[string]string) (map[string]AppClass, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make(map[string]AppClass, len(raw))
	for bundleID, class := range raw {
		id := strings.TrimSpace(bundleID)
		if id == "" {
			return nil, errors.New("app_classes: bundle IDs must not be empty")
		}
		switch c := AppClass(class); c {
		case ClassWork, ClassNeutral, ClassPersonal:
			out[id] = c
		default:
			return nil, fmt.Errorf("app_classes.%q: unknown class %q (want %q, %q or %q)", bundleID, class, ClassWork, ClassNeutral, ClassPersonal)
		}
	}
	return out, nil
}
