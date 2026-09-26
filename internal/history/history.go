// Package history turns recorded samples into F3's plug and unplug events,
// battery sessions, headline answers and totals. It is pure: the caller
// loads samples, recorder starts and pmset readings.
package history

import (
	"math"
	"time"

	"github.com/jufianto/batlog/internal/pmset"
	"github.com/jufianto/batlog/internal/store"
)

const (
	// sleepGap is the longest interval between samples still counted as
	// awake; the recorder ticks once a minute.
	sleepGap = 90
	// staleAfter marks an ongoing session (data gap) when its newest sample
	// is older than this: the recorder should have written since.
	staleAfter = 10 * 60
	// minDrainSec is the least awake time a session needs to show a drain.
	minDrainSec = 5 * 60

	SourceBatlog = "batlog"
	SourcePmset  = "pmset"
)

// Input is everything Build needs.
type Input struct {
	From, To time.Time
	// Samples are ascending and start at the last state change before From,
	// so a session that began before the range has its real start.
	Samples []store.Sample
	// RunStarts are recorder start times; a gap that holds one is a data gap.
	RunStarts []int64
	// Pmset readings, ascending. Only those before FirstSampleTS are used.
	Pmset []pmset.Reading
	// FirstSampleTS is the daemon's first sample ever, 0 when there is none.
	FirstSampleTS int64
	// WokeAt is the last wake from sleep, 0 when unknown.
	WokeAt int64
}

// Event is a change of power source.
type Event struct {
	TS      int64
	Plugged bool
	Pct     int
	Source  string
}

// Session is one run on battery.
type Session struct {
	Start int64
	End   int64 // the plug event; 0 while ongoing
	// AwakeMin and Drain are nil for pmset sessions; Drain is also nil with
	// under five awake minutes.
	AwakeMin *int
	StartPct int
	EndPct   int
	Drain    *float64 // %/hr over awake time
	Ongoing  bool
	DataGap  bool
	Source   string
}

// Lasted answers "battery lasted": the ongoing session so far, or the last
// completed one.
type Lasted struct {
	Minutes int
	Ongoing bool
}

// Totals are batlog intervals clipped to the range, in seconds.
type Totals struct {
	BatterySec, ACSec, SleepSec, GapSec int64
}

// Result is the history of one range.
type Result struct {
	Events      []Event // inside the range, ascending
	Sessions    []Session
	FirstCharge *Event
	LastUnplug  *Event
	Lasted      *Lasted
	Totals      Totals
}

// Build computes the history of in.From..in.To.
func Build(in Input) Result {
	from, to := in.From.Unix(), in.To.Unix()
	var r Result

	pmEvents, pmSessions := fromPmset(in, from, to)
	r.Events = append(r.Events, pmEvents...)
	r.Sessions = append(r.Sessions, pmSessions...)

	events, sessions, totals := fromSamples(in, from, to)
	r.Events = append(r.Events, events...)
	r.Sessions = append(r.Sessions, sessions...)
	r.Totals = totals

	// Already ascending: pmset events are all before the first sample.
	for i := range r.Events {
		e := &r.Events[i]
		if e.Plugged && r.FirstCharge == nil {
			r.FirstCharge = e
		}
		if !e.Plugged {
			r.LastUnplug = e
		}
	}
	if n := len(sessions); n > 0 {
		last := sessions[n-1]
		r.Lasted = &Lasted{Minutes: *last.AwakeMin, Ongoing: last.Ongoing}
	}
	return r
}

