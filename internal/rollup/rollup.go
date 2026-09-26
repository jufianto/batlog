// Package rollup keeps the database at 90 days of raw rows: older local days
// become one daily_rollup row each and their samples and app_energy rows are
// deleted (docs/superpowers/specs/2026-09-26-rollup-design.md).
package rollup

import (
	"context"
	"math"
	"time"

	"github.com/jufianto/batlog/internal/history"
	"github.com/jufianto/batlog/internal/store"
)

// KeepDays is how long raw rows are kept.
const KeepDays = 90

// Result says which days a run rolled up.
type Result struct {
	Days        int
	First, Last string
}

// Run rolls up every local day that ended at or before midnight KeepDays
// before now, oldest first, one transaction per day.
func Run(ctx context.Context, db *store.DB, now time.Time) (Result, error) {
	cutoff := midnight(now).AddDate(0, 0, -KeepDays).Unix()
	var res Result
	for ctx.Err() == nil {
		oldest, err := db.OldestRawTS(ctx)
		if err != nil || oldest == 0 || oldest >= cutoff {
			return res, err
		}
		start := midnight(time.Unix(oldest, 0).In(now.Location()))
		day, err := rollDay(ctx, db, start, start.AddDate(0, 0, 1))
		if err != nil {
			return res, err
		}
		if res.Days == 0 {
			res.First = day
		}
		res.Days++
		res.Last = day
	}
	return res, ctx.Err()
}

func rollDay(ctx context.Context, db *store.DB, start, end time.Time) (string, error) {
	from, to := start.Unix(), end.Unix()
	raw, err := db.SamplesBetween(ctx, from, to)
	if err != nil {
		return "", err
	}
	// history needs the sample before the day (deleted with it, so kept as
	// the carry) and the one after it.
	samples, lo, hi := raw, from, to
	if c, ok, err := db.RollupCarry(ctx); err != nil {
		return "", err
	} else if ok && c.TS < from {
		samples, lo = append([]store.Sample{c}, raw...), c.TS
	}
	if next, ok, err := db.FirstSampleFrom(ctx, to); err != nil {
		return "", err
	} else if ok {
		samples, hi = append(samples, next), next.TS
	}
	runs, err := db.RunStartsBetween(ctx, lo, hi)
	if err != nil {
		return "", err
	}
	apps, err := db.EnergySums(ctx, from, to)
	if err != nil {
		return "", err
	}

	tot := history.Build(history.Input{From: start, To: end, Samples: samples, RunStarts: runs}).Totals
	r := store.RollupDay{
		Day:         start.Format("2006-01-02"),
		MinBattery:  minutes(tot.BatterySec),
		MinAC:       minutes(tot.ACSec),
		MinAsleep:   minutes(tot.SleepSec),
		PctConsumed: float64(tot.PctUsed),
	}
	if len(apps) > 0 {
		r.AppEnergy = apps
	}
	var carry store.Sample
	if n := len(raw); n > 0 {
		carry = raw[n-1]
	}
	return r.Day, db.WriteRollup(ctx, r, from, to, carry)
}

func midnight(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func minutes(sec int64) int { return int(math.Round(float64(sec) / 60)) }
