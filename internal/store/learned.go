package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrInvalidLearnedRule is returned for a learned rule that would be unsafe
// or meaningless.
var ErrInvalidLearnedRule = errors.New("invalid learned rule")

// ErrNoSuchLearnedRule is returned when a rule ID does not exist.
var ErrNoSuchLearnedRule = errors.New("no such learned rule")

// LearnedRule is a visible rule written from a review correction: time in
// Repo (path prefix) on BranchPattern belongs to Ticket.
type LearnedRule struct {
	ID int64
	// Repo is an absolute repository path; empty matches every repository.
	Repo string
	// BranchPattern is matched against the branch name; empty matches every
	// branch.
	BranchPattern string
	Ticket        string
	CreatedAt     time.Time
}

// normalized trims the rule and rejects rules that would match everything.
func (r LearnedRule) normalized() (LearnedRule, error) {
	r.Repo = strings.TrimRight(strings.TrimSpace(r.Repo), "/")
	r.BranchPattern = strings.TrimSpace(r.BranchPattern)
	r.Ticket = strings.ToUpper(strings.TrimSpace(r.Ticket))
	switch {
	case r.Repo == "" && r.BranchPattern == "":
		return LearnedRule{}, fmt.Errorf("%w: repo and branch pattern are both empty and would match everything", ErrInvalidLearnedRule)
	case r.Ticket == "":
		return LearnedRule{}, fmt.Errorf("%w: ticket is empty", ErrInvalidLearnedRule)
	}
	return r, nil
}

// AddLearnedRule stores a new rule and returns it with its ID. It never
// touches events.
func (s *Store) AddLearnedRule(ctx context.Context, r LearnedRule) (LearnedRule, error) {
	r, err := r.normalized()
	if err != nil {
		return LearnedRule{}, err
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO learned_rules (repo, branch_pattern, ticket, created_at) VALUES (?, ?, ?, ?)`,
		r.Repo, r.BranchPattern, r.Ticket, r.CreatedAt.UTC().UnixNano())
	if err != nil {
		return LearnedRule{}, fmt.Errorf("add learned rule: %w", err)
	}
	if r.ID, err = res.LastInsertId(); err != nil {
		return LearnedRule{}, fmt.Errorf("add learned rule: %w", err)
	}
	return r, nil
}

// LearnedRules returns all rules ordered by ID.
func (s *Store) LearnedRules(ctx context.Context) ([]LearnedRule, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, repo, branch_pattern, ticket, created_at FROM learned_rules ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("query learned rules: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []LearnedRule
	for rows.Next() {
		var r LearnedRule
		var at int64
		if err := rows.Scan(&r.ID, &r.Repo, &r.BranchPattern, &r.Ticket, &at); err != nil {
			return nil, fmt.Errorf("scan learned rule: %w", err)
		}
		r.CreatedAt = time.Unix(0, at).UTC()
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate learned rules: %w", err)
	}
	return out, nil
}

// UpdateLearnedRule replaces repo, branch pattern and ticket of rule r.ID.
func (s *Store) UpdateLearnedRule(ctx context.Context, r LearnedRule) error {
	r, err := r.normalized()
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE learned_rules SET repo = ?, branch_pattern = ?, ticket = ? WHERE id = ?`,
		r.Repo, r.BranchPattern, r.Ticket, r.ID)
	return expectOneRule(res, err, "update", r.ID)
}

// DeleteLearnedRule removes rule id.
func (s *Store) DeleteLearnedRule(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM learned_rules WHERE id = ?`, id)
	return expectOneRule(res, err, "delete", id)
}

type rowsResult interface{ RowsAffected() (int64, error) }

func expectOneRule(res rowsResult, err error, verb string, id int64) error {
	if err != nil {
		return fmt.Errorf("%s learned rule: %w", verb, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s learned rule: %w", verb, err)
	}
	if n == 0 {
		return fmt.Errorf("%w: %d", ErrNoSuchLearnedRule, id)
	}
	return nil
}
