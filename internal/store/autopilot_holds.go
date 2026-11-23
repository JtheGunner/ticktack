package store

import (
	"context"
	"fmt"
	"time"
)

// HoldDay records that the autopilot must leave day alone, because the user
// undid its booking. Holding a held day again keeps the first hold time.
func (s *Store) HoldDay(ctx context.Context, day string, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO autopilot_holds (day, held_at) VALUES (?, ?)`, day, at.UTC().UnixNano())
	if err != nil {
		return fmt.Errorf("hold day: %w", err)
	}
	return nil
}

// ReleaseHold lifts the hold of day. A day without a hold is not an error.
func (s *Store) ReleaseHold(ctx context.Context, day string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM autopilot_holds WHERE day = ?`, day); err != nil {
		return fmt.Errorf("release hold: %w", err)
	}
	return nil
}

// HeldDays returns the held days in ascending order.
func (s *Store) HeldDays(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT day FROM autopilot_holds ORDER BY day`)
	if err != nil {
		return nil, fmt.Errorf("query held days: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var day string
		if err := rows.Scan(&day); err != nil {
			return nil, fmt.Errorf("scan held day: %w", err)
		}
		out = append(out, day)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate held days: %w", err)
	}
	return out, nil
}
