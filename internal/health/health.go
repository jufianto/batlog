// Package health turns battery detail and the daemon's daily health rows
// into what `batlog health` prints. Pure functions, testable without a Mac.
package health

import (
	"math"
	"time"

	"github.com/jufianto/batlog/internal/battery"
	"github.com/jufianto/batlog/internal/store"
)

const (
	// WindowDays is how far back the trend looks.
	WindowDays = 90
	// MinSpanDays is the shortest span between first and last row that
	// gives a trend worth printing.
	MinSpanDays = 7
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
	MissingKeys    []string // capacity keys that stopped HealthPct being computed
}

// TrendResult is the health change over the window.
type TrendResult struct {
	FromPct     float64
	ToPct       float64
	PctPerMonth float64
	Days        int
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
	if h.DesignMAh == nil || *h.DesignMAh <= 0 {
		r.MissingKeys = append(r.MissingKeys, "DesignCapacity")
	}
	if len(r.MissingKeys) == 0 {
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
func Trend(rows []store.HealthRow) (trend *TrendResult, spanDays int) {
	if len(rows) < 2 {
		return nil, 0
	}
	first, err1 := time.Parse(dayLayout, rows[0].Day)
	last, err2 := time.Parse(dayLayout, rows[len(rows)-1].Day)
	if err1 != nil || err2 != nil {
		return nil, 0
	}
	spanDays = int(last.Sub(first).Hours() / 24)
	if spanDays < MinSpanDays {
		return nil, spanDays
	}

	var n, sx, sy, sxx, sxy float64
	for _, r := range rows {
		d, err := time.Parse(dayLayout, r.Day)
		if err != nil {
			continue
		}
		x := d.Sub(first).Hours() / 24
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
		FromPct:     *pct(rows[0].RawMaxMAh, rows[0].DesignMAh),
		ToPct:       *pct(rows[len(rows)-1].RawMaxMAh, rows[len(rows)-1].DesignMAh),
		PctPerMonth: perMonth,
		Days:        spanDays,
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
