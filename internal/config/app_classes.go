package config

import (
	"errors"
	"fmt"
	"sort"
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
// ID to a class. Keys are trimmed and matched exactly and case-sensitively,
// as macOS reports bundle IDs; keys that become equal after trimming, or that
// differ only in case, are rejected so one app never has two classes. Keys
// are visited in sorted order, so the error is stable. The result is nil when
// the table is absent or empty.
func buildAppClasses(raw map[string]string) (map[string]AppClass, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	keys := make([]string, 0, len(raw))
	for key := range raw {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make(map[string]AppClass, len(raw))
	firstByFold := make(map[string]string, len(raw)) // lower-cased ID to the raw key that used it first
	for _, key := range keys {
		id := strings.TrimSpace(key)
		if id == "" {
			return nil, errors.New("app_classes: bundle IDs must not be empty")
		}
		switch class := AppClass(raw[key]); class {
		case ClassWork, ClassNeutral, ClassPersonal:
			out[id] = class
		default:
			return nil, fmt.Errorf("app_classes.%q: unknown class %q (want %q, %q or %q)", key, raw[key], ClassWork, ClassNeutral, ClassPersonal)
		}
		fold := strings.ToLower(id)
		if first, seen := firstByFold[fold]; seen {
			if strings.TrimSpace(first) == id {
				return nil, fmt.Errorf("app_classes: %q and %q are the same bundle ID after trimming spaces", first, key)
			}
			return nil, fmt.Errorf("app_classes: %q and %q differ only in case; list each bundle ID once", first, key)
		}
		firstByFold[fold] = key
	}
	return out, nil
}
