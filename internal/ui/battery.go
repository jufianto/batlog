package ui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/jufianto/batlog/internal/history"
	"github.com/jufianto/batlog/internal/localday"
	"github.com/jufianto/batlog/internal/store"
	"github.com/jufianto/batlog/internal/textfmt"
)

// batItem is a battery session or a charge in the Battery list.
type batItem struct {
	id    string // "s:" or "c:" and the F3 ID
	start int64
	s     *history.Session
	c     *history.ChargeSession
}

// batItems lists the range's batlog sessions and charges, newest first.
func batItems(h History) []batItem {
	var out []batItem
	for i := range h.Result.Sessions {
		s := &h.Result.Sessions[i]
		if s.Source == history.SourceBatlog {
			out = append(out, batItem{id: "s:" + s.ID, start: s.Start, s: s})
		}
	}
	for i := range h.Result.ChargeSessions {
		c := &h.Result.ChargeSessions[i]
		out = append(out, batItem{id: "c:" + c.ID, start: c.Start, c: c})
	}
	slices.SortStableFunc(out, func(a, b batItem) int { return cmp.Compare(b.start, a.start) })
	return out
}

func ids(items []batItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.id
	}
	return out
}

// chartRows is the Battery chart's height for a body h rows tall.
func chartRows(h int) int { return min(max(h/3, 3), 8) }

// batListH is how many list rows fit under the chart, totals and header.
func (m Model) batListH() int {
	return m.bodyH() - (chartRows(m.bodyH()) + 4) - 3
}

// chartSpan is what the Battery chart covers: the range, or the open
// session's or charge's own span.
func (m Model) chartSpan() (from, to time.Time) {
	t := m.now()
	if m.batOpen && m.hist != nil {
		items := batItems(*m.hist)
		if m.bat.cur < len(items) {
			it := items[m.bat.cur]
			end, ongoing := int64(0), false
			if it.s != nil {
				end, ongoing = it.s.End, it.s.Ongoing
			} else {
				end, ongoing = it.c.End, it.c.Ongoing
			}
			if ongoing {
				end = t.Unix()
			}
			loc := t.Location()
			return time.Unix(it.start, 0).In(loc), time.Unix(max(end, it.start+60), 0).In(loc)
		}
	}
	return m.rng.From, m.rng.ChartEnd()
}

// chartColumns are the Battery chart's columns at the current width.
func (m Model) chartColumns() []column {
	if m.hist == nil {
		return nil
	}
	from, to := m.chartSpan()
	return pctColumns(max(0, m.mainW()-2-4), m.hist.Samples, m.hist.RunStarts, from.Unix(), to.Unix())
}

// moveCursor shows the chart's cursor at the newest data, then moves it.
func (m *Model) moveCursor(step int) {
	cs := m.chartColumns()
	if len(cs) == 0 {
		return
	}
	if m.cursor < 0 || m.cursor >= len(cs) {
		m.cursor = lastData(cs)
		return
	}
	m.cursor = min(max(m.cursor+step, 0), len(cs)-1)
}

func (m Model) batteryView(title string, w, h int) (string, []string) {
	if m.hist == nil {
		return title, []string{"loading…"}
	}
	hs := *m.hist
	if len(hs.Samples) == 0 {
		return title, []string{"no history yet — run 'batlog daemon install'"}
	}
	items := batItems(hs)
	if m.batOpen && m.bat.cur < len(items) {
		return m.batDetail(items[m.bat.cur], w, h)
	}
	from, to := m.chartSpan()
	out := pctChart(m.chartColumns(), chartRows(h), from, to, m.cursor, m.st)
	out = append(out, textfmt.Totals(hs.Result.Totals), "")
	if len(items) == 0 {
		return title, append(out, "no sessions in this range")
	}
	out = append(out, m.st.paint(m.st.sleep, fit("  "+batRow(w, "ID", "  ", "WHEN", "BATTERY", "LENGTH", "RESULT"), w)))
	t := m.now()
	for i := m.bat.top; i < len(items) && len(out) < h; i++ {
		line := fit(m.itemRow(items[i], t, w), w-2)
		if i == m.bat.cur {
			out = append(out, "▸ "+m.st.paint(m.st.sel, line))
		} else {
			out = append(out, "  "+line)
		}
	}
	return title, out
}

// batRow lays out a list row for a body w cells wide: narrower bodies
// drop WHEN (the ID holds the start), then LENGTH, so RESULT stays.
func batRow(w int, id, mark, when, pct, length, result string) string {
	row := pad(id, 10) + " " + pad(mark, 2) + " "
	if w >= 90 {
		row += pad(when, 17) + " "
	}
	row += pad(pct, 8) + " "
	if w >= 64 {
		row += pad(length, 7) + " "
	}
	return row + result
}

