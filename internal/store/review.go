package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"
)

// Confirmation records that the user reviewed one item (issue, day, type)
// with exactly Minutes minutes.
type Confirmation struct {
	Day         string // yyyy-mm-dd
	Issue       string
	Type        string
	Minutes     int
	ConfirmedAt time.Time
}

// CapAcknowledgement records that the user accepted a day total above the
// daily cap.
type CapAcknowledgement struct {
	Day            string // yyyy-mm-dd
	TotalMinutes   int
	AcknowledgedAt time.Time
}

// Confirm stores or replaces the confirmation of one item. It never touches
// events.
func (s *Store) Confirm(ctx context.Context, c Confirmation) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO confirmations (day, issue, type, minutes, confirmed_at)
		 VALUES (?, ?, ?, ?, ?)`,
		c.Day, c.Issue, c.Type, c.Minutes, c.ConfirmedAt.UTC().UnixNano())
	if err != nil {
		return fmt.Errorf("confirm item: %w", err)
	}
	return nil
}

// Confirmations returns the confirmations of one day ordered by issue, type.
func (s *Store) Confirmations(ctx context.Context, day string) ([]Confirmation, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT day, issue, type, minutes, confirmed_at FROM confirmations
		 WHERE day = ? ORDER BY issue, type`, day)
	if err != nil {
		return nil, fmt.Errorf("query confirmations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Confirmation
	for rows.Next() {
		var c Confirmation
		var at int64
		if err := rows.Scan(&c.Day, &c.Issue, &c.Type, &c.Minutes, &at); err != nil {
			return nil, fmt.Errorf("scan confirmation: %w", err)
		}
		c.ConfirmedAt = time.Unix(0, at).UTC()
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate confirmations: %w", err)
	}
	return out, nil
}

// ConfirmedDays returns every day that has at least one confirmation, in
// ascending order.
func (s *Store) ConfirmedDays(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT day FROM confirmations ORDER BY day`)
	if err != nil {
		return nil, fmt.Errorf("query confirmed days: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var day string
		if err := rows.Scan(&day); err != nil {
			return nil, fmt.Errorf("scan confirmed day: %w", err)
		}
		out = append(out, day)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate confirmed days: %w", err)
	}
	return out, nil
}

// AcknowledgeCap stores or replaces the cap acknowledgement of a day.
func (s *Store) AcknowledgeCap(ctx context.Context, a CapAcknowledgement) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO cap_acknowledgements (day, total_minutes, acknowledged_at)
		 VALUES (?, ?, ?)`,
		a.Day, a.TotalMinutes, a.AcknowledgedAt.UTC().UnixNano())
	if err != nil {
		return fmt.Errorf("acknowledge cap: %w", err)
	}
	return nil
}

// CapAcknowledgementFor returns the cap acknowledgement of a day.
func (s *Store) CapAcknowledgementFor(ctx context.Context, day string) (CapAcknowledgement, bool, error) {
	var a CapAcknowledgement
	var at int64
	err := s.db.QueryRowContext(ctx,
		`SELECT day, total_minutes, acknowledged_at FROM cap_acknowledgements WHERE day = ?`, day).
		Scan(&a.Day, &a.TotalMinutes, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return CapAcknowledgement{}, false, nil
	}
	if err != nil {
		return CapAcknowledgement{}, false, fmt.Errorf("read cap acknowledgement: %w", err)
	}
	a.AcknowledgedAt = time.Unix(0, at).UTC()
	return a, true, nil
}

// Tx is the write surface available inside InTx: the corrections a review
// action stores together with its review log entry, and the rule writes of
// the map command together with their rule change record.
type Tx interface {
	Append(ctx context.Context, e event.Event) error
	AddOverride(ctx context.Context, o event.Override) error
	// AddOverrideID stores an override and returns its id, so a session can
	// take it back later with DeleteOverride.
	AddOverrideID(ctx context.Context, o event.Override) (int64, error)
	DeleteOverride(ctx context.Context, id int64) error
	Confirm(ctx context.Context, c Confirmation) error
	AcknowledgeCap(ctx context.Context, a CapAcknowledgement) error
	AddLearnedRule(ctx context.Context, r LearnedRule) (LearnedRule, error)
	UpdateLearnedRule(ctx context.Context, r LearnedRule) error
	DeleteLearnedRule(ctx context.Context, id int64) error
	RecordRuleChange(ctx context.Context, c RuleChange) error
	SaveReviewRecord(ctx context.Context, r ReviewRecord) (int64, error)
}

// InTx runs fn in one transaction: when fn returns an error everything it
// wrote is rolled back, otherwise it is committed. The Tx must not be used
// after fn returns, and fn must not call methods of s itself (the store has
// a single connection that the transaction holds).
func (s *Store) InTx(ctx context.Context, fn func(tx Tx) error) error {
	sqlTx, err := s.root.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = sqlTx.Rollback() }()
	if err := fn(&Store{db: sqlTx, root: s.root}); err != nil {
		return err
	}
	if err := sqlTx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
