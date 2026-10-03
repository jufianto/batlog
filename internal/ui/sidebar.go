package ui

import (
	"fmt"
	"time"

	"github.com/jufianto/batlog/internal/textfmt"
)

// staleAfter: a newest sample older than this while the Mac has been awake
// for as long means the recorder is not writing (as `daemon status`).
const staleAfter = 10 * time.Minute

// sidebar is the live status, health, the view list, the range and the
// data's age, w cells wide.
func (m Model) sidebar(w int) []string {
	var out []string
	if l := m.live; l == nil {
		out = append(out, "reading the battery…")
	} else {
		out = append(out, m.statusLines()...)
		out = append(out, "")
		h := l.Health
		hl := "health unknown"
		if h.HealthPct != nil {
			hl = fmt.Sprintf("health %.1f%%", *h.HealthPct)
		}
		if h.Cycles != nil {
			hl += fmt.Sprintf(" · %d cycles", *h.Cycles)
		}
		out = append(out, hl)
		if h.Condition != nil {
			c := "condition " + *h.Condition
			if h.NeedsService {
				c = m.st.paint(m.st.bad, c)
			}
			out = append(out, c)
		}
	}
	out = append(out, "")
	for v := range numViews {
		mark := "  "
		if v == m.view {
			mark = "▸ "
		}
		line := fmt.Sprintf("%s%d %s", mark, v+1, viewNames[v])
		if v == m.view {
			line = m.st.paint(m.st.title, line)
		}
		out = append(out, line)
	}
	out = append(out, "", "range "+m.rng.Label())
	if l := m.live; l != nil && l.NewestTS > 0 {
		t := m.now()
		age := t.Sub(time.Unix(l.NewestTS, 0))
		data := "data  " + ago(age)
		awake := l.WokeAt == 0 || t.Sub(time.Unix(l.WokeAt, 0)) > staleAfter
		if age > staleAfter && awake {
			data = m.st.paint(m.st.warn, "data  "+ago(age)+" · recorder stopped?")
		}
		out = append(out, data)
	} else if l != nil {
		out = append(out, "no history yet", "run batlog daemon install")
	}
	for i := range out {
		out[i] = fit(" "+out[i], w)
	}
	return out
}

// statusLines are F1's lines, shortened to fit the sidebar.
func (m Model) statusLines() []string {
	r := m.live.Status
	source, state := "on battery", ""
	if r.OnAC {
		source = "AC"
		switch {
		case r.Charging:
			state = "charging"
		case r.FullyCharged || r.Percent >= 100:
			state = "charged"
		default:
			state = "not charging"
		}
	}
	if r.Watts != nil && (!r.OnAC || r.Charging) {
		state = fmt.Sprintf("%.1f W", *r.Watts)
		if r.OnAC {
			state = "charging " + state
		}
	}
	head := fmt.Sprintf("%d%% · %s", r.Percent, source)
	if state != "" {
		head += " · " + state
	}
	if r.OnAC {
		head = m.st.paint(m.st.ac, head)
	}
	out := []string{head}
	if !r.OnAC {
		switch {
		case r.Drain != nil:
			out = append(out, fmt.Sprintf("drain %.1f %%/hr", *r.Drain))
		case r.Collecting:
			out = append(out, "drain collecting…")
		}
		var left []string
		if r.Drain != nil {
			est := "> 12h"
			if r.EstMinutes != nil && !r.EstOver12h {
				est = textfmt.Duration(*r.EstMinutes)
			}
			left = append(left, est)
		}
		if r.MacOSMinutes != nil {
			left = append(left, "macOS "+textfmt.Duration(*r.MacOSMinutes))
		}
		if len(left) > 0 {
			out = append(out, "left "+textfmt.JoinDot(left))
		}
	}
	var full []string
	if r.EstToFull != nil {
		full = append(full, textfmt.Duration(*r.EstToFull))
	}
	if r.MacOSToFull != nil {
		full = append(full, "macOS "+textfmt.Duration(*r.MacOSToFull))
	}
	if len(full) > 0 {
		out = append(out, "full in "+textfmt.JoinDot(full))
	}
	if r.Worst != nil {
		out = append(out, fmt.Sprintf("top %s (%.0f%%)", r.Worst.App, r.Worst.Share*100))
	}
	return out
}

func ago(d time.Duration) string {
	switch {
	case d < 90*time.Second:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	}
	return fmt.Sprintf("%d days ago", int(d.Hours()/24))
}
