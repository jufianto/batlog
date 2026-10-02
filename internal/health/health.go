// Package health turns battery detail and the daemon's daily health rows
// into what `batlog health` prints. Pure functions, testable without a Mac.
package health

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jufianto/batlog/internal/battery"
	"github.com/jufianto/batlog/internal/store"
)

const (
	// WindowDays is how far back the trend looks.
	WindowDays = 90
	// MinSpanDays is the shortest span between first and last row that
	// gives a trend worth printing. The gauge's raw maximum recalibrates by
	// several percent over days (seen live: 85.4 % → 90.4 % in a week) while
	// real wear is under 1 %/month, so a week's fit is mostly noise.
	MinSpanDays = 30
	dayLayout   = "2006-01-02"
)

// Report is what the command renders. Nil pointers mean "not available".
type Report struct {
	HealthPct      *float64 // AppleRawMaxCapacity / DesignCapacity (ADR-0002)
	AppleHealthPct *float64 // NominalChargeCapacity / DesignCapacity
	RawMaxMAh      *int
	NominalMAh     *int
	DesignMAh      *int
	Cycles         *int
	TempC          *float64
	VoltageV       *float64
	Condition      *string // "Normal" or "Service recommended"; nil without the key
	FailureStatus  *int
	NeedsService   bool
	MissingKeys    []string // capacity keys ioreg did not report
	HealthNote     string   // why HealthPct is nil, e.g. "DesignCapacity is 0"
}

// TrendResult is the health change over the window.
type TrendResult struct {
	FromPct     float64
	ToPct       float64
	PctPerMonth float64
	Days        int
	LastDay     string // newest row used, YYYY-MM-DD
}

// Build computes the current-health part of the report.
func Build(h battery.Health) Report {
	r := Report{
		RawMaxMAh:     h.RawMaxMAh,
		NominalMAh:    h.NominalMAh,
		DesignMAh:     h.DesignMAh,
		Cycles:        h.Cycles,
		TempC:         h.TempC,
		VoltageV:      h.VoltageV,
		FailureStatus: h.FailureStatus,
	}
	if h.RawMaxMAh == nil {
		r.MissingKeys = append(r.MissingKeys, "AppleRawMaxCapacity")
	}
	if h.DesignMAh == nil {
		r.MissingKeys = append(r.MissingKeys, "DesignCapacity")
	}
	var notes []string
	if len(r.MissingKeys) > 0 {
		notes = append(notes, "ioreg has no "+strings.Join(r.MissingKeys, " or "))
	}
	if h.DesignMAh != nil && *h.DesignMAh <= 0 {
		notes = append(notes, fmt.Sprintf("DesignCapacity is %d", *h.DesignMAh))
	}
	r.HealthNote = strings.Join(notes, "; ")
	if len(notes) == 0 {
		r.HealthPct = pct(*h.RawMaxMAh, *h.DesignMAh)
	}
	if h.NominalMAh != nil && h.DesignMAh != nil && *h.DesignMAh > 0 {
		r.AppleHealthPct = pct(*h.NominalMAh, *h.DesignMAh)
	}
	if h.FailureStatus != nil {
		c := "Normal"
		if *h.FailureStatus != 0 {
			c, r.NeedsService = "Service recommended", true
		}
		r.Condition = &c
	}
	return r
}

func pct(num, den int) *float64 {
	v := round(float64(num)*100/float64(den), 1)
	return &v
}

// Trend fits health % against days over rows (oldest first). It returns
// nil unless there are at least two rows spanning MinSpanDays; spanDays is
// always the span between the first and last row, for the "have N days"
// message.
func Trend(all []store.HealthRow) (trend *TrendResult, spanDays int) {
	// Drop rows whose day does not parse before choosing the ends, so one
	// bad row cannot hide months of good ones.
	type point struct {
		day time.Time
		row store.HealthRow
	}
	var pts []point
	for _, r := range all {
		if d, err := time.Parse(dayLayout, r.Day); err == nil && r.DesignMAh > 0 {
			pts = append(pts, point{d, r})
		}
	}
	sort.SliceStable(pts, func(i, j int) bool { return pts[i].day.Before(pts[j].day) })
	if len(pts) < 2 {
		return nil, 0
	}
	first, last := pts[0], pts[len(pts)-1]
	spanDays = int(last.day.Sub(first.day).Hours() / 24)
	if spanDays < MinSpanDays {
		return nil, spanDays
	}

	var n, sx, sy, sxx, sxy float64
	for _, p := range pts {
		r := p.row
		x := p.day.Sub(first.day).Hours() / 24
		y := float64(r.RawMaxMAh) * 100 / float64(r.DesignMAh)
		n++
		sx += x
		sy += y
		sxx += x * x
		sxy += x * y
	}
	perMonth := 0.0
	if den := n*sxx - sx*sx; den != 0 {
		perMonth = round((n*sxy-sx*sy)/den*30, 2)
	}
	if perMonth == 0 {
		perMonth = 0 // normalise -0 so output never shows "-0.00"
	}
	return &TrendResult{
		FromPct:     *pct(first.row.RawMaxMAh, first.row.DesignMAh),
		ToPct:       *pct(last.row.RawMaxMAh, last.row.DesignMAh),
		PctPerMonth: perMonth,
		Days:        spanDays,
		LastDay:     last.row.Day,
	}, spanDays
}

// Since is the first day of the trend window, as stored in health.day.
func Since(today time.Time) string {
	return today.AddDate(0, 0, -WindowDays).Format(dayLayout)
}

func round(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}