// itemRow is one list row: a session's drain, or a charge's outcome.
func (m Model) itemRow(it batItem, t time.Time, w int) string {
	var tags []string
	if s := it.s; s != nil {
		length, result := "", "—"
		if s.AwakeMin != nil {
			length = textfmt.Duration(*s.AwakeMin)
		}
		if s.Drain != nil {
			result = fmt.Sprintf("%.1f %%/hr", *s.Drain)
		}
		if s.Ongoing {
			tags = append(tags, "(ongoing)")
		}
		if s.DataGap {
			tags = append(tags, "(data gap)")
		}
		return batRow(w, s.ID, "", textfmt.Clock(s.Start, t), fmt.Sprintf("%d→%d%%", s.StartPct, s.EndPct),
			length, strings.Join(append([]string{result}, tags...), "  "))
	}
	c := it.c
	end := c.End
	if c.Ongoing {
		end = t.Unix()
		tags = append(tags, "(ongoing)")
	}
	if c.DataGap {
		tags = append(tags, "(data gap)")
	}
	result := textfmt.JoinDot(textfmt.ChargeOutcome(*c, m.estToFull(*c)))
	return batRow(w, c.ID, "⚡", textfmt.Clock(c.Start, t), fmt.Sprintf("%d→%d%%", c.StartPct, c.EndPct),
		textfmt.Duration(int((end-c.Start)/60)), strings.Join(append([]string{result}, tags...), "  "))
}

// estToFull is the curve's time to full for a charge still charging, as
// `history` computes it.
func (m Model) estToFull(c history.ChargeSession) *int {
	if m.hist == nil || m.hist.Curve == nil || !c.Estimable() {
		return nil
	}
	v := m.hist.Curve.MinutesToFull(c.EndPct)
	return &v
}

// batDetail is a session's or a charge's chart over its own span, and its
// figures.
func (m Model) batDetail(it batItem, w, h int) (string, []string) {
	hs, t := *m.hist, m.now()
	from, to := m.chartSpan()
	out := pctChart(m.chartColumns(), chartRows(h), from, to, m.cursor, m.st)
	ongoing := it.s != nil && it.s.Ongoing || it.c != nil && it.c.Ongoing
	out = append(out, wrapDots(milestones(hs.Samples, from.Unix(), to.Unix(), it.c != nil, ongoing, t), w)...)
	out = append(out, "")
	if s := it.s; s != nil {
		title := "1 Battery · session " + s.ID
		switch {
		case m.sessFor != s.ID:
			out = append(out, "loading…")
		default:
			out = append(out, m.sessText...)
		}
		return title, out
	}
	c := it.c
	span := textfmt.Clock(c.Start, t) + " → now"
	if !c.Ongoing {
		span = textfmt.Clock(c.Start, t) + " → " + textfmt.Clock(c.End, t)
	}
	out = append(out, textfmt.JoinDot([]string{"charge " + c.ID, span, fmt.Sprintf("%d%% → %d%%", c.StartPct, c.EndPct)}))
	out = append(out, textfmt.ChargeOutcome(*c, m.estToFull(*c))...)
	if c.Ongoing && m.live != nil && m.live.Status.MacOSToFull != nil {
		out = append(out, "macOS says full in "+textfmt.Duration(*m.live.Status.MacOSToFull))
	}
	if c.DataGap {
		out = append(out, "the recorder was down for part of it (data gap)")
	}
	return "1 Battery · charge " + c.ID, out
}

// milestones are when a session crossed each tenth on the way down, or a
// charge on the way up, from its start to how it ended: "99% yesterday
// 23:27 → 90% 01:30 → … → plugged in 21:56 at 21%". A crossing is the
// first sample at or past the tenth. A time names its day only when the
// day changes.
func milestones(samples []store.Sample, from, to int64, charge, ongoing bool, t time.Time) []string {
	var in []store.Sample
	for _, s := range samples {
		if s.TS >= from && s.TS <= to {
			in = append(in, s)
		}
	}
	if len(in) == 0 {
		return nil
	}
	day := localday.Start(time.Unix(in[0].TS, 0).In(t.Location()))
	clock := func(ts int64) string {
		at := time.Unix(ts, 0).In(t.Location())
		if d := localday.Start(at); !d.Equal(day) {
			day = d
			return textfmt.Clock(ts, t)
		}
		return at.Format("15:04")
	}
	first, last := in[0], in[len(in)-1]
	out := []string{fmt.Sprintf("%d%% %s", first.Pct, textfmt.Clock(first.TS, t))}
	next := (first.Pct - 1) / 10 * 10 // the tenth below the start
	if charge {
		next = (first.Pct/10 + 1) * 10
	}
	for _, s := range in[1:] {
		for charge && next <= 100 && s.Pct >= next || !charge && next > 0 && s.Pct <= next {
			out = append(out, fmt.Sprintf("%d%% %s", next, clock(s.TS)))
			if charge {
				next += 10
			} else {
				next -= 10
			}
		}
	}
	switch {
	case ongoing:
		out = append(out, fmt.Sprintf("now %d%%", last.Pct))
	case charge:
		out = append(out, fmt.Sprintf("unplugged %s at %d%%", clock(to), last.Pct))
	default:
		out = append(out, fmt.Sprintf("plugged in %s at %d%%", clock(to), last.Pct))
	}
	return out
}

// wrapDots joins parts with " → " in lines at most w cells wide.
func wrapDots(parts []string, w int) []string {
	var lines []string
	line := ""
	for _, p := range parts {
		switch {
		case line == "":
			line = p
		case ansi.StringWidth(line)+3+ansi.StringWidth(p) <= w:
			line += " → " + p
		default:
			lines = append(lines, line+" →")
			line = p
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}
