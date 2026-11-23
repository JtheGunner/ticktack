package store

import (
	"context"
	"fmt"
	"time"
)

// RuleChangeAction names what `ticktack map` did to a learned rule.
type RuleChangeAction string

// Rule change actions.
const (
	RuleAdded   RuleChangeAction = "add"
	RuleEdited  RuleChangeAction = "edit"
	RuleRemoved RuleChangeAction = "rm"
)

// RuleChange records that the user changed a learned rule by hand. It holds
// no rule content: the trust gate only needs to know when it happened.
type RuleChange struct {
	// ID is zero until the change is recorded.
	ID        int64
	ChangedAt time.Time
	Action    RuleChangeAction
}

// RecordRuleChange appends one manual rule change. Rows are never updated or
// deleted (apart from Purge), and events are never touched.
func (s *Store) RecordRuleChange(ctx context.Context, c RuleChange) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO rule_changes (changed_at, action) VALUES (?, ?)`,
		c.ChangedAt.UTC().UnixNano(), string(c.Action))
	if err != nil {
		return fmt.Errorf("record rule change: %w", err)
	}
	return nil
}

// RuleChanges returns all manual rule changes in the order they were made.
func (s *Store) RuleChanges(ctx context.Context) ([]RuleChange, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, changed_at, action FROM rule_changes ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("query rule changes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []RuleChange
	for rows.Next() {
		var c RuleChange
		var at int64
		var action string
		if err := rows.Scan(&c.ID, &at, &action); err != nil {
			return nil, fmt.Errorf("scan rule change: %w", err)
		}
		c.ChangedAt = time.Unix(0, at).UTC()
		c.Action = RuleChangeAction(action)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rule changes: %w", err)
	}
	return out, nil
}
