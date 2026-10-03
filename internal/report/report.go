// Package report computes the conclusions `batlog report` prints
// (docs/specs/F6-report.md) from F3's sessions and the raw samples. It is
// pure: the caller loads history, app energy and health.
package report

import (
	"fmt"
	"math"
	"sort"

	"github.com/jufianto/batlog/internal/history"
	"github.com/jufianto/batlog/internal/store"
)

const (
	// sleepGap matches F3: a longer interval between samples is sleep.
	sleepGap = 90
	// MinSessionMin is the least awake time a session needs to count for
	// effective battery life and the worst drain. Percent is a whole
	// number, so a shorter session's rate is mostly rounding.
	MinSessionMin = 30
	// MinLifeStartPct: effective battery life uses sessions that started
	// at least this full.
	MinLifeStartPct = 80
	// MinSessions is the number of completed sessions medians and
	// effective battery life need.
	MinSessions = 2
	// MinAwakeMin is the awake time the above-90 and below-20 shares need.
	MinAwakeMin = 30

	// Flag thresholds; a flag fires only above its threshold.
	Above90Share    = 0.5
	LowPlugInPct    = 15.0
	PinnedMinPerDay = 4 * 60
)

// Life is effective battery life: the mean full 100→0 run of the qualifying
// sessions, each at its own drain rate.
type Life struct {
	Minutes  int
	Sessions int // the sessions averaged
}

// EffectiveLife averages the completed batlog sessions that started in
// from <= t < to, at ≥ MinLifeStartPct, with at least MinSessionMin awake and a
// positive drain. ok is false with fewer than MinSessions of them; n is how
// many there were.
func EffectiveLife(ss []history.Session, from, to int64) (l Life, n int, ok bool) {
	var sum float64
	for _, s := range ss {
		if !completed(s) || s.Start < from || s.Start >= to || s.StartPct < MinLifeStartPct ||
			*s.AwakeMin < MinSessionMin || s.Drain == nil || *s.Drain <= 0 {
			continue
		}
		sum += 100 / *s.Drain * 60
		n++
	}
	if n < MinSessions {
		return Life{}, n, false
	}
	return Life{Minutes: int(math.Round(sum / float64(n))), Sessions: n}, n, true
}

// Worst is the session with the highest drain among those that started at
// or after from with at least MinSessionMin awake; nil when there is none.
// The ongoing one counts. Like EffectiveLife it skips sessions that began
// before the range, so consecutive reports never name the same one.
func Worst(ss []history.Session, from int64) *history.Session {
	var w *history.Session
	for i := range ss {
		s := &ss[i]
		if s.Source != history.SourceBatlog || s.Start < from || s.Drain == nil || *s.AwakeMin < MinSessionMin {
			continue
		}
		if w == nil || *s.Drain > *w.Drain {
			w = s
		}
	}
	return w
}

// Longest is the session started at or after from with the most awake
// minutes; nil when there is none.
func Longest(ss []history.Session, from int64) *history.Session {
	var l *history.Session
	for i := range ss {
		s := &ss[i]
		if s.AwakeMin == nil || s.Start < from {
			continue
		}
		if l == nil || *s.AwakeMin > *l.AwakeMin {
			l = s
		}
	}
	return l
}

// AvgDrain is the percent used per awake hour on battery; ok is false with
// under five minutes on battery.
func AvgDrain(t history.Totals) (float64, bool) {
	if t.BatterySec < 5*60 {
		return 0, false
	}
	return math.Round(float64(max(t.PctUsed, 0))*3600/float64(t.BatterySec)*10) / 10, true
}

// Habits are how the battery is charged and kept.
type Habits struct {
	Completed int // completed sessions that started in the range
	// PlugInMedian and UnplugMedian are nil with fewer than MinSessions
	// completed sessions.
	PlugInMedian, UnplugMedian *float64
	// Above90Share and Below20Share are of awake time; nil with under
	// MinAwakeMin of it.
	Above90Share, Below20Share *float64
	// MaxMinAt100OnAC is the longest unbroken stretch at 100% on AC. Unlike
	// every other duration it runs through sleep between two samples at
	// 100% on AC: a full battery left on the charger overnight is the
	// habit this measures.
	MaxMinAt100OnAC int
	// MinAt100OnAC is the total of those stretches inside the range.
	MinAt100OnAC int
}

