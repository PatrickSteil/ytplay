package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type castTarget struct {
	name  string
	local bool
}

func (m Model) openCastPicker() (Model, tea.Cmd) {
	m.castFrom, m.mode = m.mode, modeCast
	m.castBusy = true
	m.castTargets = nil
	m.castCursor = 0
	m.castNotice = "Looking for speakers…"
	ctx, devices := m.queueCtx, m.deps.Devices
	return m, func() tea.Msg {
		names, err := devices(ctx)
		return castListMsg{names: names, err: err}
	}
}

func (m Model) finishCastLookup(msg castListMsg) Model {
	m.castBusy = false
	if m.mode != modeCast {
		return m
	}
	m.castNotice = ""
	switch {
	case msg.err != nil:
		m.castNotice = "Could not look for speakers: " + msg.err.Error()
	case len(msg.names) == 0:
		m.castNotice = "No speakers found on the network"
	default:
		for _, name := range msg.names {
			m.castTargets = append(m.castTargets, castTarget{name: name})
		}
	}
	if m.castName != "" {
		m.castTargets = append(m.castTargets, castTarget{name: "Play here", local: true})
	}
	return m
}

func (m Model) closeCastPicker() Model {
	m.mode = m.castFrom
	m.castTargets = nil
	m.clampQueue()
	return m
}

func (m Model) handleCastKey(key string) (Model, tea.Cmd) {
	switch key {
	case keyEsc:
		return m.closeCastPicker(), nil
	case keyUp, keyShiftUp:
		m.castCursor = max(m.castCursor-1, 0)
	case keyDown, keyShiftDown:
		m.castCursor = min(m.castCursor+1, max(len(m.castTargets)-1, 0))
	case keyEnter:
		if len(m.castTargets) > 0 {
			target := m.castTargets[m.castCursor]
			next := m.closeCastPicker()
			if !target.local && target.name == m.castName {
				return next, nil
			}
			return next.connectCast(target.name, target.local)
		}
	}
	return m, nil
}

func (m Model) castBox() []string {
	th := m.theme()
	w := max(min(64, m.width)-2, 0)
	row := func(s string) string { return th.accent.Render("│") + fit(" "+s, w) + th.accent.Render("│") }
	title := " Cast to "
	lines := []string{th.accent.Render("┌") + bold.Render(clip(title, w)) + th.accent.Render(strings.Repeat("─", max(w-lipgloss.Width(title), 0))+"┐")}
	win := max(m.height-3, 1)
	if m.castNotice != "" {
		lines = append(lines, row(dim.Render(safe(m.castNotice))))
		win = max(win-1, 1)
	}
	first := max(m.castCursor-win+1, 0)
	for i := first; i < min(first+win, len(m.castTargets)); i++ {
		target := m.castTargets[i]
		label := safe(target.name)
		if !target.local && target.name == m.castName {
			label += " (current)"
		}
		if i == m.castCursor {
			lines = append(lines, row(th.accentB.Render("> "+label)))
		} else {
			lines = append(lines, row("  "+label))
		}
	}
	hint := "esc close"
	if len(m.castTargets) > 0 {
		hint = "↑/↓ select   enter cast   esc close"
	}
	return append(lines, row(dim.Render(hint)), th.accent.Render("└"+strings.Repeat("─", w)+"┘"))
}
