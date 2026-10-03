package ui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/jufianto/batlog/internal/localday"
	"github.com/jufianto/batlog/internal/store"
)

// sleepGap matches F3: a longer interval between samples is sleep.
const sleepGap = 90

// kind is what a chart column shows.
type kind int

const (
	none    kind = iota // no data: before the first sample, a data gap, the future
	battery             // awake on battery
	onAC                // awake on AC
	asleep              // asleep, at the percent it went to sleep at
)

type column struct {
	kind kind
	pct  int
}

// pctColumns splits from..to into n slices. A slice with samples shows the
// lowest percent in it, so a drain is never hidden, on AC when most of its
// samples are. A slice inside a sleep shows the percent before the sleep;
// one inside a gap the recorder was down for (a start in it) shows nothing.
func pctColumns(n int, samples []store.Sample, runStarts []int64, from, to int64) []column {
	cols := make([]column, n)
	if n == 0 || to <= from {
		return cols
	}
	span := float64(to - from)
	i := 0
	for c := range cols {
		a := from + int64(span*float64(c)/float64(n))
		b := from + int64(span*float64(c+1)/float64(n))
		for i < len(samples) && samples[i].TS < a {
			i++
		}
		lo, cnt, ac := 101, 0, 0
		j := i
		for ; j < len(samples) && samples[j].TS < b; j++ {
			lo = min(lo, samples[j].Pct)
			if !awake(samples, j) {
				continue
			}
			cnt++
			if samples[j].OnAC {
				ac++
			}
		}
		switch {
		case cnt > 0:
			k := battery
			if ac*2 > cnt {
				k = onAC
			}
			cols[c] = column{k, lo}
			continue
		case j > i:
			// Only dark wakes: a Mac asleep that woke for a moment.
			cols[c] = column{asleep, lo}
			continue
		}
		if i == 0 {
			continue // before the first sample
		}
		p := samples[i-1]
		if j == len(samples) {
			// After the newest sample: it holds for one interval.
			if a <= p.TS+sleepGap {
				cols[c] = column{state(p), p.Pct}
			}
			continue
		}
		q := samples[j]
		switch {
		case q.TS-p.TS <= sleepGap:
			cols[c] = column{state(p), p.Pct}
		case !startIn(runStarts, p.TS, q.TS):
			cols[c] = column{asleep, p.Pct}
		}
	}
	return cols
}

// awake: a sample with a neighbour within sleepGap is part of awake time
// (F3). A lone one is a dark wake: macOS woke briefly while asleep.
func awake(ss []store.Sample, i int) bool {
	return i > 0 && ss[i].TS-ss[i-1].TS <= sleepGap || i+1 < len(ss) && ss[i+1].TS-ss[i].TS <= sleepGap
}

func state(s store.Sample) kind {
	if s.OnAC {
		return onAC
	}
	return battery
}

func startIn(starts []int64, a, b int64) bool {
	for _, s := range starts {
		if s > a && s <= b {
			return true
		}
	}
	return false
}

var eighths = []rune(" ▁▂▃▄▅▆▇")

// bar is one column's cells, top row first, for v in 0..1. Any v above 0
// shows at least the lowest eighth. A shade (▓ ░) has no eighths: its top
// cell is whole from half full.
func bar(v float64, rows int, full rune) []rune {
	cells := make([]rune, rows)
	n := int(math.Round(v * float64(rows*8)))
	if v > 0 && n == 0 {
		n = 1
	}
	for r := range rows {
		fill := n - (rows-1-r)*8 // eighths in this row, bottom row last
		switch {
		case fill >= 8:
			cells[r] = full
		case fill > 0 && full != '█':
			cells[r] = ' '
			if fill >= 4 {
				cells[r] = full
			}
		case fill > 0:
			cells[r] = eighths[fill]
		default:
			cells[r] = ' '
		}
	}
	return cells
}

// pctChart draws battery percent over from..to: a 4-cell y axis, rows of
// bars, the x axis with the cursor's ▲, the time labels, a legend and the
// cursor's readout; cols+4 wide, rows+4 tall. cursor is a column, or -1
// for none. Without colour, AC columns are ▓ and asleep ones ░ so the
// source still shows.
func pctChart(cs []column, rows int, from, to time.Time, cursor int, st styles) []string {
	cols := len(cs)
	grid := make([][]string, rows)
	for r := range grid {
		grid[r] = make([]string, cols)
	}
	for c, col := range cs {
		full, style := '█', st.bat
		switch col.kind {
		case onAC:
			full, style = '▓', st.ac
		case asleep:
			full, style = '░', st.sleep
		}
		if st.color {
			full = '█'
		}
		if c == cursor {
			style = st.cursor
		}
		var cells []rune
		if col.kind == none {
			cells = bar(0, rows, full)
		} else {
			cells = bar(float64(col.pct)/100, rows, full)
		}
		for r := range rows {
			grid[r][c] = st.paint(style, string(cells[r]))
		}
	}
	out := make([]string, 0, rows+4)
	for r := range rows {
		label := "   "
		switch {
		case r == 0:
			label = "100"
		case r == rows-1:
			label = "  0"
		case rows >= 5 && r == rows/2:
			label = " 50"
		}
		out = append(out, label+"│"+strings.Join(grid[r], ""))
	}
	axis := []rune(strings.Repeat("─", cols))
	if cursor >= 0 && cursor < cols {
		axis[cursor] = '▲'
	}
	out = append(out, "   └"+string(axis), "    "+timeLabels(cols, from, to))
	return append(out, legend(st), readout(cs, from, to, cursor, st))
}

