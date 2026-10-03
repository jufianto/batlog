package ui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jufianto/batlog/internal/history"
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
	return m.bodyH() - (chartRows(m.bodyH()) + 2) - 3
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
	out := pctChart(w-4, chartRows(h), hs.Samples, hs.RunStarts, m.rng.From, m.rng.ChartEnd(), m.st)
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
	from, ongoing, end := it.start, false, int64(0)
	if it.s != nil {
		ongoing, end = it.s.Ongoing, it.s.End
	} else {
		ongoing, end = it.c.Ongoing, it.c.End
	}
	if ongoing {
		end = t.Unix()
	}
	out := pctChart(w-4, chartRows(h), hs.Samples, hs.RunStarts, time.Unix(from, 0), time.Unix(max(end, from+60), 0), m.st)
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
