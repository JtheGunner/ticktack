package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// LedgerRow is one booked work item: one per (issue, date, type).
type LedgerRow struct {
	Issue       string
	Date        string // yyyy-mm-dd
	Type        string
	WorkItemID  string
	Minutes     int
	ContentHash string
	BookedAt    time.Time
}

// SyncEntry is one change a sync run made, kept so the run can be undone.
type SyncEntry struct {
	Issue      string
	Date       string // yyyy-mm-dd
	Type       string
	Action     string // created, updated, recovered or deleted
	WorkItemID string
	// PreviousMinutes is the booked minutes before an update or a deletion;
	// zero otherwise.
	PreviousMinutes int
	NewMinutes      int
	// Text is the work item text that was sent.
	Text string
	// PreviousText is the booked text before an update, so undo can restore
	// it; empty when it is unknown (entries of earlier versions) or when
	// nothing was replaced.
	PreviousText string
}

// SyncRun is a sync run together with its entries.
type SyncRun struct {
	ID        int64
	StartedAt time.Time
	Entries   []SyncEntry
}

// LedgerGet returns the ledger row for (issue, date, typ).
func (s *Store) LedgerGet(ctx context.Context, issue, date, typ string) (LedgerRow, bool, error) {
	var r LedgerRow
	var booked int64
	err := s.db.QueryRowContext(ctx,
		`SELECT issue, date, type, work_item_id, minutes, content_hash, booked_at
		 FROM ledger WHERE issue = ? AND date = ? AND type = ?`, issue, date, typ).
		Scan(&r.Issue, &r.Date, &r.Type, &r.WorkItemID, &r.Minutes, &r.ContentHash, &booked)
	if errors.Is(err, sql.ErrNoRows) {
		return LedgerRow{}, false, nil
	}
	if err != nil {
		return LedgerRow{}, false, fmt.Errorf("read ledger row: %w", err)
	}
	r.BookedAt = time.Unix(0, booked).UTC()
	return r, true, nil
}

// LedgerRowsForDate returns every ledger row of one day, ordered by issue and
// type.
func (s *Store) LedgerRowsForDate(ctx context.Context, date string) ([]LedgerRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT issue, date, type, work_item_id, minutes, content_hash, booked_at
		 FROM ledger WHERE date = ? ORDER BY issue, type`, date)
	if err != nil {
		return nil, fmt.Errorf("read ledger rows: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []LedgerRow
	for rows.Next() {
		var r LedgerRow
		var booked int64
		if err := rows.Scan(&r.Issue, &r.Date, &r.Type, &r.WorkItemID, &r.Minutes, &r.ContentHash, &booked); err != nil {
			return nil, fmt.Errorf("scan ledger row: %w", err)
		}
		r.BookedAt = time.Unix(0, booked).UTC()
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate ledger rows: %w", err)
	}
	return out, nil
}

// BeginSyncRun starts a new sync run and returns its id.
func (s *Store) BeginSyncRun(ctx context.Context, startedAt time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO sync_runs (started_at) VALUES (?)`, startedAt.UTC().UnixNano())
	if err != nil {
		return 0, fmt.Errorf("begin sync run: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("begin sync run: %w", err)
	}
	return id, nil
}

// RecordBooking upserts the ledger row and stores the run entry in one
// transaction.
func (s *Store) RecordBooking(ctx context.Context, runID int64, row LedgerRow, e SyncEntry) error {
	tx, err := s.root.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin record booking: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := upsertLedger(ctx, tx, row); err != nil {
		return err
	}
	if err := insertSyncEntry(ctx, tx, runID, e); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit record booking: %w", err)
	}
	return nil
}

// RecordDeletion removes the ledger row and stores the run entry in one
// transaction, so the deletion can be undone.
func (s *Store) RecordDeletion(ctx context.Context, runID int64, row LedgerRow, e SyncEntry) error {
	tx, err := s.root.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin record deletion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM ledger WHERE issue = ? AND date = ? AND type = ?`, row.Issue, row.Date, row.Type); err != nil {
		return fmt.Errorf("remove ledger row: %w", err)
	}
	if err := insertSyncEntry(ctx, tx, runID, e); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit record deletion: %w", err)
	}
	return nil
}

func insertSyncEntry(ctx context.Context, tx *sql.Tx, runID int64, e SyncEntry) error {
	_, err := tx.ExecContext(ctx,
		`INSERT OR REPLACE INTO sync_entries
		 (run_id, issue, date, type, action, work_item_id, previous_minutes, new_minutes, text, previous_text)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		runID, e.Issue, e.Date, e.Type, e.Action, e.WorkItemID, e.PreviousMinutes, e.NewMinutes, e.Text, e.PreviousText)
	if err != nil {
		return fmt.Errorf("record sync entry: %w", err)
	}
	return nil
}

