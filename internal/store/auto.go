package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ReviewRecord is what one review session did to one day.
type ReviewRecord struct {
	// ID is zero until the record is saved.
	ID           int64
	Day          string // yyyy-mm-dd
	SessionStart time.Time
	FinishedAt   time.Time
	// Corrections counts changes to time that already had a ticket
	// (reassign, discard, break, split). Confirmations and filling or
	// dropping unassigned time are not corrections.
	Corrections int
	// Confirmed is the number of items this session confirmed; items
	// confirmed in earlier sessions do not count.
	Confirmed int
}

// Digest is the stored summary of one auto run.
type Digest struct {
	ID        int64
	Day       string // yyyy-mm-dd
	CreatedAt time.Time
	Text      string
}

// SaveReviewRecord stores a session's record. A record with ID zero is
// inserted and its new id returned; a record with an ID replaces the changing
// fields of that row. One session thus keeps one row, and sessions never
// overwrite each other.
func (s *Store) SaveReviewRecord(ctx context.Context, r ReviewRecord) (int64, error) {
	if r.ID != 0 {
		_, err := s.db.ExecContext(ctx,
			`UPDATE review_log SET finished_at = ?, corrections = ?, confirmed = ? WHERE id = ?`,
			r.FinishedAt.UTC().UnixNano(), r.Corrections, r.Confirmed, r.ID)
		if err != nil {
			return 0, fmt.Errorf("update review record: %w", err)
		}
		return r.ID, nil
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO review_log (day, started_at, finished_at, corrections, confirmed)
		 VALUES (?, ?, ?, ?, ?)`,
		r.Day, r.SessionStart.UTC().UnixNano(), r.FinishedAt.UTC().UnixNano(), r.Corrections, r.Confirmed)
	if err != nil {
		return 0, fmt.Errorf("save review record: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("save review record: %w", err)
	}
	return id, nil
}

// ReviewRecords returns all review records ordered by day, then by session.
func (s *Store) ReviewRecords(ctx context.Context) ([]ReviewRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, day, started_at, finished_at, corrections, confirmed
		 FROM review_log ORDER BY day, id`)
	if err != nil {
		return nil, fmt.Errorf("query review records: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ReviewRecord
	for rows.Next() {
		var r ReviewRecord
		var started, finished int64
		if err := rows.Scan(&r.ID, &r.Day, &started, &finished, &r.Corrections, &r.Confirmed); err != nil {
			return nil, fmt.Errorf("scan review record: %w", err)
		}
		r.SessionStart = time.Unix(0, started).UTC()
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
