package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// inSQLTx runs fn in one transaction on the store's connection and rolls back
// when fn fails. It is for store methods that need several statements and are
// not reached through InTx.
func (s *Store) inSQLTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.root.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// Project is a cached YouTrack project.
type Project struct {
	Key  string
	Name string
}

// ReplaceProjects swaps the cached project list for projects, fetched at the
// given time. It never touches events.
func (s *Store) ReplaceProjects(ctx context.Context, projects []Project, fetchedAt time.Time) error {
	return s.inSQLTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM youtrack_projects`); err != nil {
			return fmt.Errorf("clear project cache: %w", err)
		}
		for _, p := range projects {
			if _, err := tx.ExecContext(ctx,
				`INSERT OR REPLACE INTO youtrack_projects (project_key, name, fetched_at) VALUES (?, ?, ?)`,
				p.Key, p.Name, fetchedAt.UTC().UnixNano()); err != nil {
				return fmt.Errorf("cache project %s: %w", p.Key, err)
			}
		}
		return nil
	})
}

// Projects returns the cached projects ordered by key and the time of the
// refresh that produced them; the zero time means the cache is empty.
func (s *Store) Projects(ctx context.Context) ([]Project, time.Time, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT project_key, name, fetched_at FROM youtrack_projects ORDER BY project_key`)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("query project cache: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var (
		out     []Project
		fetched time.Time
	)
	for rows.Next() {
		var p Project
		var at int64
		if err := rows.Scan(&p.Key, &p.Name, &at); err != nil {
			return nil, time.Time{}, fmt.Errorf("scan project: %w", err)
		}
		fetched = time.Unix(0, at).UTC()
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, time.Time{}, fmt.Errorf("iterate projects: %w", err)
	}
	return out, fetched, nil
}

// DefaultTasks returns the known standard task issue key of each project.
func (s *Store) DefaultTasks(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT project, issue FROM default_tasks`)
	if err != nil {
		return nil, fmt.Errorf("query default tasks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var project, issue string
		if err := rows.Scan(&project, &issue); err != nil {
			return nil, fmt.Errorf("scan default task: %w", err)
		}
		out[project] = issue
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate default tasks: %w", err)
	}
	return out, nil
}

// SetDefaultTask stores or replaces the standard task issue key of a project.
func (s *Store) SetDefaultTask(ctx context.Context, project, issue string) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO default_tasks (project, issue) VALUES (?, ?)`, project, issue); err != nil {
		return fmt.Errorf("store default task of %s: %w", project, err)
	}
	return nil
}

// ConsentedProjects returns the projects whose standard task the user
// confirmed creating at least once.
func (s *Store) ConsentedProjects(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT project FROM default_task_consent`)
	if err != nil {
		return nil, fmt.Errorf("query consent: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]bool{}
	for rows.Next() {
		var project string
		if err := rows.Scan(&project); err != nil {
			return nil, fmt.Errorf("scan consent: %w", err)
		}
		out[project] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate consent: %w", err)
	}
	return out, nil
}

// SetConsent records that the user confirmed creating the standard task of
// project. Calling it again keeps the first confirmation time.
func (s *Store) SetConsent(ctx context.Context, project string, at time.Time) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO default_task_consent (project, confirmed_at) VALUES (?, ?)`,
		project, at.UTC().UnixNano()); err != nil {
		return fmt.Errorf("store consent for %s: %w", project, err)
	}
	return nil
}

// RenameConfirmationIssue moves every confirmation of issue from to issue to.
// Where a confirmation for the same day and type already exists on to, that one
// is kept and the one on from is dropped.
func (s *Store) RenameConfirmationIssue(ctx context.Context, from, to string) error {
	return s.inSQLTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE OR IGNORE confirmations SET issue = ? WHERE issue = ?`, to, from); err != nil {
			return fmt.Errorf("rename confirmations %s to %s: %w", from, to, err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM confirmations WHERE issue = ?`, from); err != nil {
			return fmt.Errorf("drop duplicate confirmations of %s: %w", from, err)
		}
		return nil
	})
}
