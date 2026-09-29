// Package top ranks apps by energy over a range and estimates what each cost
// the battery (docs/specs/F4-top.md). It is pure: the caller loads buckets,
// samples and the range's percent used.
package top

import (
	"sort"

	"github.com/jufianto/batlog/internal/energy"
	"github.com/jufianto/batlog/internal/store"
)

// Row is one app over the range.
type Row struct {
	App     string
	System  bool
	Share   float64 // of all apps' energy in the range, 0..1
	EnergyJ float64 // in the range
	// BatteryPct is the estimated percent of battery the app used: its share
	// of on-battery energy × the percent used. Nil when the range had no
	// on-battery energy.
	BatteryPct *float64
}

// Build ranks apps over from <= t < to, by share desc then name asc.
//
// A bucket partly outside the range counts by the share of its samples
// inside the range (samples exist only while awake, so this apportions by
// awake minutes), or by time when it has no samples. Its on-battery part is
// the share of its samples that are inside the range and on battery.
// samples must cover every bucket that overlaps the range.
func Build(buckets []store.AppEnergy, samples []store.Sample, from, to int64, pctUsed int) []Row {
	type count struct{ all, in, battery int }
	counts := map[int64]*count{}
	for _, s := range samples {
		b := store.Bucket(s.TS)
		c := counts[b]
		if c == nil {
			c = &count{}
			counts[b] = c
		}
		c.all++
		if s.TS >= from && s.TS < to {
			c.in++
			if !s.OnAC {
				c.battery++
			}
		}
	}
	type sum struct {
		system       bool
		all, battery float64
	}
	apps := map[string]*sum{}
	var total, totalBattery float64
	for _, e := range buckets {
		var w, wb float64
		if c := counts[e.TS]; c != nil && c.all > 0 {
			w = float64(c.in) / float64(c.all)
			wb = float64(c.battery) / float64(c.all)
		} else {
			lo, hi := max(e.TS, from), min(e.TS+store.BucketSec, to)
			if hi > lo {
				w = float64(hi-lo) / store.BucketSec
			}
		}
		if w == 0 {
			continue
		}
		a := apps[e.App]
		if a == nil {
			a = &sum{}
			apps[e.App] = a
		}
		a.system = a.system || e.System
		a.all += w * e.Energy
		a.battery += wb * e.Energy
		total += w * e.Energy
		totalBattery += wb * e.Energy
	}
	if total <= 0 {
		return nil
	}
	rows := make([]Row, 0, len(apps))
	for name, a := range apps {
		r := Row{App: name, System: a.system, Share: a.all / total, EnergyJ: a.all}
		if totalBattery > 0 {
			p := a.battery / totalBattery * float64(pctUsed)
			r.BatteryPct = &p
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Share != rows[j].Share {
			return rows[i].Share > rows[j].Share
		}
		return rows[i].App < rows[j].App
	})
	return rows
}

// Window is a stretch of battery time and its mean power.
type Window struct {
	Start, End int64
	AvgWatts   float64
}

const (
	windowSec = 30 * 60
	gapSec    = 90 // longer between samples is sleep (F3)
)

// Heaviest finds the 30 minutes on battery, awake throughout, with the
// highest mean power, inside from <= t < to. ok is false when the range
// has no 30 unbroken awake minutes on battery, or no power readings.
func Heaviest(samples []store.Sample, from, to int64) (Window, bool) {
	var ss []store.Sample
	for _, s := range samples {
		if s.TS >= from && s.TS < to {
			ss = append(ss, s)
		}
	}
	var best Window
	found := false
	for i := range ss {
		if ss[i].OnAC {
			continue
		}
		var sum float64
		n := 0
		covered := false
		for j := i; j < len(ss) && ss[j].TS < ss[i].TS+windowSec; j++ {
			if ss[j].OnAC || (j > i && ss[j].TS-ss[j-1].TS > gapSec) {
				break
			}
			sum += ss[j].Watts
			n++
			// The next sample, due within gapSec, would fall past the window.
			covered = ss[j].TS+gapSec >= ss[i].TS+windowSec
		}
		if covered && n > 0 {
			if avg := sum / float64(n); !found || avg > best.AvgWatts {
				best = Window{ss[i].TS, ss[i].TS + windowSec, avg}
				found = true
			}
		}
	}
	// Samples without a power reading store 0 W: no answer beats "0.0 W".
	return best, found && best.AvgWatts > 0
}

// Live ranks one interval's deltas (`top --live`): shares only, as a
// second says nothing about the battery.
func Live(ds []energy.Delta) []Row {
	buckets := make([]store.AppEnergy, len(ds))
	for i, d := range ds {
		buckets[i] = store.AppEnergy{App: d.App, System: d.System, Energy: float64(d.CPU+d.GPU+d.ANE) / 1e9}
	}
	// One bucket at 0, wholly inside the range, with no samples: weight 1.
	rows := Build(buckets, nil, 0, store.BucketSec, 0)
	for i := range rows {
		rows[i].BatteryPct = nil
	}
	return rows
}
