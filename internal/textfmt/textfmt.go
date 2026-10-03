// Package textfmt formats durations, times and shares the same way in every
// command and in `batlog ui`.
package textfmt

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jufianto/batlog/internal/history"
	"github.com/jufianto/batlog/internal/localday"
)

// Duration prints minutes as 45m or 1h 05m.
func Duration(minutes int) string {
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%dh %02dm", minutes/60, minutes%60)
}

// JoinDot joins parts with " · ".
func JoinDot(parts []string) string { return strings.Join(parts, " · ") }

// Clock prints a time as 15:04 on t's day, "yesterday 15:04" the day
// before, and "Thu 24 Sep 15:04" further back.
func Clock(ts int64, t time.Time) string {
	at := time.Unix(ts, 0).In(t.Location())
	switch day, today := localday.Start(at), localday.Start(t); {
	case !day.Before(today):
		return at.Format("15:04")
	case localday.Next(day).Equal(today):
		return at.Format("yesterday 15:04")
	}
	return at.Format("Mon 02 Jan 15:04")
}

// Totals is the line under `history`'s lists.
func Totals(t history.Totals) string {
	parts := []string{
		"on battery " + Duration(int(t.BatterySec/60)),
		"on AC " + Duration(int(t.ACSec/60)),
		"asleep " + Duration(int(t.SleepSec/60)),
	}
	if t.GapSec > 0 {
		parts = append(parts, "no data "+Duration(int(t.GapSec/60)))
	}
	return JoinDot(parts)
}

// Share prints a 0..1 share as a whole percent, "< 1%" for a sliver.
func Share(share float64) string {
	if share > 0 && share < 0.005 {
		return "< 1%"
	}
	return fmt.Sprintf("%.0f%%", share*100)
}

// Thousands groups digits with a space: 5424 → "5 424".
func Thousands(n int) string {
	if n < 0 {
		return "-" + Thousands(-n)
	}
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + " " + s[i:]
	}
	return s
}

// Signed prints a rate with a typographic minus, or a plus for growth.
func Signed(v float64) string {
	switch {
	case v < 0:
		return "−" + strconv.FormatFloat(-v, 'f', 2, 64)
	case v > 0:
		return "+" + strconv.FormatFloat(v, 'f', 2, 64)
	}
	return "0.00"
}

// MinHoldMin is the least not-charging time a charge session mentions.
const MinHoldMin = 5

// ChargeOutcome describes a charge session for `history` and `batlog ui`:
// how it got to full (or how far it got), its time at 100 % and any hold
// below it. est is the curve's time to full for an ongoing charge, or nil.
func ChargeOutcome(c history.ChargeSession, est *int) []string {
	bound := func(upper bool) string {
		if upper {
			return "≤ "
		}
		return ""
	}
	var parts []string
	switch {
	case c.FullAt == c.Start:
		parts = append(parts, "already full")
	case c.FullAt != 0:
		parts = append(parts, "full in "+bound(c.FullUpperBound)+Duration(int((c.FullAt-c.Start)/60)))
	case !c.Ongoing:
		p := "unplugged before full"
		if c.MaxPct > c.StartPct {
			p = fmt.Sprintf("%d%% in %s%s · %s", c.MaxPct, bound(c.MaxUpperBound), Duration(int((c.MaxAt-c.Start)/60)), p)
		}
		parts = append(parts, p)
	case c.Charging:
		p := "charging"
		if est != nil {
			p += " · full in ~" + Duration(*est)
		}
		parts = append(parts, p)
	default:
		parts = append(parts, "not charging")
	}
	if c.FullAt != 0 {
		at := Duration(c.AtFullMin) + " at 100%"
		if c.Ongoing {
			at += " so far"
		}
		parts = append(parts, at)
	}
	if c.HoldMin >= MinHoldMin {
		parts = append(parts, fmt.Sprintf("not charging at %d%% for %s", c.HoldPct, Duration(c.HoldMin)))
	}
	return parts
}
