package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// styles colours text, or leaves it plain without colour (NO_COLOR,
// TERM=dumb, not a terminal): the layout and every figure stay the same.
type styles struct {
	color bool
	ac    lipgloss.Style // on AC, charging
	sleep lipgloss.Style // asleep, and secondary text
	warn  lipgloss.Style
	bad   lipgloss.Style
	title lipgloss.Style
	sel   lipgloss.Style // the selected row
}

func newStyles(color bool) styles {
	return styles{
		color: color,
		ac:    lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
		sleep: lipgloss.NewStyle().Faint(true),
		warn:  lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		bad:   lipgloss.NewStyle().Foreground(lipgloss.Color("1")),
		title: lipgloss.NewStyle().Bold(true),
		sel:   lipgloss.NewStyle().Reverse(true),
	}
}

func (s styles) paint(st lipgloss.Style, text string) string {
	if !s.color || text == "" {
		return text
	}
	return st.Render(text)
}

// fit pads or cuts a line to exactly w cells, colour codes kept.
func fit(line string, w int) string {
	if w <= 0 {
		return ""
	}
	if n := ansi.StringWidth(line); n > w {
		return ansi.Truncate(line, w, "…")
	} else if n < w {
		return line + strings.Repeat(" ", w-n)
	}
	return line
}

// pad is fit for a column: at least w cells, never cut.
func pad(s string, w int) string {
	if n := ansi.StringWidth(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// box frames lines in w×h with a title in the top border.
func box(title string, lines []string, w, h int, st styles) []string {
	inner := w - 2
	top := "┌"
	if title != "" {
		t := " " + title + " "
		if ansi.StringWidth(t) > inner {
			t = ansi.Truncate(t, inner, "…")
		}
		top += st.paint(st.title, t) + strings.Repeat("─", max(0, inner-ansi.StringWidth(t)))
	} else {
		top += strings.Repeat("─", inner)
	}
	out := []string{top + "┐"}
	for i := 0; i < h-2; i++ {
		l := ""
		if i < len(lines) {
			l = lines[i]
		}
		out = append(out, "│"+fit(l, inner)+"│")
	}
	return append(out, "└"+strings.Repeat("─", inner)+"┘")
}
