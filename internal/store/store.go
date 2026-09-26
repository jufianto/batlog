// Package store is the SQLite database (ADR-0001: modernc, no cgo;
// ADR-0005: lives in the batlog data directory).
package store

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"

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

// RecordRunStart notes that a recorder process started at ts.
func (d *DB) RecordRunStart(ctx context.Context, ts int64) error {
	_, err := d.sql.ExecContext(ctx, `INSERT INTO runs(started) VALUES(?) ON CONFLICT(started) DO NOTHING`, ts)
	return err
}

// RunStartsBetween returns recorder starts with from <= started <= to, oldest
// first. A database from before the runs table has none.
func (d *DB) RunStartsBetween(ctx context.Context, from, to int64) ([]int64, error) {
	// Readers do not migrate: the recorder upgrades the file on its next start.
	var n int
	if err := d.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'runs'`).Scan(&n); err != nil || n == 0 {
		return nil, err
	}
	rows, err := d.sql.QueryContext(ctx, `SELECT started FROM runs WHERE started BETWEEN ? AND ? ORDER BY started`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var ts int64
		if err := rows.Scan(&ts); err != nil {
			return nil, err
		}
		out = append(out, ts)
	}
	return out, rows.Err()
}

// LastSampleBefore returns the newest sample with ts < before.
func (d *DB) LastSampleBefore(ctx context.Context, before int64) (Sample, bool, error) {
	return d.oneSample(ctx, `SELECT ts, pct, on_ac, charging, COALESCE(watts, 0) FROM samples
		WHERE ts < ? ORDER BY ts DESC LIMIT 1`, before)
}

// LastSampleBeforeWithState returns the newest sample with ts < before and
// the given on_ac: where the power-source run containing `before` began.
func (d *DB) LastSampleBeforeWithState(ctx context.Context, before int64, onAC bool) (Sample, bool, error) {
	return d.oneSample(ctx, `SELECT ts, pct, on_ac, charging, COALESCE(watts, 0) FROM samples
		WHERE ts < ? AND on_ac = ? ORDER BY ts DESC LIMIT 1`, before, onAC)
}

func (d *DB) oneSample(ctx context.Context, query string, args ...any) (Sample, bool, error) {
	var s Sample
	err := d.sql.QueryRowContext(ctx, query, args...).Scan(&s.TS, &s.Pct, &s.OnAC, &s.Charging, &s.Watts)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Sample{}, false, nil
	case err != nil:
		return Sample{}, false, err
	}
	return s, true, nil
}

// SamplesBetween returns samples with from <= ts < to, oldest first.
func (d *DB) SamplesBetween(ctx context.Context, from, to int64) ([]Sample, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT ts, pct, on_ac, charging, COALESCE(watts, 0) FROM samples WHERE ts >= ? AND ts < ? ORDER BY ts`, from, to)
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

// FirstSampleFrom returns the oldest sample with ts >= from.
func (d *DB) FirstSampleFrom(ctx context.Context, from int64) (Sample, bool, error) {
	return d.oneSample(ctx, `SELECT ts, pct, on_ac, charging, COALESCE(watts, 0) FROM samples
		WHERE ts >= ? ORDER BY ts LIMIT 1`, from)
}

// OldestRawTS is the oldest ts in samples or app_energy, 0 when both are empty.
func (d *DB) OldestRawTS(ctx context.Context) (int64, error) {
	var ts int64
	err := d.sql.QueryRowContext(ctx, `SELECT COALESCE(MIN(ts), 0) FROM (
		SELECT MIN(ts) AS ts FROM samples UNION ALL SELECT MIN(ts) FROM app_energy)`).Scan(&ts)
	return ts, err
}

