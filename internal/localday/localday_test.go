package localday

import (
	"testing"
	"time"
	_ "time/tzdata" // the zones below, on any OS
)

func zone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestDayLengths(t *testing.T) {
	cases := []struct {
		zone  string
		day   string
		hours float64
		start string // local clock at the day's first instant
	}{
		{"Asia/Jakarta", "2026-09-26", 24, "00:00"},
		{"Europe/Berlin", "2026-03-29", 23, "00:00"},
		{"Europe/Berlin", "2026-10-25", 25, "00:00"},
		{"America/Santiago", "2026-09-06", 23, "01:00"}, // 00:00 does not exist
		{"America/Havana", "2026-03-08", 23, "01:00"},
		{"Atlantic/Azores", "2026-03-29", 23, "01:00"},
		{"Africa/Cairo", "2026-04-24", 23, "01:00"},
	}
	for _, c := range cases {
		loc := zone(t, c.zone)
		d, _ := time.Parse("2006-01-02", c.day) // not in loc: its midnight may not exist
		noon := time.Date(d.Year(), d.Month(), d.Day(), 12, 0, 0, 0, loc)
		s, n := Start(noon), Next(noon)
		if s.Format("2006-01-02 15:04") != c.day+" "+c.start {
			t.Errorf("%s %s: Start = %s", c.zone, c.day, s)
		}
		if got := n.Sub(s).Hours(); got != c.hours {
			t.Errorf("%s %s: day is %.1f h, want %.0f", c.zone, c.day, got, c.hours)
		}
		if Start(n) != n || Next(s) != n || Start(s) != s {
			t.Errorf("%s %s: Start/Next not stable at the boundaries", c.zone, c.day)
		}
		if got := Shift(noon, -1); Next(got) != s {
			t.Errorf("%s %s: Shift(-1) = %s", c.zone, c.day, got)
		}
	}
}

func TestStartOfAnyInstant(t *testing.T) {
	loc := zone(t, "America/Santiago")
	for _, clock := range []string{"2026-09-06 01:00", "2026-09-06 13:30", "2026-09-06 23:59"} {
		at, _ := time.ParseInLocation("2006-01-02 15:04", clock, loc)
		if got := Start(at).Format("2006-01-02 15:04"); got != "2026-09-06 01:00" {
			t.Errorf("Start(%s) = %s", clock, got)
		}
	}
	at, _ := time.ParseInLocation("2006-01-02 15:04", "2026-09-05 23:30", loc)
	if got := Start(at).Format("2006-01-02 15:04"); got != "2026-09-05 00:00" {
		t.Errorf("Start(the hour before the skipped midnight) = %s", got)
	}
}
