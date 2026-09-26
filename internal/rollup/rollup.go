// Package rollup keeps the database at 90 days of raw rows: older local days
// become one daily_rollup row each and their samples and app_energy rows are
// deleted (docs/superpowers/specs/2026-09-26-rollup-design.md).
package rollup

import (
	"context"
	"math"
	"time"

	"github.com/jufianto/batlog/internal/history"
	"github.com/jufianto/batlog/internal/localday"
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
// before now, oldest first, one transaction per day. It walks every date
// from the oldest raw row to the cutoff, so a day without raw rows (the Mac
// asleep all day) still gets its row.
func Run(ctx context.Context, db *store.DB, now time.Time) (Result, error) {
	cutoff := localday.Shift(now, -KeepDays)
	var res Result
	oldest, err := db.OldestRawTS(ctx)
	if err != nil || oldest == 0 || oldest >= cutoff.Unix() {
		return res, err
	}
	for d := localday.Start(time.Unix(oldest, 0).In(now.Location())); d.Before(cutoff); d = localday.Next(d) {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		day, wrote, err := rollDay(ctx, db, d, localday.Next(d))
		if err != nil {
			return res, err
		}
		if !wrote {
			continue
		}
		if res.Days == 0 {
			res.First = day
		}
		res.Days++
		res.Last = day
	}
	return res, nil
}

// rollDay rolls up one day. It writes nothing for a day with no raw rows
// and nothing known about it (a data gap, or already rolled up).
func rollDay(ctx context.Context, db *store.DB, start, end time.Time) (string, bool, error) {
	from, to := start.Unix(), end.Unix()
	raw, err := db.SamplesBetween(ctx, from, to)
	if err != nil {
		return "", false, err
	}
	// history needs the sample before the day (deleted with it, so kept as
	// the carry) and the one after it.
	samples, lo, hi := raw, from, to
	if c, ok, err := db.RollupCarry(ctx); err != nil {
		return "", false, err
	} else if ok && c.TS < from {
		samples, lo = append([]store.Sample{c}, raw...), c.TS
	}
	if next, ok, err := db.FirstSampleFrom(ctx, to); err != nil {
		return "", false, err
	} else if ok {
		samples, hi = append(samples, next), next.TS
	}
	runs, err := db.RunStartsBetween(ctx, lo, hi)
	if err != nil {
		return "", false, err
	}
	apps, err := db.EnergySums(ctx, from, to)
	if err != nil {
		return "", false, err
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
	} else if r.AppEnergy == nil && r.MinBattery+r.MinAC+r.MinAsleep == 0 && r.PctConsumed == 0 {
		return r.Day, false, nil
	}
	return r.Day, true, db.WriteRollup(ctx, r, from, to, carry)
}

func minutes(sec int64) int { return int(math.Round(float64(sec) / 60)) }