// EnergySums adds up each app's energy over from <= ts < to.
func (d *DB) EnergySums(ctx context.Context, from, to int64) (map[string]float64, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT app, SUM(energy) FROM app_energy WHERE ts >= ? AND ts < ? GROUP BY app`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var app string
		var e float64
		if err := rows.Scan(&app, &e); err != nil {
			return nil, err
		}
		out[app] = e
	}
	return out, rows.Err()
}

// RollupDay is one daily_rollup row. AppEnergy is nil when the day had no
// app_energy rows.
type RollupDay struct {
	Day                          string
	MinBattery, MinAC, MinAsleep int
	PctConsumed                  float64
	AppEnergy                    map[string]float64
}

// WriteRollup stores a day's rollup, merging into an existing row, and
// deletes its raw rows (from <= ts < to), in one transaction. carry is the
// newest sample deleted; it is kept in meta as the next day's lead-in unless
// a newer one is already there.
func (d *DB) WriteRollup(ctx context.Context, r RollupDay, from, to int64, carry Sample) error {
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	old, ok, err := scanRollup(tx.QueryRowContext(ctx, rollupSelect, r.Day))
	if err != nil {
		return fmt.Errorf("read rollup: %w", err)
	}
	if ok {
		r.MinBattery += old.MinBattery
		r.MinAC += old.MinAC
		r.MinAsleep += old.MinAsleep
		r.PctConsumed += old.PctConsumed
		if old.AppEnergy != nil {
			merged := map[string]float64{}
			for app, e := range old.AppEnergy {
				merged[app] += e
			}
			for app, e := range r.AppEnergy {
				merged[app] += e
			}
			r.AppEnergy = merged
		}
	}
	var apps *string
	if r.AppEnergy != nil {
		b, err := json.Marshal(r.AppEnergy)
		if err != nil {
			return err
		}
		v := string(b)
		apps = &v
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO daily_rollup(day, min_battery, min_ac, min_asleep, pct_consumed, app_energy)
		 VALUES(?,?,?,?,?,?)
		 ON CONFLICT(day) DO UPDATE SET min_battery = excluded.min_battery, min_ac = excluded.min_ac,
		   min_asleep = excluded.min_asleep, pct_consumed = excluded.pct_consumed, app_energy = excluded.app_energy`,
		r.Day, r.MinBattery, r.MinAC, r.MinAsleep, r.PctConsumed, apps); err != nil {
		return fmt.Errorf("write rollup: %w", err)
	}
	for _, table := range []string{"samples", "app_energy"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE ts >= ? AND ts < ?`, from, to); err != nil {
			return fmt.Errorf("prune %s: %w", table, err)
		}
	}
	if carry.TS != 0 {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO meta(key, value) VALUES('rollup_carry', ?)
			 ON CONFLICT(key) DO UPDATE SET value = excluded.value
			 WHERE CAST(substr(meta.value, 1, instr(meta.value, ',') - 1) AS INTEGER) < ?`,
			fmt.Sprintf("%d,%d,%t", carry.TS, carry.Pct, carry.OnAC), carry.TS); err != nil {
			return fmt.Errorf("write rollup carry: %w", err)
		}
	}
	return tx.Commit()
}

// RollupCarry reads the sample the last rollup kept as a lead-in.
func (d *DB) RollupCarry(ctx context.Context) (Sample, bool, error) {
	v, ok, err := d.Meta(ctx, "rollup_carry")
	if err != nil || !ok {
		return Sample{}, false, err
	}
	f := strings.Split(v, ",")
	if len(f) != 3 {
		return Sample{}, false, fmt.Errorf("rollup_carry %q: want ts,pct,on_ac", v)
	}
	ts, err1 := strconv.ParseInt(f[0], 10, 64)
	pct, err2 := strconv.Atoi(f[1])
	onAC, err3 := strconv.ParseBool(f[2])
	if err := errors.Join(err1, err2, err3); err != nil {
		return Sample{}, false, fmt.Errorf("rollup_carry %q: %w", v, err)
	}
	return Sample{TS: ts, Pct: pct, OnAC: onAC}, true, nil
}

const rollupSelect = `SELECT day, min_battery, min_ac, min_asleep, COALESCE(pct_consumed, 0), app_energy
	FROM daily_rollup WHERE day = ?`

// Rollup reads one day's rollup row.
func (d *DB) Rollup(ctx context.Context, day string) (RollupDay, bool, error) {
	return scanRollup(d.sql.QueryRowContext(ctx, rollupSelect, day))
}

func scanRollup(row *sql.Row) (RollupDay, bool, error) {
	var r RollupDay
	var apps sql.NullString
	err := row.Scan(&r.Day, &r.MinBattery, &r.MinAC, &r.MinAsleep, &r.PctConsumed, &apps)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return RollupDay{}, false, nil
	case err != nil:
		return RollupDay{}, false, err
	}
	if apps.Valid {
		if err := json.Unmarshal([]byte(apps.String), &r.AppEnergy); err != nil {
			return RollupDay{}, false, fmt.Errorf("rollup %s app_energy: %w", r.Day, err)
		}
	}
	return r, true, nil
}