// fromSamples walks consecutive batlog samples.
func fromSamples(in Input, from, to int64) ([]Event, []Session, Totals) {
	var (
		events   []Event
		sessions []Session
		tot      Totals
		cur      *Session // the battery run being walked
		awake    int64
		drop     int
	)
	ss := in.Samples
	closeRun := func(end int64, endPct int, ongoing bool) {
		cur.EndPct = endPct
		cur.Ongoing = ongoing
		if !ongoing {
			cur.End = end
		}
		m := int(awake / 60)
		cur.AwakeMin = &m
		if awake >= minDrainSec {
			d := math.Round(float64(drop)*3600/float64(awake)*10) / 10
			cur.Drain = &d
		}
		if cur.Ongoing || cur.End > from {
			sessions = append(sessions, *cur)
		}
		cur = nil
	}
	for i, s := range ss {
		if i > 0 && s.OnAC != ss[i-1].OnAC {
			if s.TS >= from && s.TS <= to {
				events = append(events, Event{TS: s.TS, Plugged: s.OnAC, Pct: s.Pct, Source: SourceBatlog})
			}
			if s.OnAC && cur != nil {
				closeRun(s.TS, s.Pct, false)
			}
		}
		if !s.OnAC && cur == nil {
			cur = &Session{Start: s.TS, StartPct: s.Pct, Source: SourceBatlog}
			awake, drop = 0, 0
		}
		if i+1 == len(ss) {
			break
		}
		next := ss[i+1]
		dt := next.TS - s.TS
		clipped := overlap(s.TS, next.TS, from, to)
		switch {
		case dt <= sleepGap && s.OnAC:
			tot.ACSec += clipped
		case dt <= sleepGap:
			tot.BatterySec += clipped
			awake += dt
			drop += s.Pct - next.Pct
		case startInside(in.RunStarts, s.TS, next.TS):
			tot.GapSec += clipped
			if cur != nil {
				cur.DataGap = true
			}
		default:
			tot.SleepSec += clipped
		}
	}
	if n := len(ss); n > 0 {
		// After the last sample the Mac slept until it woke; a recorder
		// silent for longer than that is down.
		last, tail := ss[n-1], ss[n-1].TS
		if in.WokeAt > tail {
			tot.SleepSec += overlap(tail, in.WokeAt, from, to)
			tail = in.WokeAt
		}
		stale := to-tail > staleAfter
		if stale {
			tot.GapSec += overlap(tail, to, from, to)
		}
		if cur != nil {
			cur.DataGap = cur.DataGap || stale
			closeRun(0, last.Pct, true)
		}
	}
	return events, sessions, tot
}

// fromPmset covers the part of the range before the daemon's first sample.
// Its sessions start only at an unplug: the log's first line is where the
// log begins, not when the Mac went on battery.
func fromPmset(in Input, from, to int64) ([]Event, []Session) {
	var rs []pmset.Reading
	for _, r := range in.Pmset {
		if in.FirstSampleTS == 0 || r.TS < in.FirstSampleTS {
			rs = append(rs, r)
		}
	}
	var (
		events   []Event
		sessions []Session
		cur      *Session
	)
	keep := func(s Session) {
		if (s.Ongoing || s.End > from) && s.Start <= to {
			sessions = append(sessions, s)
		}
	}
	for i := 1; i < len(rs); i++ {
		r, prev := rs[i], rs[i-1]
		if cur != nil {
			cur.EndPct = r.Pct
		}
		if r.OnAC == prev.OnAC {
			continue
		}
		if r.TS >= from && r.TS <= to {
			events = append(events, Event{TS: r.TS, Plugged: r.OnAC, Pct: r.Pct, Source: SourcePmset})
		}
		if r.OnAC && cur != nil {
			cur.End = r.TS
			keep(*cur)
			cur = nil
		} else if !r.OnAC {
			cur = &Session{Start: r.TS, StartPct: r.Pct, EndPct: r.Pct, Source: SourcePmset}
		}
	}
	if cur != nil {
		if in.FirstSampleTS != 0 {
			cur.End = in.FirstSampleTS
		} else {
			cur.Ongoing = true
		}
		keep(*cur)
	}
	return events, sessions
}

// startInside reports whether a recorder start falls in (a, b]: a start at
// a belongs to the run that wrote a.
func startInside(starts []int64, a, b int64) bool {
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
