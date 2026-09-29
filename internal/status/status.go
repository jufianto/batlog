// Package status turns a live battery snapshot plus recent daemon samples
// into the numbers `batlog status` prints. Everything here is pure so the
// maths is testable without a Mac.
package status

import (
	"math"
	"time"

	"github.com/jufianto/batlog/internal/battery"
	"github.com/jufianto/batlog/internal/store"
)

const (
	// Window is how far back status looks for samples.
	Window = 10 * time.Minute
	// sleepGap: a gap strictly greater than this means the Mac was asleep.
	sleepGap = 90 * time.Second
	// minSamples for a drain rate; fewer means "collecting".
	minSamples = 3
	// flatRate in %/hr: a fitted rate smaller than this, either sign, is a
	// flat battery. Whole-percent samples cannot resolve less in 10 minutes.
	flatRate = 0.05
	// over12h: estimates longer than this print as "> 12h".
	over12h = 12 * 60
)

// EnergyFrom is the start of the buckets the worst offender is picked from:
// the current one and the one before, so a bucket that has just begun never
// decides alone.
func EnergyFrom(now time.Time) int64 {
	return store.Bucket(now.Unix()) - store.BucketSec
}

// Offender is the non-system app with the most energy in the window.
type Offender struct {
	App   string
	Share float64 // 0..1 of the window's total energy
}

// Report is what the command renders. Nil pointers mean "not computable".
type Report struct {
	TS           int64
	Percent      int
	OnAC         bool
	Charging     bool
	FullyCharged bool
	Watts        *float64
	MacOSMinutes *int // macOS's estimate; only while discharging

	HasData    bool // any daemon rows in the window
	Collecting bool // on battery with data but fewer than minSamples usable rows
	Drain      *float64
	EstMinutes *int // nil when the rate is 0
	EstOver12h bool // Drain set and the estimate is over 12 h (or infinite)
	Worst      *Offender
}

// Build computes the report. samples must be oldest first.
func Build(s battery.Snapshot, samples []store.Sample, energy []store.AppEnergy, now time.Time) Report {
	r := Report{
		TS:           now.Unix(),
		Percent:      s.Percent,
		OnAC:         s.OnAC,
		Charging:     s.Charging,
		FullyCharged: s.FullyCharged,
		HasData:      len(samples) > 0 || len(energy) > 0,
	}
	if s.HasWatts {
		w := math.Round(s.Watts*10) / 10
		r.Watts = &w
	}
	if s.HasMacOSMinutes && !s.OnAC {
		m := s.MacOSMinutes
		r.MacOSMinutes = &m
	}

	if !s.OnAC && len(samples) > 0 {
		usable := sinceLastBreak(samples, now)
		if len(usable) < minSamples {
			r.Collecting = true
		} else if rate := -slopePerHour(usable); rate > -flatRate {
			if rate < flatRate {
				rate = 0 // flat; also turns -0 into 0 so JSON never prints "-0"
			}
			rounded := math.Round(rate*10) / 10
			r.Drain = &rounded
			if rate == 0 {
				r.EstOver12h = true
			} else {
				est := int(math.Round(float64(s.Percent) / rate * 60))
				r.EstMinutes = &est
				r.EstOver12h = est > over12h
			}
		}
	}

	r.Worst = worst(energy)
	return r
}

// sinceLastBreak returns the unbroken on-battery run that ends now. The run
// starts after the last sleep gap or the last row taken on AC, whichever is
// later, so a fit never spans a plug-in. If the newest row is itself more
// than sleepGap old, the Mac has just woken (or the daemon stopped) and
// nothing in the window is current.
func sinceLastBreak(samples []store.Sample, now time.Time) []store.Sample {
	last := samples[len(samples)-1]
	if time.Duration(now.Unix()-last.TS)*time.Second > sleepGap {
		return nil
	}
	start := 0
	for i := range samples {
		if samples[i].OnAC {
			start = i + 1
		} else if i > 0 && time.Duration(samples[i].TS-samples[i-1].TS)*time.Second > sleepGap {
			start = i
		}
	}
	return samples[start:]
}

// slopePerHour is the least-squares slope of pct against time in hours.
// It is computed from deviations around the means: for a flat series every
// y - ȳ is exactly 0, so the slope is exactly 0 rather than the ±1e-13 the
// n·Σxy − Σx·Σy form leaves behind.
func slopePerHour(samples []store.Sample) float64 {
	n := float64(len(samples))
	t0 := samples[0].TS
	var mx, my float64
	for _, s := range samples {
		mx += float64(s.TS-t0) / 3600
		my += float64(s.Pct)
	}
	mx /= n
	my /= n
	var sxy, sxx float64
	for _, s := range samples {
		dx := float64(s.TS-t0)/3600 - mx
		sxy += dx * (float64(s.Pct) - my)
		sxx += dx * dx
	}
	if sxx == 0 {
		return 0
	}
	return sxy / sxx
}

// worst sums energy per app and returns the largest non-system app as a
// share of all apps' energy, system ones included: WindowServer's drain is
// real, but quitting it is not advice (F4).
func worst(energy []store.AppEnergy) *Offender {
	if len(energy) == 0 {
		return nil
	}
	sums := map[string]float64{}
	var total float64
	for _, e := range energy {
		if !e.System {
			sums[e.App] += e.Energy
		}
		total += e.Energy
	}
	if total <= 0 {
		return nil
	}
	best := Offender{}
	for app, sum := range sums {
		if sum > best.Share*total || (sum == best.Share*total && app < best.App) {
			best = Offender{App: app, Share: sum / total}
		}
	}
	return &best
}
