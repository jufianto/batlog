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
	"net/url"
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
	db, err := sql.Open("sqlite", dsn(path, readOnly))
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

// dsn builds the SQLite URI. The path is percent-escaped because SQLite
// reads '#', '?' and '%' in a file: URI as syntax, not as part of the name.
// Readers wait at most 500 ms for a lock so a command stays inside its 1 s
// budget; the daemon's writer waits 5 s.
func dsn(path string, readOnly bool) string {
	u := (&url.URL{Path: path}).EscapedPath()
	if readOnly {
		return "file:" + u + "?_pragma=busy_timeout(500)&mode=ro"
	}
	return "file:" + u + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
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

// HealthRow is the part of a daily health row that the trend needs.
type HealthRow struct {
	Day       string // local calendar day, YYYY-MM-DD
	RawMaxMAh int
	DesignMAh int
}

// HealthSince returns health rows with day >= since, oldest first. Rows
// missing either capacity cannot give a health percentage and are skipped.
func (d *DB) HealthSince(ctx context.Context, since string) ([]HealthRow, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT day, raw_max_mah, design_mah FROM health
		 WHERE day >= ? AND raw_max_mah IS NOT NULL AND design_mah > 0
		 ORDER BY day`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HealthRow
	for rows.Next() {
		var h HealthRow
		if err := rows.Scan(&h.Day, &h.RawMaxMAh, &h.DesignMAh); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// Tick is everything the recorder writes for one sample. Nil pointers are
// stored as NULL: a value the probe did not report is never written as 0.
type Tick struct {
	TS        int64
	Pct       int
	OnAC      bool
	Charging  bool
	Watts     *float64
	RawCurMAh *int
	RawMaxMAh *int
	Health    *HealthDay // set on the first tick of a calendar day
}

// HealthDay is one row of the health table.
type HealthDay struct {
	Day        string // local date, YYYY-MM-DD
	Cycles     *int
	RawMaxMAh  *int
	NominalMAh *int
	DesignMAh  *int
	TempC      *float64
	Condition  *string
}

// WriteTick stores a sample, the optional health row and meta.last_tick in
// one transaction. A second sample in the same second is ignored; a second
// health row on the same day updates the first, keeping known values where
// the new read has none.
func (d *DB) WriteTick(ctx context.Context, t Tick) error {
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO samples(ts, pct, on_ac, charging, watts, raw_cur_mah, raw_max_mah)
		 VALUES(?,?,?,?,?,?,?) ON CONFLICT(ts) DO NOTHING`,
		t.TS, t.Pct, t.OnAC, t.Charging, t.Watts, t.RawCurMAh, t.RawMaxMAh); err != nil {
		return fmt.Errorf("write sample: %w", err)
	}
	if h := t.Health; h != nil {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO health(day, cycles, raw_max_mah, nominal_mah, design_mah, temp_c, condition)
			 VALUES(?,?,?,?,?,?,?)
			 ON CONFLICT(day) DO UPDATE SET
			   cycles      = COALESCE(excluded.cycles, health.cycles),
			   raw_max_mah = COALESCE(excluded.raw_max_mah, health.raw_max_mah),
			   nominal_mah = COALESCE(excluded.nominal_mah, health.nominal_mah),
			   design_mah  = COALESCE(excluded.design_mah, health.design_mah),
			   temp_c      = COALESCE(excluded.temp_c, health.temp_c),
			   condition   = COALESCE(excluded.condition, health.condition)`,
			h.Day, h.Cycles, h.RawMaxMAh, h.NominalMAh, h.DesignMAh, h.TempC, h.Condition); err != nil {
			return fmt.Errorf("write health: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO meta(key, value) VALUES('last_tick', ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		strconv.FormatInt(t.TS, 10)); err != nil {
		return fmt.Errorf("write last_tick: %w", err)
	}
	return tx.Commit()
}

// Meta reads one key from the meta table.
func (d *DB) Meta(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := d.sql.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, err
	}
	return v, true, nil
}

// SampleStats returns the number of samples and the oldest ts (0 if none).
func (d *DB) SampleStats(ctx context.Context) (count int, oldest int64, err error) {
	err = d.sql.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(MIN(ts), 0) FROM samples`).Scan(&count, &oldest)
	return count, oldest, err
}