// legend names the chart's colours, or its shades without colour.
func legend(st styles) string {
	if !st.color {
		return "█ on battery  ▓ on AC  ░ asleep  blank: no data · ← → read a time"
	}
	return st.paint(st.bat, "█") + " on battery  " + st.paint(st.ac, "█") + " on AC  " +
		st.paint(st.sleep, "█") + " asleep  blank: no data · ← → read a time"
}

// readout says what the cursor's column holds: when, the percent, and
// whether the Mac was on battery, on AC or asleep.
func readout(cs []column, from, to time.Time, cursor int, st styles) string {
	if cursor < 0 || cursor >= len(cs) {
		return ""
	}
	span := to.Sub(from)
	a := from.Add(time.Duration(float64(span) * float64(cursor) / float64(len(cs))))
	b := from.Add(time.Duration(float64(span) * float64(cursor+1) / float64(len(cs))))
	when := a.Format("Mon 02 Jan 15:04") + "–" + b.Format("15:04")
	col := cs[cursor]
	what := map[kind]string{none: "no data", battery: "%d%% · on battery", onAC: "%d%% · on AC", asleep: "%d%% · asleep"}[col.kind]
	if col.kind != none {
		what = fmt.Sprintf(what, col.pct)
	}
	return st.paint(st.title, "▲ "+when+" · "+what)
}

// lastData is the rightmost column with data, or the last one.
func lastData(cs []column) int {
	for c := len(cs) - 1; c >= 0; c-- {
		if cs[c].kind != none {
			return c
		}
	}
	return len(cs) - 1
}

// valueChart draws bars of vals (one per column, NaN for none) scaled from
// lo to hi, with a y axis labelled by label(hi) and label(lo).
func valueChart(vals []float64, rows int, lo, hi float64, label func(float64) string, from, to time.Time, st styles) []string {
	cols := len(vals)
	top, bottom := label(hi), label(lo)
	gw := max(ansi.StringWidth(top), ansi.StringWidth(bottom))
	grid := make([][]rune, rows)
	for r := range grid {
		grid[r] = []rune(strings.Repeat(" ", cols))
	}
	for c, v := range vals {
		if math.IsNaN(v) {
			continue
		}
		f := 0.0
		if hi > lo {
			f = (v - lo) / (hi - lo)
		}
		cells := bar(min(max(f, 0), 1), rows, '█')
		for r := range rows {
			grid[r][c] = cells[r]
		}
	}
	out := make([]string, 0, rows+2)
	for r := range rows {
		l := strings.Repeat(" ", gw)
		switch r {
		case 0:
			l = fmt.Sprintf("%*s", gw, top)
		case rows - 1:
			l = fmt.Sprintf("%*s", gw, bottom)
		}
		out = append(out, l+"│"+st.paint(st.ac, string(grid[r])))
	}
	out = append(out, strings.Repeat(" ", gw)+"└"+strings.Repeat("─", cols))
	return append(out, strings.Repeat(" ", gw+1)+timeLabels(cols, from, to))
}

// timeLabels marks from..to across cols cells: hours for up to a day and a
// half, weekdays for up to eight days, dates or months beyond.
func timeLabels(cols int, from, to time.Time) string {
	line := []rune(strings.Repeat(" ", cols))
	span := to.Sub(from)
	if cols == 0 || span <= 0 {
		return string(line)
	}
	at := func(t time.Time) int { return int(float64(cols) * float64(t.Sub(from)) / float64(span)) }
	end := -1
	put := func(t time.Time, s string) {
		c := at(t)
		if c <= end || c+len([]rune(s)) > cols {
			return
		}
		copy(line[c:], []rune(s))
		end = c + len([]rune(s))
	}
	perCell := span / time.Duration(cols)
	switch {
	case span <= 36*time.Hour:
		step := 1
		for _, s := range []int{1, 2, 3, 4, 6, 12, 24} {
			step = s
			if time.Duration(s)*time.Hour >= 7*perCell {
				break
			}
		}
		t := from.Truncate(time.Hour)
		if t.Before(from) {
			t = t.Add(time.Hour)
		}
		for ; t.Before(to); t = t.Add(time.Hour) {
			if t.Hour()%step == 0 {
				put(t, t.Format("15:04"))
			}
		}
	case span <= 8*24*time.Hour:
		for t := localday.Next(from.Add(-time.Nanosecond)); t.Before(to); t = localday.Next(t) {
			put(t, t.Format("Mon"))
		}
	case span <= 62*24*time.Hour:
		every := max(1, int(7*perCell/(24*time.Hour))+1)
		for t, i := localday.Next(from.Add(-time.Nanosecond)), 0; t.Before(to); t, i = localday.Next(t), i+1 {
			if i%every == 0 {
				put(t, t.Format("02 Jan"))
			}
		}
	default:
		for t := localday.Next(from.Add(-time.Nanosecond)); t.Before(to); t = localday.Next(t) {
			if t.Day() == 1 {
				put(t, t.Format("Jan"))
			}
		}
	}
	return string(line)
}
