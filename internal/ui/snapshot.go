package ui

import (
	"context"

	tea "charm.land/bubbletea/v2"
)

// Snapshot renders the screen at w×h after the first load and the given
// key presses, each run to completion: no terminal, no timer. Keys are
// named as Bubble Tea names them ("enter", "down", "2", "[").
func Snapshot(ctx context.Context, src Source, o Options, w, h int, keys ...string) string {
	o.static = true
	var m tea.Model = New(ctx, src, o)
	m = drain(m, func() tea.Msg { return tea.WindowSizeMsg{Width: w, Height: h} })
	m = drain(m, m.Init())
	for _, k := range keys {
		m = drain(m, func() tea.Msg { return keyPress(k) })
	}
	return m.(Model).Render()
}

// drain runs cmd and every command its messages lead to.
func drain(m tea.Model, cmd tea.Cmd) tea.Model {
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		switch msg := c().(type) {
		case nil, tea.QuitMsg:
		case tea.BatchMsg:
			queue = append(queue, msg...)
		default:
			var next tea.Cmd
			m, next = m.Update(msg)
			queue = append(queue, next)
		}
	}
	return m
}

var namedKeys = map[string]tea.Key{
	"enter":     {Code: tea.KeyEnter},
	"esc":       {Code: tea.KeyEscape},
	"up":        {Code: tea.KeyUp},
	"down":      {Code: tea.KeyDown},
	"pgup":      {Code: tea.KeyPgUp},
	"pgdown":    {Code: tea.KeyPgDown},
	"tab":       {Code: tea.KeyTab},
	"shift+tab": {Code: tea.KeyTab, Mod: tea.ModShift},
}

func keyPress(name string) tea.KeyPressMsg {
	if k, ok := namedKeys[name]; ok {
		return tea.KeyPressMsg(k)
	}
	r := []rune(name)[0]
	return tea.KeyPressMsg(tea.Key{Code: r, Text: name})
}
