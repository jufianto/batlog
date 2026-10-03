package ui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jufianto/batlog/internal/store"
)

// reportView scrolls F6's text; flags are yellow and section rules bold.
func (m Model) reportView(w, h int) []string {
	if !m.repOK {
		return []string{"loading…"}
	}
	return m.styled(m.report, m.scroll[ReportView], h)
}

func (m Model) styled(lines []string, from, h int) []string {
	var out []string
	for i := from; i < len(lines) && len(out) < h; i++ {
		l := lines[i]
		switch {
		case strings.HasPrefix(l, "⚠"):
			l = m.st.paint(m.st.warn, l)
		case strings.HasPrefix(l, "── "):
			l = m.st.paint(m.st.title, l)
		}
		out = append(out, l)
	}
	return out
}

// healthView is F2 with its trend, then health % per day.
func (m Model) healthView(w, h int) []string {
	if m.health == nil {
		return []string{"loading…"}
	}
	return m.styled(m.healthLines(w), m.scroll[HealthView], h)
}

func (m Model) healthLines(w int) []string {
	if m.health == nil {
		return nil
	}
	out := splitLines(m.health.Text)
	rows := m.health.Rows
	out = append(out, "")
	if len(rows) < 2 {
		return append(out, "no daily history yet: the recorder writes one health row a day")
	}
	out = append(out, fmt.Sprintf("health per day, %s → %s", rows[0].Day, rows[len(rows)-1].Day))
	return append(out, healthChart(rows, w-8, 6, m.now().Location(), m.st)...)
}

// healthChart draws each day's health %, scaled to the data's own range so
// a 2 % change shows. Days share columns when there are more than fit.
func healthChart(rows []store.HealthRow, cols, h int, loc *time.Location, st styles) []string {
	pcts := make([]float64, len(rows))
	lo, hi := math.Inf(1), math.Inf(-1)
	for i, r := range rows {
		pcts[i] = float64(r.RawMaxMAh) * 100 / float64(r.DesignMAh)
		lo, hi = min(lo, pcts[i]), max(hi, pcts[i])
	}
	lo, hi = math.Floor(lo)-1, math.Ceil(hi)
	n := min(cols, len(pcts)*max(1, cols/len(pcts)))
	vals := make([]float64, n)
	for c := range vals {
		i := c * len(pcts) / n
		vals[c] = pcts[i]
	}
	from, _ := time.ParseInLocation("2006-01-02", rows[0].Day, loc)
	to, _ := time.ParseInLocation("2006-01-02", rows[len(rows)-1].Day, loc)
	return valueChart(vals, h, lo, hi, func(v float64) string { return fmt.Sprintf("%.0f%%", v) }, from, to.AddDate(0, 0, 1), st)
}
