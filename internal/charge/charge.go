// Package charge estimates the time to a full battery from this Mac's own
// charge curve (docs/specs/F3-history.md, F1). Charging is not linear: on
// an M4 Pro it ran ~78 %/hr up to 60 %, 48 %/hr in the 80s and 22 %/hr in
// the 90s, so the last tenth takes as long as 20 → 50 %. A rate measured
// now says little about the rest of the charge; a curve per tenth does.
package charge

import "math"

// Bands is one per tenth of the battery: 0–9 %, 10–19 %, … 90–99 %.
const Bands = 10

// MinBandSec is the charging time a band needs before its own rate is
// trusted over the default.
const MinBandSec = 10 * 60

// defaultRates (%/hr per band) are the curve measured on an M4 Pro MacBook
// Pro with its 96 W adapter over a week, used for any band this Mac has not
// charged through enough yet.
var defaultRates = [Bands]float64{75, 75, 78, 78, 78, 78, 69, 64, 48, 22}

// BandStat is the charging this Mac did inside one band: the percent it
// gained over awake, charging seconds.
type BandStat struct {
	Gained int
	Sec    int64
}

// Curve is a charge rate per band, in %/hr.
type Curve struct {
	Rates   [Bands]float64
	Learned [Bands]bool // the rate is this Mac's own, not the default
}

// Learn builds a curve from per-band charging stats; a band with under
// MinBandSec of charging, or that gained nothing, keeps the default rate.
func Learn(stats [Bands]BandStat) Curve {
	c := Curve{Rates: defaultRates}
	for b, st := range stats {
		if st.Sec >= MinBandSec && st.Gained > 0 {
			c.Rates[b] = float64(st.Gained) * 3600 / float64(st.Sec)
			c.Learned[b] = true
		}
	}
	return c
}

// MinutesToFull is the time from pct to 100 % along the curve.
func (c Curve) MinutesToFull(pct int) int {
	if pct >= 100 {
		return 0
	}
	pct = max(pct, 0)
	var hours float64
	for b := pct / 10; b < Bands; b++ {
		lo, hi := max(pct, b*10), (b+1)*10
		hours += float64(hi-lo) / c.Rates[b]
	}
	return int(math.Round(hours * 60))
}
