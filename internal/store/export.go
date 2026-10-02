package store

import (
	"context"
	"database/sql"
)

// Readers for `batlog export` (docs/specs/F7-export.md). Each streams its
// rows to fn in order and stops at fn's first error, so a 90-day export
// never holds a table in memory.

// RawSample is a samples row as stored; nil is NULL.
type RawSample struct {
	TS                   int64
	Pct                  int
	OnAC, Charging       bool
	Watts                *float64
	RawCurMAh, RawMaxMAh *int
}

// EachSample streams samples with ts >= from, oldest first.
func (d *DB) EachSample(ctx context.Context, from int64, fn func(RawSample) error) error {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT ts, pct, on_ac, charging, watts, raw_cur_mah, raw_max_mah
		 FROM samples WHERE ts >= ? ORDER BY ts`, from)
	if err != nil {
		return err
	}
	return each(rows, func() error {
		var s RawSample
		var watts sql.NullFloat64
		var cur, maxMAh sql.NullInt64
		if err := rows.Scan(&s.TS, &s.Pct, &s.OnAC, &s.Charging, &watts, &cur, &maxMAh); err != nil {
			return err
		}
		s.Watts, s.RawCurMAh, s.RawMaxMAh = nullFloat(watts), nullInt(cur), nullInt(maxMAh)
		return fn(s)
	})
}

// RawEnergy is one app's energy in one bucket, in nanojoules.
type RawEnergy struct {
	TS            int64 // bucket start
	App           string
	System        bool
	CPU, GPU, ANE int64
}

// EachEnergy streams the buckets with start >= from, ordered by ts then
// app. A database from before the apps table has none.
func (d *DB) EachEnergy(ctx context.Context, from int64, fn func(RawEnergy) error) error {
	if ok, err := d.hasTable(ctx, "apps"); !ok {
		return err
	}
	rows, err := d.sql.QueryContext(ctx,
		`SELECT e.ts, a.name, a.is_system, e.cpu_nj, e.gpu_nj, e.ane_nj
		 FROM app_energy e JOIN apps a ON a.id = e.app_id
		 WHERE e.ts >= ? ORDER BY e.ts, a.name`, from)
	if err != nil {
		return err
	}
	return each(rows, func() error {
		var e RawEnergy
		if err := rows.Scan(&e.TS, &e.App, &e.System, &e.CPU, &e.GPU, &e.ANE); err != nil {
			return err
		}
		return fn(e)
	})
}

// EachHealth streams the health rows with day >= sinceDay (YYYY-MM-DD;
// "" for all), oldest first.
func (d *DB) EachHealth(ctx context.Context, sinceDay string, fn func(HealthDay) error) error {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT day, cycles, raw_max_mah, nominal_mah, design_mah, temp_c, condition
		 FROM health WHERE day >= ? ORDER BY day`, sinceDay)
	if err != nil {
		return err
	}
	return each(rows, func() error {
		var h HealthDay
		var cycles, raw, nominal, design sql.NullInt64
		var temp sql.NullFloat64
		var cond sql.NullString
		if err := rows.Scan(&h.Day, &cycles, &raw, &nominal, &design, &temp, &cond); err != nil {
			return err
		}
		h.Cycles, h.RawMaxMAh, h.NominalMAh, h.DesignMAh = nullInt(cycles), nullInt(raw), nullInt(nominal), nullInt(design)
		h.TempC = nullFloat(temp)
		if cond.Valid {
			h.Condition = &cond.String
		}
		return fn(h)
	})
}

// RollupDays is how many days have been rolled up into daily_rollup, so
// their raw samples and app energy are gone.
func (d *DB) RollupDays(ctx context.Context) (int, error) {
	var n int
	err := d.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM daily_rollup`).Scan(&n)
	return n, err
}

func each(rows *sql.Rows, scan func() error) error {
	defer rows.Close()
	for rows.Next() {
		if err := scan(); err != nil {
			return err
		}
	}
	return rows.Err()
}

func nullInt(v sql.NullInt64) *int {
	if !v.Valid {
		return nil
	}
	i := int(v.Int64)
	return &i
}

func nullFloat(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	return &v.Float64
}
