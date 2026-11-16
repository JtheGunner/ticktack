package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ReviewRecord is what one review session did to one day. SessionStart
// identifies the session; saving the same session again replaces its record.
type ReviewRecord struct {
	Day          string // yyyy-mm-dd
	SessionStart time.Time
	FinishedAt   time.Time
	// Corrections counts changes to the day's attribution; plain
	// confirmations are not corrections.
	Corrections int
	// Confirmed is the number of confirmed items of the day.
	Confirmed int
}

// Digest is the stored summary of one auto run.
type Digest struct {
	ID        int64
	Day       string // yyyy-mm-dd
	CreatedAt time.Time
	Text      string
}

// SaveReviewRecord stores or replaces the record of one review session.
func (s *Store) SaveReviewRecord(ctx context.Context, r ReviewRecord) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO review_log (day, session_key, finished_at, corrections, confirmed)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (day, session_key) DO UPDATE SET
		   finished_at = excluded.finished_at,
		   corrections = excluded.corrections,
		   confirmed = excluded.confirmed`,
		r.Day, r.SessionStart.UTC().UnixNano(), r.FinishedAt.UTC().UnixNano(), r.Corrections, r.Confirmed)
	if err != nil {
		return fmt.Errorf("save review record: %w", err)
	}
	return nil
}

// ReviewRecords returns all review records ordered by day, then by the time
// the session last changed.
func (s *Store) ReviewRecords(ctx context.Context) ([]ReviewRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT day, session_key, finished_at, corrections, confirmed
		 FROM review_log ORDER BY day, finished_at, id`)
	if err != nil {
		return nil, fmt.Errorf("query review records: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ReviewRecord
	for rows.Next() {
		var r ReviewRecord
		var key, finished int64
		if err := rows.Scan(&r.Day, &key, &finished, &r.Corrections, &r.Confirmed); err != nil {
			return nil, fmt.Errorf("scan review record: %w", err)
		}
		r.SessionStart = time.Unix(0, key).UTC()
		r.FinishedAt = time.Unix(0, finished).UTC()
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate review records: %w", err)
	}
	return out, nil
}

// AddDigest stores the digest of an auto run and returns it with its id.
func (s *Store) AddDigest(ctx context.Context, d Digest) (Digest, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO digests (day, created_at, text) VALUES (?, ?, ?)`,
		d.Day, d.CreatedAt.UTC().UnixNano(), d.Text)
	if err != nil {
		return Digest{}, fmt.Errorf("add digest: %w", err)
	}
	if d.ID, err = res.LastInsertId(); err != nil {
		return Digest{}, fmt.Errorf("add digest: %w", err)
	}
	return d, nil
}

// LastDigest returns the most recent digest.
func (s *Store) LastDigest(ctx context.Context) (Digest, bool, error) {
	var d Digest
	var at int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id, day, created_at, text FROM digests ORDER BY id DESC LIMIT 1`).
		Scan(&d.ID, &d.Day, &at, &d.Text)
	if errors.Is(err, sql.ErrNoRows) {
		return Digest{}, false, nil
	}
	if err != nil {
		return Digest{}, false, fmt.Errorf("read last digest: %w", err)
	}
	d.CreatedAt = time.Unix(0, at).UTC()
	return d, true, nil
}
