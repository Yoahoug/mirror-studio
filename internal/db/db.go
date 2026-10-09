// Package db owns the SQLite connection, the embedded migrations and the
// low-level transaction helpers every store layer builds on.
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the pure-Go "sqlite" driver
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// DB wraps *sql.DB so store layers share one handle type.
type DB struct {
	*sql.DB
	path string
}

// Path reports the database file the handle points at.
func (d *DB) Path() string { return d.path }

// Open connects to the SQLite file and applies the PRAGMAs the service relies
// on: WAL for concurrent readers, a busy timeout so writers wait instead of
// failing, and NORMAL synchronous for a write-ahead-logged database.
func Open(path string) (*DB, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}
	dsn := path + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)"
	handle, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	handle.SetMaxOpenConns(1)
	handle.SetMaxIdleConns(1)
	handle.SetConnMaxLifetime(0)
	if err := handle.Ping(); err != nil {
		handle.Close()
		return nil, fmt.Errorf("ping sqlite database: %w", err)
	}
	return &DB{DB: handle, path: path}, nil
}

// Migrate applies every embedded migration exactly once, tracked in
// schema_migrations. Migrations are append-only: released files are never
// edited, only new ones added.
func (d *DB) Migrate(logger *slog.Logger) error {
	if _, err := d.Exec(`CREATE TABLE IF NOT EXISTS "schema_migrations" (
		"name" varchar(255) NOT NULL PRIMARY KEY,
		"applied_at" datetime NOT NULL
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return fmt.Errorf("read embedded migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		var exists int
		if err := d.QueryRow(`SELECT COUNT(1) FROM schema_migrations WHERE name = ?`, name).Scan(&exists); err != nil {
			return fmt.Errorf("check migration %s: %w", name, err)
		}
		if exists > 0 {
			continue
		}
		script, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		if err := d.applyMigration(name, string(script)); err != nil {
			return err
		}
		if logger != nil {
			logger.Info("applied migration", "name", name)
		}
	}
	return nil
}

func (d *DB) applyMigration(name, script string) error {
	tx, err := d.Begin()
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", name, err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(script); err != nil {
		return fmt.Errorf("apply migration %s: %w", name, err)
	}
	if _, err := tx.Exec(
		`INSERT INTO schema_migrations (name, applied_at) VALUES (?, ?)`,
		name, FormatTime(time.Now().UTC()),
	); err != nil {
		return fmt.Errorf("record migration %s: %w", name, err)
	}
	return tx.Commit()
}

// WithImmediate runs fn inside a write transaction that takes the SQLite write
// lock up front. Read-modify-write sequences that must not interleave (quota
// settlement, task claiming, upload assembly) use this instead of relying on
// row locks, which SQLite does not provide.
func (d *DB) WithImmediate(ctx context.Context, fn func(tx *Tx) error) error {
	conn, err := d.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	tx := &Tx{conn: conn, ctx: ctx}
	if err := fn(tx); err != nil {
		_, _ = conn.ExecContext(ctx, "ROLLBACK")
		return err
	}
	_, err = conn.ExecContext(ctx, "COMMIT")
	return err
}

// Tx is a thin wrapper that keeps the context attached to every statement.
type Tx struct {
	conn interface {
		ExecContext(context.Context, string, ...any) (sql.Result, error)
		QueryContext(context.Context, string, ...any) (*sql.Rows, error)
		QueryRowContext(context.Context, string, ...any) *sql.Row
	}
	ctx context.Context
}

// Exec runs a statement inside the transaction.
func (t *Tx) Exec(query string, args ...any) (sql.Result, error) {
	return t.conn.ExecContext(t.ctx, query, args...)
}

// Query runs a query inside the transaction.
func (t *Tx) Query(query string, args ...any) (*sql.Rows, error) {
	return t.conn.QueryContext(t.ctx, query, args...)
}

// QueryRow runs a single-row query inside the transaction.
func (t *Tx) QueryRow(query string, args ...any) *sql.Row {
	return t.conn.QueryRowContext(t.ctx, query, args...)
}

// FormatTime renders a timestamp the way Django stores it in SQLite: UTC,
// naive (no offset suffix), microsecond precision.
func FormatTime(value time.Time) string {
	return value.UTC().Format("2006-01-02 15:04:05.999999")
}

// FormatDate renders a date-only column value.
func FormatDate(value time.Time) string { return value.Format("2006-01-02") }

// ParseTime accepts both the naive timestamp format Django writes and RFC3339.
func ParseTime(value string) (time.Time, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02",
	} {
		if parsed, err := time.ParseInLocation(layout, trimmed, time.UTC); err == nil {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
}

// NullableTime renders an optional timestamp for storage.
func NullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return FormatTime(*value)
}

// TimePtr converts an optional sql value into a *time.Time.
func TimePtr(value sql.NullString) *time.Time {
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return nil
	}
	parsed, ok := ParseTime(value.String)
	if !ok {
		return nil
	}
	return &parsed
}

// NullableString renders an optional string for storage.
func NullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// Bool converts a Go bool into the 0/1 integer SQLite stores.
func Bool(value bool) int {
	if value {
		return 1
	}
	return 0
}
