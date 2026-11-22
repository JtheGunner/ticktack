// Package store persists events, overrides and the booking ledger in SQLite.
//
// Events are append-only: apart from Purge the store exposes no way to
// update or delete them.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/JtheGunner/ticktack/internal/event"

	_ "modernc.org/sqlite" // pure-Go SQLite driver
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Store is a SQLite-backed event store.
type Store struct {
	// db runs statements: the database itself, or the open transaction of a
	// store handed out by InTx.
	db dbtx
	// root is the database handle; it opens transactions and is closed.
	root *sql.DB
}

// dbtx is the statement surface shared by *sql.DB and *sql.Tx.
type dbtx interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// dsnOptions configures every connection:
//
//   - busy_timeout makes a connection wait up to 5s for a lock instead of
//     failing at once with SQLITE_BUSY. It comes first so the journal_mode
//     change below already waits.
//   - journal_mode(WAL) lets readers and one writer run side by side; the
//     agent, the shell hook and the CLI share one database file.
//   - _txlock=immediate makes every transaction start with BEGIN IMMEDIATE,
//     so it takes the write lock up front (and waits for it) instead of
//     failing with SQLITE_BUSY_SNAPSHOT when it upgrades from a read.
//     TestTransactionsTakeTheWriteLockImmediately proves the driver honours it.
//
// SQLite versions before 3.51.3 (backports 3.44.6 and 3.50.7) can corrupt a
// WAL database when several connections write and checkpoint at the same time
// (the "WAL-reset bug"). modernc.org/sqlite v1.62.0 bundles SQLite 3.54.1;
// TestBundledSQLiteHasWALResetFix fails when a driver update goes below the
// fixed versions.
const dsnOptions = "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_txlock=immediate"

// Open opens (creating if needed) the database at path and applies all
// pending migrations.
func Open(path string) (*Store, error) {
	if err := restrictFile(path); err != nil {
		return nil, err
	}
	if strings.Contains(path, "?") {
		return nil, fmt.Errorf("open database: path %q contains '?'", path)
	}
	db, err := sql.Open("sqlite", path+dsnOptions)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// A single connection keeps SQLite writes serialized and simple.
	db.SetMaxOpenConns(1)
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db, root: db}, nil
}

// restrictFile creates the database file with mode 0600 when it is missing and
// narrows an existing one to 0600. The driver would create it with the
// process umask (usually 0644); SQLite gives its journal file the mode of the
// database file, so the side files inherit the restriction.
func restrictFile(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("create database file: %w", err)
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return fmt.Errorf("restrict database file: %w", err)
	}
	return f.Close()
}

func migrate(db *sql.DB) error {
	if err := ensureMigrationsTable(db); err != nil {
		return err
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

// ensureMigrationsTable creates the bookkeeping table in a write transaction,
// and only when it is missing: a plain CREATE TABLE IF NOT EXISTS outside a
// transaction fails at once with SQLITE_BUSY while another process writes,
// instead of waiting for the lock.
func ensureMigrationsTable(db *sql.DB) error {
	var present int
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", migrationsTable).Scan(&present); err != nil {
		return fmt.Errorf("check schema_migrations: %w", err)
	}
	if present > 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin schema_migrations: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		name TEXT PRIMARY KEY,
		applied_at INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit schema_migrations: %w", err)
	}
	return nil
}

func applyMigration(db *sql.DB, name string) error {
	// Fast path without the write lock: an up-to-date database must open while
	// another process holds a write transaction. The check is repeated inside
	// the transaction below, which is the one that decides.
	var applied int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE name = ?", name).Scan(&applied); err != nil {
		return fmt.Errorf("check migration %s: %w", name, err)
	}
	if applied > 0 {
		return nil
	}
	body, err := migrationFiles.ReadFile("migrations/" + name)
	if err != nil {
		return fmt.Errorf("read migration %s: %w", name, err)
	}
	// The write lock is taken by BEGIN IMMEDIATE, so the "already applied"
	// check and the apply are one atomic step: of several processes opening a
	// fresh database, exactly one applies each migration.
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", name, err)
	}
	defer func() { _ = tx.Rollback() }()
	var exists int
	if err := tx.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE name = ?", name).Scan(&exists); err != nil {
		return fmt.Errorf("check migration %s: %w", name, err)
	}
	if exists > 0 {
		return nil
	}
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
	return s.root.Close()
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

