// Package store is the SQLite database (ADR-0001: modernc, no cgo;
// ADR-0005: lives in the batlog data directory).
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strconv"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

// DB is an open database handle.
type DB struct {
	sql *sql.DB
}

// Sample is one row of the samples table.
type Sample struct {
	TS       int64
	Pct      int
	OnAC     bool
	Charging bool
	Watts    float64
}

// AppEnergy is one row of the app_energy table.
type AppEnergy struct {
	TS     int64
	App    string
	Energy float64
}

// Exists reports whether a database file is present. Read commands use it
// so they never create a database; that is `daemon install`'s job.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Open opens the database. Read-only handles are used by every command
// except the daemon. WAL mode and a busy timeout keep a reader and the
// daemon's writer from blocking each other.
func Open(path string, readOnly bool) (*DB, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)"
	if readOnly {
		dsn += "&mode=ro"
	} else {
		dsn += "&_pragma=journal_mode(WAL)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite opens lazily; touch the schema so a corrupt file fails here.
	var v int
	if err := db.QueryRow("PRAGMA schema_version").Scan(&v); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return &DB{sql: db}, nil
}

// Close releases the handle.
func (d *DB) Close() error { return d.sql.Close() }

// Exec runs a statement without a result. Used by writers and by tests to seed.
func (d *DB) Exec(ctx context.Context, query string, args ...any) error {
	_, err := d.sql.ExecContext(ctx, query, args...)
	return err
}

// Migrate applies every embedded migration newer than meta.schema_version,
// each in its own transaction. Safe to call on every daemon start.
func (d *DB) Migrate(ctx context.Context) error {
	if _, err := d.sql.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		return err
	}
	current := 0
	var v string
	err := d.sql.QueryRowContext(ctx, `SELECT value FROM meta WHERE key='schema_version'`).Scan(&v)
	switch {
	case err == nil:
		current, _ = strconv.Atoi(v)
	case errors.Is(err, sql.ErrNoRows):
	default:
		return err
	}

	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	for i, name := range names {
		n := i + 1
		if n <= current {
			continue
		}
		body, err := migrations.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := d.sql.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO meta(key,value) VALUES('schema_version',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
			strconv.Itoa(n)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// SamplesSince returns samples with ts >= since, oldest first.
func (d *DB) SamplesSince(ctx context.Context, since int64) ([]Sample, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT ts, pct, on_ac, charging, COALESCE(watts, 0) FROM samples WHERE ts >= ? ORDER BY ts`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Sample
	for rows.Next() {
		var s Sample
		if err := rows.Scan(&s.TS, &s.Pct, &s.OnAC, &s.Charging, &s.Watts); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// EnergySince returns app_energy rows with ts >= since, ordered by ts then app.
func (d *DB) EnergySince(ctx context.Context, since int64) ([]AppEnergy, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT ts, app, energy FROM app_energy WHERE ts >= ? ORDER BY ts, app`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AppEnergy
	for rows.Next() {
		var e AppEnergy
		if err := rows.Scan(&e.TS, &e.App, &e.Energy); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
