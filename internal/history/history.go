// Package history turns recorded samples into F3's plug and unplug events,
// battery sessions, headline answers and totals. It is pure: the caller
// loads samples, recorder starts and pmset readings.
package history

import (
	"errors"
	"math"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/jufianto/batlog/internal/localday"
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
	// ID is the start in local time as MMDD-HHMM; a later session starting
	// in the same minute gets b, c, …. `top --session` takes it.
	ID    string
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

// ChargeSession is one run on AC, from a plug-in to the unplug. Only batlog
// samples make them: pmset has no charging detail.
type ChargeSession struct {
	// ID is the plug-in in local time as MMDD-HHMM, with b, c, … for a
	// later one in the same minute, as for battery sessions.
	ID       string
	Start    int64 // the plug event
	End      int64 // the unplug; 0 while ongoing
	StartPct int
	EndPct   int // the last sample on AC: at the unplug, or now
	// FullAt is the first sample at 100 %, 0 if the charge never got
	// there. Macs charge asleep, so after a sleep gap the battery may have
	// been full for a while: FullUpperBound says the time to full is at
	// most FullAt - Start.
	FullAt         int64
	FullUpperBound bool
	// MaxPct is the highest percent on the charger and MaxAt the first
	// sample at it, so a charge unplugged before full still says how long
	// it took to get as far as it did; MaxUpperBound as FullUpperBound.
	MaxPct        int
	MaxAt         int64
	MaxUpperBound bool
	// AtFullMin is the time from FullAt to the unplug (or now), sleep on
	// the charger included: a full battery left plugged in overnight is
	// the habit it measures.
	AtFullMin int
	// HoldMin is the awake time on AC, not charging, below 100 %, before
	// the battery was first full: macOS holding the charge (Optimized
	// Charging, a charge limit, heat) or a charger too weak to charge. After
	// full, macOS lets it drift a few percent before topping up; that is
	// time at full, not a hold. HoldPct is the percent it held at last.
	HoldMin, HoldPct int
	Charging         bool // charging at the newest sample of an ongoing one
	// Current is true when that newest sample is from the last 90 s: just
	// after a wake the newest sample can be hours old, and its percent says
	// nothing about the battery now.
	Current bool
	Ongoing bool
	DataGap bool
}

// Estimable reports whether the curve can give c a time to full: ongoing,
// charging below 100 % at a sample from the last 90 s, with the recorder
// writing. An old percent gives no time to full.
func (c ChargeSession) Estimable() bool {
	return c.Ongoing && c.Charging && c.Current && !c.DataGap && c.EndPct < 100
}

// Lasted answers "battery lasted": the ongoing session so far, or the last
// completed one.
type Lasted struct {
	Minutes int
	Ongoing bool
}

// Totals are batlog intervals clipped to the range, in seconds, and the
// percent dropped over awake on-battery intervals that start in the range.
type Totals struct {
	BatterySec, ACSec, SleepSec, GapSec int64
	PctUsed                             int
}

// Result is the history of one range.
type Result struct {
	Events         []Event // inside the range, ascending
	Sessions       []Session
	ChargeSessions []ChargeSession
	FirstCharge    *Event
	LastUnplug     *Event
	Lasted         *Lasted
	Totals         Totals
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
	r.ChargeSessions = charges(in, from, to)
	// Samples run to now; a range that ends earlier (a past day) does not
	// hold what started after it.
	r.Sessions = slices.DeleteFunc(r.Sessions, func(s Session) bool { return s.Start > to })
	r.ChargeSessions = slices.DeleteFunc(r.ChargeSessions, func(c ChargeSession) bool { return c.Start > to })
	assignIDs(r.Sessions, in.From.Location())
	ids := make([]int64, len(r.ChargeSessions))
	for i, c := range r.ChargeSessions {
		ids[i] = c.Start
	}
	for i, id := range idsFor(ids, in.From.Location()) {
		r.ChargeSessions[i].ID = id
	}

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
	// pmset sessions all come before batlog's.
	if n := len(r.Sessions); n > 0 && r.Sessions[n-1].Source == SourceBatlog {
		last := r.Sessions[n-1]
		r.Lasted = &Lasted{Minutes: *last.AwakeMin, Ongoing: last.Ongoing}
	}
	return r
}

const idLayout = "0102-1504"

// assignIDs names sessions, which are in start order, by their start minute.
func assignIDs(ss []Session, loc *time.Location) {
	starts := make([]int64, len(ss))
	for i := range ss {
		starts[i] = ss[i].Start
	}
	for i, id := range idsFor(starts, loc) {
		ss[i].ID = id
	}
}

// idsFor names starts, ascending, by their minute: MMDD-HHMM, then b, c, …
// for later ones in the same minute.
func idsFor(starts []int64, loc *time.Location) []string {
	ids := make([]string, len(starts))
	seen := map[string]int{}
	for i, s := range starts {
		id := time.Unix(s, 0).In(loc).Format(idLayout)
		if n := seen[id]; n > 0 && n < 26 {
			ids[i] = id + string(rune('a'+n))
		} else {
			ids[i] = id
		}
		seen[id]++
	}
	return ids
}

var idPattern = regexp.MustCompile(`^(\d\d)(\d\d)-(\d\d)(\d\d)[b-z]?$`)

// IDDay returns the first instant of the local day a session ID started on.
// IDs carry no year: it is the most recent one in which that minute exists
// and is not after now. A history built from that day to now holds the
// session under the same ID.
func IDDay(id string, now time.Time) (time.Time, error) {
	m := idPattern.FindStringSubmatch(id)
	if m == nil {
		return time.Time{}, errors.New("want a session ID like 0926-1656 (see batlog history)")
	}
	var v [4]int
	for i := range v {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	month, day, hour, minute := time.Month(v[0]), v[1], v[2], v[3]
	if hour > 23 || minute > 59 {
		return time.Time{}, errors.New("session ID " + id + " has no such time")
	}
	loc := now.Location()
	for y := now.Year(); y >= now.Year()-8; y-- {
		noon := time.Date(y, month, day, 12, 0, 0, 0, loc)
		if noon.Month() != month || noon.Day() != day {
			continue // no such date this year (29 Feb)
		}
		if !time.Date(y, month, day, hour, minute, 0, 0, loc).After(now) {
			return localday.Start(noon), nil
		}
	}
	return time.Time{}, errors.New("session ID " + id + " has no such date")
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
			if s.TS >= from && s.TS < to {
				tot.PctUsed += s.Pct - next.Pct
			}
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

// charges walks the batlog samples for runs on AC. A run counts from a plug
// event the samples hold: one already on AC at the first sample has no
// known start, so it is left out (the caller loads from the battery sample
// before any AC run the range starts in).
func charges(in Input, from, to int64) []ChargeSession {
	var (
		out  []ChargeSession
		cur  *ChargeSession
		hold int64
	)
	ss := in.Samples
	closeRun := func(end int64, ongoing bool) {
		cur.Ongoing = ongoing
		if !ongoing {
			cur.End = end
		}
		if cur.FullAt != 0 {
			cur.AtFullMin = int((end - cur.FullAt) / 60)
		}
		cur.HoldMin = int(hold / 60)
		if cur.Ongoing || cur.End > from {
			out = append(out, *cur)
		}
		cur = nil
	}
	for i, s := range ss {
		if i > 0 && s.OnAC != ss[i-1].OnAC {
			if s.OnAC {
				cur = &ChargeSession{Start: s.TS, StartPct: s.Pct}
				hold = 0
			} else if cur != nil {
				closeRun(s.TS, false)
			}
		}
		if cur == nil || !s.OnAC {
			continue
		}
		cur.EndPct, cur.Charging = s.Pct, s.Charging
		// Woke to a higher percent than before the sleep: it got there at
		// some point while asleep.
		woke := s.TS > cur.Start && s.TS-ss[i-1].TS > sleepGap && ss[i-1].Pct < s.Pct
		if s.Pct > cur.MaxPct || cur.MaxAt == 0 {
			cur.MaxPct, cur.MaxAt, cur.MaxUpperBound = s.Pct, s.TS, woke
		}
		if s.Pct >= 100 && cur.FullAt == 0 {
			cur.FullAt, cur.FullUpperBound = s.TS, woke
		}
		if i+1 == len(ss) {
			break
		}
		next := ss[i+1]
		switch dt := next.TS - s.TS; {
		case dt <= sleepGap:
			// Up to the next sample, as every interval: the unplug lands
			// somewhere in the last one.
			if !s.Charging && s.Pct < 100 && cur.FullAt == 0 {
				hold += dt
				cur.HoldPct = s.Pct
			}
		case startInside(in.RunStarts, s.TS, next.TS):
			cur.DataGap = true
		}
	}
	if cur != nil {
		// After the last sample the Mac slept until it woke, as fromSamples
		// reads it; a recorder silent for longer than that is down.
		end, last := to, ss[len(ss)-1].TS
		tail := max(last, in.WokeAt)
		if to-tail > staleAfter {
			cur.DataGap = true
			end = tail
		}
		cur.Current = to-last <= sleepGap
		closeRun(end, true)
	}
	return out
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
