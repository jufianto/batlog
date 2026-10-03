package ui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jufianto/batlog/internal/localday"
	"github.com/jufianto/batlog/internal/textfmt"
	"github.com/jufianto/batlog/internal/top"
)

// appsListH is how many table rows fit above the notes.
func (m Model) appsListH() int { return m.bodyH() - 1 - 3 }

// shareBarW is the width of the share bar in the Apps table.
const shareBarW = 12

func (m Model) appsView(title string, w, h int) (string, []string) {
	switch a := m.apps; {
	case a == nil:
		return title, []string{"loading…"}
	case !a.Recorded:
		return title, []string{"no app energy recorded yet — run 'batlog daemon install'"}
	case m.appOpen && m.appl.cur < len(a.Rows):
		return m.appDetail(a.Rows[m.appl.cur], w, h)
	case len(a.Rows) == 0:
		return title, []string{"no app energy recorded for this range",
			"recording started " + textfmt.Clock(a.First, m.now())}
	}
	a := m.apps
	out := []string{m.st.paint(m.st.sleep, fit("  "+appRow(" #", "APP", "SHARE", "", "BATTERY COST"), w))}
	top := a.Rows[0].Share
	t := m.now()
	for i := m.appl.top; i < len(a.Rows) && len(out) <= m.appsListH(); i++ {
		r := a.Rows[i]
		line := fit(appRow(fmt.Sprintf("%2d", i+1), appLabel(r), textfmt.Share(r.Share), shareBar(r.Share, top), cost(r, i == 0)), w-2)
		if i == m.appl.cur {
			out = append(out, "▸ "+m.st.paint(m.st.sel, line))
		} else {
			out = append(out, "  "+line)
		}
	}
	var notes []string
	if a.Since != 0 {
		notes = append(notes, fmt.Sprintf("app energy from about %s only; battery cost covers the %d%% used since",
			textfmt.Clock(a.Since, t), a.CoveredPctUsed))
	}
	if a.Short {
		notes = append(notes, "short window — shares may be noisy")
	}
	notes = append(notes, "shares are of app energy (kernel counters); battery cost is an estimate")
	for len(out) < h-len(notes) {
		out = append(out, "")
	}
	for _, n := range notes {
		out = append(out, m.st.paint(m.st.sleep, n))
	}
	return title, out
}

func appRow(n, app, share, bar, cost string) string {
	return pad(n, 3) + " " + pad(app, 22) + " " + pad(share, 6) + " " + pad(bar, shareBarW) + " " + cost
}

func appLabel(r top.Row) string {
	if r.System {
		return r.App + " ⚙"
	}
	return r.App
}

// shareBar is a share relative to the top app's, in eighths of a cell.
func shareBar(share, top float64) string {
	if top <= 0 {
		return ""
	}
	n := int(math.Round(share / top * shareBarW * 8))
	s := strings.Repeat("█", n/8)
	if n%8 > 0 {
		s += string([]rune("▏▎▍▌▋▊▉")[8-n%8-1])
	}
	return s
}

// cost is F4's battery cost column; the first row says what it is.
func cost(r top.Row, first bool) string {
	if r.BatteryPct == nil {
		return "—"
	}
	c := fmt.Sprintf("≈ %.0f%%", *r.BatteryPct)
	if p := *r.BatteryPct; p > 0 && p < 0.5 {
		c = "< 1%"
	}
	if first {
		c += " of battery"
	}
	return c
}

// bins are an app chart's bars: hours for a day, days for a week.
func bins(r Range) []time.Time {
	var edges []time.Time
	end := r.ChartEnd()
	if r.Daily() {
		for t := r.From; t.Before(end); t = t.Add(time.Hour) {
			edges = append(edges, t)
		}
	} else {
		for t := r.From; t.Before(end); t = localday.Next(t) {
			edges = append(edges, t)
		}
	}
	return append(edges, end)
}

func (m Model) appDetail(r top.Row, w, h int) (string, []string) {
	title := "2 Apps · " + r.App
	if m.serFor != m.seriesKey() {
		return title, []string{"loading…"}
	}
	edges := bins(m.rng)
	sums := make([]float64, len(edges)-1)
	total := 0.0
	for _, p := range m.series {
		for b := range sums {
			if p.TS >= edges[b].Unix() && p.TS < edges[b+1].Unix() {
				sums[b] += p.J
				total += p.J
				break
			}
		}
	}
	cols := w - 6
	slot := max(1, cols/len(sums))
	vals := make([]float64, len(sums)*slot)
	hi, peak := 0.0, 0
	for b, v := range sums {
		for c := range slot {
			vals[b*slot+c] = math.NaN()
			if c < max(1, slot-1) && v > 0 {
				vals[b*slot+c] = v
			}
		}
		if v > hi {
			hi, peak = v, b
		}
	}
	out := valueChart(vals, chartRows(h)+2, 0, hi, func(float64) string { return "" }, edges[0], edges[len(edges)-1], m.st)
	out = append(out, "", textfmt.JoinDot([]string{r.App, textfmt.Share(r.Share) + " of app energy", cost(r, true)}))
	if total > 0 {
		var when string
		if m.rng.Daily() {
			when = edges[peak].Format("15:04") + "–" + edges[peak+1].Format("15:04")
		} else {
			when = edges[peak].Format("Mon 02 Jan")
		}
		out = append(out, fmt.Sprintf("busiest %s: %s of its energy in the range", when, textfmt.Share(hi/total)))
	}
	return title, out
}
