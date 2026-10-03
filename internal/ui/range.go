package ui

import (
	"time"

	"github.com/jufianto/batlog/internal/localday"
)

// Range is a day or a seven-day week: the current one (Back 0) ends now,
// earlier ones run midnight to midnight.
type Range struct {
	Week     bool
	Back     int // 0 is today or the last 7 days; 1 the day or week before, …
	From, To time.Time
}

// MakeRange is the day or week back steps before the current one.
func MakeRange(now time.Time, week bool, back int) Range {
	r := Range{Week: week, Back: back, To: now}
	switch {
	case week && back == 0:
		r.From = localday.Shift(now, -6) // as `history --week`
	case week:
		r.From, r.To = localday.Shift(now, -6-7*back), localday.Shift(now, 1-7*back)
	case back == 0:
		r.From = localday.Start(now)
	default:
		r.From, r.To = localday.Shift(now, -back), localday.Shift(now, 1-back)
	}
	return r
}

// Current is true for the range that ends now.
func (r Range) Current() bool { return r.Back == 0 }

// Daily is true for a single day.
func (r Range) Daily() bool { return !r.Week }

type rangeKey struct {
	week bool
	back int
}

func (r Range) key() rangeKey { return rangeKey{r.Week, r.Back} }

// Label names the range: today, last 7 days, Thu 01 Oct, 21 – 27 Sep.
func (r Range) Label() string {
	switch {
	case r.Current() && r.Week:
		return "last 7 days"
	case r.Current():
		return "today"
	case r.Week:
		last := r.To.Add(-time.Hour) // inside the week's last day
		if r.From.Month() == last.Month() {
			return r.From.Format("02") + " – " + last.Format("02 Jan")
		}
		return r.From.Format("02 Jan") + " – " + last.Format("02 Jan")
	}
	return r.From.Format("Mon 02 Jan")
}

// ChartEnd is where the chart's time axis ends: the end of today for the
// current range, so a day's hours sit in the same place all day.
func (r Range) ChartEnd() time.Time {
	if r.Current() {
		return localday.Next(r.To)
	}
	return r.To
}
