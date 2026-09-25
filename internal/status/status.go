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
	// minRate in %/hr; below this the estimate is "> 12h".
	minRate = 0.5
)

// Offender is the app with the highest energy in the window.
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
	EstMinutes *int
	EstOver12h bool // Drain set but below minRate
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
		usable := onBatteryAfterLastGap(samples)
		if len(usable) < minSamples {
			r.Collecting = true
		} else if rate := -slopePerHour(usable); rate >= 0 {
			if rate == 0 {
				rate = 0 // normalise -0 so JSON never prints "-0"
			}
			rounded := math.Round(rate*10) / 10
			r.Drain = &rounded
			if rate < minRate {
				r.EstOver12h = true
			} else {
				est := int(math.Round(float64(s.Percent) / rate * 60))
				r.EstMinutes = &est
			}
		}
	}

	r.Worst = worst(energy)
	return r
}

// onBatteryAfterLastGap drops everything before the last sleep gap, then
// every row taken on AC.
func onBatteryAfterLastGap(samples []store.Sample) []store.Sample {
	start := 0
	for i := 1; i < len(samples); i++ {
		if time.Duration(samples[i].TS-samples[i-1].TS)*time.Second > sleepGap {
			start = i
		}
	}
	var out []store.Sample
	for _, s := range samples[start:] {
		if !s.OnAC {
			out = append(out, s)
		}
	}
	return out
}

// slopePerHour is the least-squares slope of pct against time in hours.
func slopePerHour(samples []store.Sample) float64 {
	n := float64(len(samples))
	t0 := samples[0].TS
	var sx, sy, sxx, sxy float64
	for _, s := range samples {
		x := float64(s.TS-t0) / 3600
		y := float64(s.Pct)
		sx += x
		sy += y
		sxx += x * x
		sxy += x * y
	}
	den := n*sxx - sx*sx
	if den == 0 {
		return 0
	}
	return (n*sxy - sx*sy) / den
}

// worst sums energy per app and returns the largest as a share of the total.
func worst(energy []store.AppEnergy) *Offender {
	if len(energy) == 0 {
		return nil
	}
	sums := map[string]float64{}
	var total float64
	for _, e := range energy {
		sums[e.App] += e.Energy
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
