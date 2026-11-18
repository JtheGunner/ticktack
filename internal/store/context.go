package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"
)

// CarrySources names the event sources CarriedContext tells apart. The store
// does not know the sessionizer, so the caller passes them in.
type CarrySources struct {
	// Manual is the source of manual switches.
	Manual string
	// AppHint is the source of window-title hints. A hint of that source
	// that names an app only applies while the app is frontmost, so it is
	// not carried.
	AppHint string
}

const (
	manualContextCondition = `source = ?`
	// automaticContextCondition matches a non-manual context event that
	// carries a signal and is not an app-bound hint.
	automaticContextCondition = `source <> ? AND (repo <> '' OR branch <> '' OR ticket <> '')
		 AND NOT (source = ? AND app <> '')`
)

// CarriedContext returns the context in effect just before a moment: the last
// manual context event and the last automatic context event with At < before,
// ordered by time. Either may be missing, so the result holds at most two
// events. Presence and gap events are never returned.
//
// The manual event is returned even when it clears the pin (empty ticket), so
// the caller can tell that no pin is in effect. The automatic event is the
// latest non-manual context event that carries a repo, branch or ticket and
// is not an app-bound hint.
func (s *Store) CarriedContext(ctx context.Context, before time.Time, src CarrySources) ([]event.Event, error) {
	limit := before.UTC().UnixNano()
	manual, hasManual, err := s.lastContext(ctx, manualContextCondition, limit, src.Manual)
	if err != nil {
		return nil, err
	}
	automatic, hasAutomatic, err := s.lastContext(ctx, automaticContextCondition, limit, src.Manual, src.AppHint)
	if err != nil {
		return nil, err
	}
	var out []event.Event
	if hasManual {
		out = append(out, manual)
	}
	if hasAutomatic {
		out = append(out, automatic)
	}
	if len(out) == 2 && eventBefore(out[1], out[0]) {
		out[0], out[1] = out[1], out[0]
	}
	return out, nil
}

func eventBefore(a, b event.Event) bool {
	if !a.At.Equal(b.At) {
		return a.At.Before(b.At)
	}
	return a.ID < b.ID
}

// lastContext returns the latest context event before limit that matches
// condition, which must be one of the constants of this file and never user
// input.
func (s *Store) lastContext(ctx context.Context, condition string, limit int64, args ...any) (event.Event, bool, error) {
	query := `SELECT id, at, source, kind, repo, branch, ticket, app, idle_seconds, COALESCE(dedup_key, '')
		 FROM events WHERE kind = ? AND at < ? AND ` + condition + `
		 ORDER BY at DESC, id DESC LIMIT 1`
	params := append([]any{string(event.KindContext), limit}, args...)
	var e event.Event
	var at int64
	var kind string
	err := s.db.QueryRowContext(ctx, query, params...).
		Scan(&e.ID, &at, &e.Source, &kind, &e.Repo, &e.Branch, &e.Ticket, &e.App, &e.IdleSeconds, &e.Key)
	if errors.Is(err, sql.ErrNoRows) {
		return event.Event{}, false, nil
	}
	if err != nil {
		return event.Event{}, false, fmt.Errorf("query carried context: %w", err)
	}
	e.At = time.Unix(0, at).UTC()
	e.Kind = event.Kind(kind)
	return e, true, nil
}