// BuildHabits reads events and sessions from F3's result and the samples it
// was built from, clipped to from <= t < to. runStarts mark data gaps: a
// stretch at 100% never runs through one.
func BuildHabits(r history.Result, samples []store.Sample, runStarts []int64, from, to int64) Habits {
	var h Habits
	for _, s := range r.Sessions {
		if completed(s) && s.Start >= from {
			h.Completed++
		}
	}
	if h.Completed >= MinSessions {
		var plug, unplug []float64
		for _, e := range r.Events {
			if e.Source != history.SourceBatlog {
				continue
			}
			if e.Plugged {
				plug = append(plug, float64(e.Pct))
			} else {
				unplug = append(unplug, float64(e.Pct))
			}
		}
		h.PlugInMedian, h.UnplugMedian = median(plug), median(unplug)
	}

	var awake, above, below, run, longest int64
	for i := 0; i+1 < len(samples); i++ {
		s, next := samples[i], samples[i+1]
		clipped := overlap(s.TS, next.TS, from, to)
		dt := next.TS - s.TS
		if dt <= sleepGap {
			awake += clipped
			if s.Pct > 90 {
				above += clipped
			}
			if s.Pct < 20 {
				below += clipped
			}
		}
		full := s.OnAC && s.Pct == 100 && next.OnAC && next.Pct == 100
		if full && (dt <= sleepGap || !gapHasStart(runStarts, s.TS, next.TS)) {
			run += clipped
			h.MinAt100OnAC += int(clipped)
		} else {
			run = 0
		}
		longest = max(longest, run)
	}
	h.MaxMinAt100OnAC = int(longest / 60)
	h.MinAt100OnAC /= 60
	if awake >= MinAwakeMin*60 {
		a, b := float64(above)/float64(awake), float64(below)/float64(awake)
		h.Above90Share, h.Below20Share = &a, &b
	}
	return h
}

// Charging is how the charges that started in the range went (F3's charge
// sessions).
type Charging struct {
	Charges int
	// ReachedFull counts the charges that got to 100 %; one plugged in
	// already full is not among them.
	ReachedFull int
	StartMedian *float64 // the percent at plug-in
	// ToFullMedian is of the charges with an exact time to full: not after
	// a sleep on the charger (an upper bound) and without a data gap.
	ToFullMedian *int
	// AtFullMedian is the time left plugged in after full, of finished
	// charges; MaxAtFull counts the ongoing one so far too.
	AtFullMedian *int
	MaxAtFull    int
	StoppedBelow []int // the last percent of finished charges that never got to full
	// NotChargingMin is the time on AC not charging below full before it
	// (F3's hold), summed.
	NotChargingMin int
}

// BuildCharging reads the charge sessions that started at or after from:
// one that began before the range is the previous report's, as for
// battery sessions.
func BuildCharging(cs []history.ChargeSession, from int64) Charging {
	var (
		c                     Charging
		start, toFull, atFull []float64
	)
	for _, s := range cs {
		if s.Start < from {
			continue
		}
		c.Charges++
		start = append(start, float64(s.StartPct))
		c.NotChargingMin += s.HoldMin
		if s.FullAt == 0 {
			if !s.Ongoing {
				c.StoppedBelow = append(c.StoppedBelow, s.EndPct)
			}
			continue
		}
		if s.FullAt > s.Start {
			c.ReachedFull++
			if !s.FullUpperBound && !s.DataGap {
				toFull = append(toFull, float64(s.FullAt-s.Start)/60)
			}
		}
		c.MaxAtFull = max(c.MaxAtFull, s.AtFullMin)
		if !s.Ongoing {
			atFull = append(atFull, float64(s.AtFullMin))
		}
	}
	c.StartMedian = median(start)
	c.ToFullMedian, c.AtFullMedian = roundMin(median(toFull)), roundMin(median(atFull))
	return c
}

func roundMin(v *float64) *int {
	if v == nil {
		return nil
	}
	m := int(math.Round(*v))
	return &m
}

// Flag is one rule-based warning.
type Flag struct {
	ID      string
	Message string
}

// Flags applies the F6 rules; days is how many days the range spans, for
// the per-day rule.
func Flags(h Habits, days int) []Flag {
	var fs []Flag
	if h.Above90Share != nil && *h.Above90Share > Above90Share {
		fs = append(fs, Flag{"above_90", fmt.Sprintf(
			"battery spends %.0f%% of time above 90%% — consider Optimized Charging or unplugging earlier", *h.Above90Share*100)})
	}
	if h.PlugInMedian != nil && *h.PlugInMedian < LowPlugInPct {
		fs = append(fs, Flag{"runs_low", fmt.Sprintf(
			"you often run very low (median %s%%) — deep discharges add wear", FmtPct(*h.PlugInMedian))})
	}
	if perDay := float64(h.MinAt100OnAC) / float64(max(days, 1)); perDay > PinnedMinPerDay {
		fs = append(fs, Flag{"pinned_100", fmt.Sprintf("sat at 100%% for %.1fh/day", perDay/60)})
	}
	return fs
}

// FmtPct prints a median: whole, or with the .5 an even count can give.
func FmtPct(v float64) string {
	if v == math.Trunc(v) {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.1f", v)
}

func completed(s history.Session) bool {
	return s.Source == history.SourceBatlog && !s.Ongoing && s.AwakeMin != nil
}

func median(v []float64) *float64 {
	if len(v) == 0 {
		return nil
	}
	sort.Float64s(v)
	m := v[len(v)/2]
	if len(v)%2 == 0 {
		m = (v[len(v)/2-1] + m) / 2
	}
	return &m
}

// gapHasStart reports whether a recorder start falls in (a, b]: the
// recorder was down, so nothing is known about the battery in between.
func gapHasStart(starts []int64, a, b int64) bool {
	for _, s := range starts {
		if s > a && s <= b {
			return true
		}
	}
	return false
}

func overlap(a, b, from, to int64) int64 {
	lo, hi := max(a, from), min(b, to)
	if hi <= lo {
		return 0
	}
	return hi - lo
}
