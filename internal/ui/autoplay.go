package ui

import tea "github.com/charmbracelet/bubbletea"

const autoplayThreshold = 3

func (m Model) toggleAutoplay() (Model, tea.Cmd) {
	next := !m.autoplay
	if m.deps.SaveAutoplay != nil {
		if err := m.deps.SaveAutoplay(next); err != nil {
			return m, m.setStatus("Could not save autoplay: "+err.Error(), true)
		}
	}
	m.autoplay, m.autoplayPos = next, -1
	if !next {
		return m, m.setStatus("Autoplay off", false)
	}
	m, refill := m.maybeAutoplay()
	if refill != nil {
		m.setStickyStatus("Autoplay on · finding similar songs…")
		return m, refill
	}
	return m, m.setStatus("Autoplay on", false)
}

func (m Model) maybeAutoplay() (Model, tea.Cmd) {
	if !m.autoplay {
		return m, nil
	}
	if m.currentEntry() == nil || len(m.upcoming()) > autoplayThreshold {
		m.autoplayPos = -1
		return m, nil
	}
	if m.autoplayPos == m.pos || m.radioBusy || m.deps.Similar == nil {
		return m, nil
	}
	// While the current song is playing, repeated playlist updates agree on
	// the position. A failed or empty lookup can be retried on the next song.
	return m.startRadio(radioDefault, true)
}
