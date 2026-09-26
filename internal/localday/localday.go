// Package localday finds where local calendar days begin. time.Date(y, m, d,
// 0, 0, 0, 0, loc) is not enough: where DST skips midnight (Chile, Cuba, the
// Azores) Go moves the missing 00:00 back into the day before.
package localday

import "time"

// Start is the first instant of t's local date.
func Start(t time.Time) time.Time {
	y, m, d := t.Date()
	s := time.Date(y, m, d, 0, 0, 0, 0, t.Location())
	for s.Day() != d { // landed in the day before: walk to the gap's end
		s = s.Add(15 * time.Minute)
	}
	return s
}

// Shift is the first instant of the local date days after t's (days < 0 is
// before). Noon exists on every date, so the date arithmetic is exact.
func Shift(t time.Time, days int) time.Time {
	y, m, d := t.Date()
	return Start(time.Date(y, m, d+days, 12, 0, 0, 0, t.Location()))
}

// Next is the first instant of the local date after t's.
func Next(t time.Time) time.Time { return Shift(t, 1) }
