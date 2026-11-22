package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ArchivedDay is a local day whose raw events were deleted by the retention
// cleanup. Such a day is frozen: it is never derived, reviewed or synced
// again.
type ArchivedDay struct {
	Day           string // yyyy-mm-dd
	ArchivedAt    time.Time
	EventsDeleted int
}

// ArchiveRequest names one day for ArchiveDay.
type ArchiveRequest struct {
	// Day is the local day as yyyy-mm-dd.
	Day string
	// From and To bound the local day: events with From <= At < To belong to
	// it. The caller owns the local-day logic.
	From, To time.Time
	// ArchivedAt is the moment the day is archived.
	ArchivedAt time.Time
	// Carry names the sources of carried context. The newest manual and
	// automatic context events before To are kept (see ArchiveDay).
	Carry CarrySources
	// DryRun counts what would be deleted and changes nothing.
	DryRun bool
}

// ArchiveDay is the one place where events are deleted besides Purge: the
// retention cleanup. It deletes the events of one local day and records the
// day in archived_days in a single transaction, so a day is never half
// archived. It is deliberately narrow: it never touches overrides, the
// ledger, sync history or review state, and it refuses a day that is already
// archived.
//
// The newest manual and automatic context events before the end of the day
// stay. The days after it derive their starting context from them (see
// CarriedContext); deleting them would silently change those days. They are
// not counted as deleted.
//
// With DryRun the transaction is rolled back, so the count is exact.
func (s *Store) ArchiveDay(ctx context.Context, req ArchiveRequest) (int, error) {
	if req.Day == "" || !req.From.Before(req.To) {
		return 0, fmt.Errorf("archive day: invalid request for %q", req.Day)
	}
	tx, err := s.root.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin archive day: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	inTx := &Store{db: tx, root: s.root}

	var already int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM archived_days WHERE day = ?`, req.Day).Scan(&already); err != nil {
		return 0, fmt.Errorf("check archived day: %w", err)
	}
	if already > 0 {
		return 0, fmt.Errorf("archive day: %s is already archived", req.Day)
	}
	anchors, err := inTx.CarriedContext(ctx, req.To, req.Carry)
	if err != nil {
		return 0, err
	}
	from, to := req.From.UTC().UnixNano(), req.To.UTC().UnixNano()
	keep := make([]any, 0, len(anchors))
	condition := `at >= ? AND at < ?`
	args := []any{from, to}
	for _, a := range anchors {
		condition += ` AND id <> ?`
		keep = append(keep, a.ID)
	}
	args = append(args, keep...)
	res, err := tx.ExecContext(ctx, `DELETE FROM events WHERE `+condition, args...)
	if err != nil {
		return 0, fmt.Errorf("delete events of %s: %w", req.Day, err)
	}
	deleted, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("delete events of %s: %w", req.Day, err)
	}
	if req.DryRun {
		return int(deleted), nil
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO archived_days (day, start_at, end_at, archived_at, events_deleted) VALUES (?, ?, ?, ?, ?)`,
		req.Day, from, to, req.ArchivedAt.UTC().UnixNano(), deleted); err != nil {
		return 0, fmt.Errorf("record archived day %s: %w", req.Day, err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit archive day: %w", err)
	}
	return int(deleted), nil
}

// ArchivedDay returns the archive record of a day, if it was archived.
func (s *Store) ArchivedDay(ctx context.Context, day string) (ArchivedDay, bool, error) {
	var a ArchivedDay
	var at int64
	err := s.db.QueryRowContext(ctx,
		`SELECT day, archived_at, events_deleted FROM archived_days WHERE day = ?`, day).
		Scan(&a.Day, &at, &a.EventsDeleted)
	if errors.Is(err, sql.ErrNoRows) {
		return ArchivedDay{}, false, nil
	}
	if err != nil {
		return ArchivedDay{}, false, fmt.Errorf("read archived day: %w", err)
	}
	a.ArchivedAt = time.Unix(0, at).UTC()
	return a, true, nil
}

// ArchivedDays returns every archived day, oldest first.
func (s *Store) ArchivedDays(ctx context.Context) ([]ArchivedDay, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT day, archived_at, events_deleted FROM archived_days ORDER BY day`)
	if err != nil {
		return nil, fmt.Errorf("read archived days: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ArchivedDay
	for rows.Next() {
		var a ArchivedDay
		var at int64
		if err := rows.Scan(&a.Day, &at, &a.EventsDeleted); err != nil {
			return nil, fmt.Errorf("scan archived day: %w", err)
		}
		a.ArchivedAt = time.Unix(0, at).UTC()
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate archived days: %w", err)
	}
	return out, nil
}

// EarliestEventAt returns the time of the oldest stored event.
func (s *Store) EarliestEventAt(ctx context.Context) (time.Time, bool, error) {
	var at sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT MIN(at) FROM events`).Scan(&at); err != nil {
		return time.Time{}, false, fmt.Errorf("read earliest event: %w", err)
	}
	if !at.Valid {
		return time.Time{}, false, nil
	}
	return time.Unix(0, at.Int64).UTC(), true, nil
}
