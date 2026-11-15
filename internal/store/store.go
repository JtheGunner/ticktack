// Package store persists events, overrides and the booking ledger in SQLite.
//
// Events are append-only: apart from Purge the store exposes no way to
// update or delete them.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"

	_ "modernc.org/sqlite" // pure-Go SQLite driver
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Store is a SQLite-backed event store.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the database at path and applies all
// pending migrations.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// A single connection keeps SQLite writes serialized and simple.
	db.SetMaxOpenConns(1)
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		name TEXT PRIMARY KEY,
		applied_at INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		if err := applyMigration(db, name); err != nil {
			return err
		}
	}
	return nil
}

func applyMigration(db *sql.DB, name string) error {
	var exists int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE name = ?", name).Scan(&exists); err != nil {
		return fmt.Errorf("check migration %s: %w", name, err)
	}
	if exists > 0 {
		return nil
	}
	body, err := migrationFiles.ReadFile("migrations/" + name)
	if err != nil {
		return fmt.Errorf("read migration %s: %w", name, err)
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", name, err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(string(body)); err != nil {
		return fmt.Errorf("apply migration %s: %w", name, err)
	}
	if _, err := tx.Exec("INSERT INTO schema_migrations (name, applied_at) VALUES (?, ?)", name, time.Now().UTC().UnixNano()); err != nil {
		return fmt.Errorf("record migration %s: %w", name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", name, err)
	}
	return nil
}

// Close releases the database handle.
func (s *Store) Close() error {
	return s.db.Close()
}

// Append stores an event. An event with a non-empty Key that already exists
// is silently ignored.
func (s *Store) Append(ctx context.Context, e event.Event) error {
	var key any
	if e.Key != "" {
		key = e.Key
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO events (at, source, kind, repo, branch, ticket, app, idle_seconds, dedup_key)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.At.UTC().UnixNano(), e.Source, string(e.Kind), e.Repo, e.Branch, e.Ticket, e.App, e.IdleSeconds, key)
	if err != nil {
		return fmt.Errorf("append event: %w", err)
	}
	return nil
}

// Events returns events with from <= At < to, ordered by time.
func (s *Store) Events(ctx context.Context, from, to time.Time) ([]event.Event, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, at, source, kind, repo, branch, ticket, app, idle_seconds, COALESCE(dedup_key, '')
		 FROM events WHERE at >= ? AND at < ? ORDER BY at, id`,
		from.UTC().UnixNano(), to.UTC().UnixNano())
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []event.Event
	for rows.Next() {
		var e event.Event
		var at int64
		var kind string
		if err := rows.Scan(&e.ID, &at, &e.Source, &kind, &e.Repo, &e.Branch, &e.Ticket, &e.App, &e.IdleSeconds, &e.Key); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		e.At = time.Unix(0, at).UTC()
		e.Kind = event.Kind(kind)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate events: %w", err)
	}
	return out, nil
}

// AddOverride records a user correction.
func (s *Store) AddOverride(ctx context.Context, o event.Override) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO overrides (start, "end", ticket, discard) VALUES (?, ?, ?, ?)`,
		o.Start.UTC().UnixNano(), o.End.UTC().UnixNano(), o.Ticket, o.Discard)
	if err != nil {
		return fmt.Errorf("add override: %w", err)
	}
	return nil
}

// Overrides returns overrides whose start lies in [from, to), ordered by start.
func (s *Store) Overrides(ctx context.Context, from, to time.Time) ([]event.Override, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT start, "end", ticket, discard FROM overrides
		 WHERE start >= ? AND start < ? ORDER BY start, id`,
		from.UTC().UnixNano(), to.UTC().UnixNano())
	if err != nil {
		return nil, fmt.Errorf("query overrides: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []event.Override
	for rows.Next() {
		var o event.Override
		var start, end int64
		if err := rows.Scan(&start, &end, &o.Ticket, &o.Discard); err != nil {
			return nil, fmt.Errorf("scan override: %w", err)
		}
		o.Start = time.Unix(0, start).UTC()
		o.End = time.Unix(0, end).UTC()
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate overrides: %w", err)
	}
	return out, nil
}

// Purge deletes all events, overrides and ledger rows in one transaction.
func (s *Store) Purge(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin purge: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, table := range []string{"events", "overrides", "ledger"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("purge %s: %w", table, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit purge: %w", err)
	}
	return nil
}