func upsertLedger(ctx context.Context, tx *sql.Tx, r LedgerRow) error {
	_, err := tx.ExecContext(ctx,
		`INSERT OR REPLACE INTO ledger (issue, date, type, work_item_id, minutes, content_hash, booked_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		r.Issue, r.Date, r.Type, r.WorkItemID, r.Minutes, r.ContentHash, r.BookedAt.UTC().UnixNano())
	if err != nil {
		return fmt.Errorf("write ledger row: %w", err)
	}
	return nil
}

// LastSyncRun returns the most recent run that still has entries.
func (s *Store) LastSyncRun(ctx context.Context) (SyncRun, bool, error) {
	var run SyncRun
	var started int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id, started_at FROM sync_runs
		 WHERE id IN (SELECT run_id FROM sync_entries) ORDER BY id DESC LIMIT 1`).Scan(&run.ID, &started)
	if errors.Is(err, sql.ErrNoRows) {
		return SyncRun{}, false, nil
	}
	if err != nil {
		return SyncRun{}, false, fmt.Errorf("read last sync run: %w", err)
	}
	run.StartedAt = time.Unix(0, started).UTC()
	rows, err := s.db.QueryContext(ctx,
		`SELECT issue, date, type, action, work_item_id, previous_minutes, new_minutes, text, previous_text
		 FROM sync_entries WHERE run_id = ? ORDER BY date, issue, type`, run.ID)
	if err != nil {
		return SyncRun{}, false, fmt.Errorf("read sync entries: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var e SyncEntry
		if err := rows.Scan(&e.Issue, &e.Date, &e.Type, &e.Action, &e.WorkItemID, &e.PreviousMinutes, &e.NewMinutes, &e.Text, &e.PreviousText); err != nil {
			return SyncRun{}, false, fmt.Errorf("scan sync entry: %w", err)
		}
		run.Entries = append(run.Entries, e)
	}
	if err := rows.Err(); err != nil {
		return SyncRun{}, false, fmt.Errorf("iterate sync entries: %w", err)
	}
	return run, true, nil
}

// LastBookedText returns the text most recently booked for (issue, date, typ),
// as the sync history recorded it. It reports false when the history holds no
// booking of that item, for example after the history was purged.
func (s *Store) LastBookedText(ctx context.Context, issue, date, typ string) (string, bool, error) {
	var text string
	err := s.db.QueryRowContext(ctx,
		`SELECT text FROM sync_entries
		 WHERE issue = ? AND date = ? AND type = ? AND action IN ('created', 'updated', 'recovered')
		 ORDER BY run_id DESC LIMIT 1`, issue, date, typ).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read last booked text: %w", err)
	}
	return text, true, nil
}

// RevertEntry marks one entry of a run as undone in one transaction: the
// ledger row is replaced by restore, or removed when restore is nil, and the
// entry is deleted. A run without entries is removed.
func (s *Store) RevertEntry(ctx context.Context, runID int64, e SyncEntry, restore *LedgerRow) error {
	tx, err := s.root.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin revert: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if restore != nil {
		if err := upsertLedger(ctx, tx, *restore); err != nil {
			return err
		}
	} else if _, err := tx.ExecContext(ctx,
		`DELETE FROM ledger WHERE issue = ? AND date = ? AND type = ?`, e.Issue, e.Date, e.Type); err != nil {
		return fmt.Errorf("remove ledger row: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM sync_entries WHERE run_id = ? AND issue = ? AND date = ? AND type = ?`,
		runID, e.Issue, e.Date, e.Type); err != nil {
		return fmt.Errorf("remove sync entry: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM sync_runs WHERE id = ? AND NOT EXISTS (SELECT 1 FROM sync_entries WHERE run_id = ?)`,
		runID, runID); err != nil {
		return fmt.Errorf("remove sync run: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit revert: %w", err)
	}
	return nil
}