// AppendBatch stores events in one transaction: either all of them are
// stored or none. Events with a known Key are ignored like in Append.
func (s *Store) AppendBatch(ctx context.Context, events []event.Event) error {
	if len(events) == 0 {
		return nil
	}
	return s.InTx(ctx, func(tx Tx) error {
		for _, e := range events {
			if err := tx.Append(ctx, e); err != nil {
				return err
			}
		}
		return nil
	})
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
	_, err := s.AddOverrideID(ctx, o)
	return err
}

// AddOverrideID records a user correction and returns its id.
func (s *Store) AddOverrideID(ctx context.Context, o event.Override) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO overrides (start, "end", ticket, discard, kind) VALUES (?, ?, ?, ?, ?)`,
		o.Start.UTC().UnixNano(), o.End.UTC().UnixNano(), o.Ticket, o.Drops(), string(o.EffectiveKind()))
	if err != nil {
		return 0, fmt.Errorf("add override: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("add override: %w", err)
	}
	return id, nil
}

// DeleteOverride removes one override. It is how a review session takes back
// its own last correction; events are never touched.
func (s *Store) DeleteOverride(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM overrides WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete override: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete override: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("delete override: no override with id %d", id)
	}
	return nil
}

// Overrides returns overrides whose start lies in [from, to) in creation
// order. Callers apply them in that order and let later ones win, so the most
// recent correction decides wherever overrides overlap.
func (s *Store) Overrides(ctx context.Context, from, to time.Time) ([]event.Override, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, start, "end", ticket, kind FROM overrides
		 WHERE start >= ? AND start < ? ORDER BY id`,
		from.UTC().UnixNano(), to.UTC().UnixNano())
	if err != nil {
		return nil, fmt.Errorf("query overrides: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []event.Override
	for rows.Next() {
		var o event.Override
		var start, end int64
		var kind string
		if err := rows.Scan(&o.ID, &start, &end, &o.Ticket, &kind); err != nil {
			return nil, fmt.Errorf("scan override: %w", err)
		}
		o.Start = time.Unix(0, start).UTC()
		o.End = time.Unix(0, end).UTC()
		o.Kind = event.OverrideKind(kind)
		o.Discard = o.Drops()
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate overrides: %w", err)
	}
	return out, nil
}

// migrationsTable records the applied migrations. It is bookkeeping, not user
// data, and the only table Purge keeps.
const migrationsTable = "schema_migrations"

// Purge empties every data table in one transaction. The tables are read from
// the schema, so a table added by a later migration is covered without a
// change here. The migration bookkeeping stays, which keeps the database
// usable afterwards.
func (s *Store) Purge(ctx context.Context) error {
	tx, err := s.root.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin purge: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	tables, err := dataTables(ctx, tx)
	if err != nil {
		return err
	}
	// Tables are emptied in schema order, not dependency order; references
	// between them are only checked once everything is gone.
	if _, err := tx.ExecContext(ctx, "PRAGMA defer_foreign_keys = ON"); err != nil {
		return fmt.Errorf("defer foreign keys for purge: %w", err)
	}
	for _, table := range tables {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+quoteIdentifier(table)); err != nil {
			return fmt.Errorf("purge %s: %w", table, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit purge: %w", err)
	}
	return nil
}

// dataTables lists the tables holding user data: every table of the schema
// except SQLite's internal ones and the migration bookkeeping.
func dataTables(ctx context.Context, tx *sql.Tx) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list tables for purge: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("list tables for purge: %w", err)
		}
		if name == migrationsTable || strings.HasPrefix(name, "sqlite_") {
			continue
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list tables for purge: %w", err)
	}
	return tables, nil
}

// quoteIdentifier quotes a table name taken from the schema for use in SQL.
func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// ErrBusy is returned by CheckWritable when another connection holds the
// write lock: the database is in use, not unwritable.
var ErrBusy = errors.New("database is in use by another process")

// CheckWritable verifies that the database accepts writes by taking and
// releasing a write lock. It stores nothing. A held lock yields ErrBusy.
func (s *Store) CheckWritable(ctx context.Context) error {
	conn, err := s.root.Conn(ctx)
	if err != nil {
		return fmt.Errorf("check writable: %w", err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		if msg := err.Error(); strings.Contains(msg, "SQLITE_BUSY") || strings.Contains(msg, "database is locked") {
			return ErrBusy
		}
		return fmt.Errorf("check writable: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "ROLLBACK"); err != nil {
		return fmt.Errorf("check writable: %w", err)
	}
	return nil
}
