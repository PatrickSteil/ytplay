package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/PatrickSteil/ytplay/internal/cover"
	"github.com/PatrickSteil/ytplay/internal/yt"
)

const (
	coverCols = cover.Cols
	coverRows = cover.Rows
	// Below this terminal width the cover is hidden.
	minWidthForCover = 60
	maxQueueRows     = 5 // current entry + queueWindow upcoming ones
	queueWindow      = maxQueueRows - 1
	helpText         = "/ search  ⏎ play  q queue  r similar  ⇥ edit queue  s skip  0-9 = seek  ␣ pause  Q quit"
	queueHelpText    = "j/k select  ↑/↓ move  x remove  ⇥/esc back"

	// From this width on the screen is split into three framed columns.
	wideMinWidth  = 100
	wideMinHeight = 6
	leftColWidth  = coverCols + 4 // frame + one space of padding on each side
	// The cover needs the cover rows plus a blank line and three text lines.
	coverColHeight = coverRows + 4
)

var (
	accent   = lipgloss.NewStyle().Foreground(lipgloss.Color("75"))
	accentB  = accent.Bold(true)
	dim      = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	errStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	bold     = lipgloss.NewStyle().Bold(true)
)

// fit truncates s with "…" to w display columns and pads it with spaces to w.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = ansi.Truncate(s, w, "…")
	if pad := w - lipgloss.Width(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}

// clip truncates s to w columns without padding.
func clip(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return ansi.Truncate(s, w, "…")
}

func formatDur(sec float64) string {
	if sec <= 0 {
		return "--:--"
	}
	s := int(sec + 0.5)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s%3600/60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// placeholderCover is a dim box of ░ that keeps the layout stable.
func placeholderCover() string {
	row := dim.Render(strings.Repeat("░", coverCols))
	rows := make([]string, coverRows)
	for i := range rows {
		rows[i] = row
	}
	return strings.Join(rows, "\n")
}

func (m Model) showCover() bool { return m.width >= minWidthForCover }

func (m Model) showResults() bool {
	return m.mode == modeResults ||
		(m.mode == modeSearch && len(m.results) > 0) ||
		(m.mode == modeQueue && m.queueReturn == modeResults)
}

func (m Model) help() string {
	if m.mode == modeQueue {
		return queueHelpText
	}
	return helpText
}

func (m Model) rule() string { return dim.Render(strings.Repeat("─", max(m.width, 0))) }

// renderTop is the now-playing panel.
func (m Model) renderTop() []string {
	text := m.nowPlayingText()
	if !m.showCover() {
		for len(text) < 3 {
			text = append(text, "")
		}
		return text
	}
	textW := m.width - coverCols - 3
	for i, l := range text {
		text[i] = clip(l, textW)
	}
	joined := lipgloss.JoinHorizontal(lipgloss.Top, m.coverFor(), "   ", strings.Join(text, "\n"))
	return strings.Split(joined, "\n")
}

// coverFor is the current track's cover, or the placeholder while it loads
// (or if it could not be fetched).
func (m Model) coverFor() string {
	if c, ok := m.covers[m.currentID()]; ok {
		return c
	}
	return placeholderCover()
}

// nowPlayingText returns the lines shown next to the cover.
func (m Model) nowPlayingText() []string {
	e := m.currentEntry()
	if e == nil {
		if m.wide() {
			return []string{bold.Render("Nothing playing"), dim.Render("press / to search")}
		}
		return []string{dim.Render("Nothing playing — press / to search")}
	}
	title, channel, _ := m.entryInfo(*e)
	return []string{bold.Render(title), dim.Render(channel), m.progressLine()}
}

// textWidth is the width available next to the cover.
func (m Model) textWidth() int {
	if m.wide() {
		return leftColWidth - 3
	}
	if m.showCover() {
		return m.width - coverCols - 3
	}
	return m.width
}

func formatClock(sec float64) string {
	s := int(sec)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s%3600/60, s%60)
	}
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}

// progressLine is "▶ 01:23 / 03:52  ██████░░░░░░░░░░ 36%" (or "Loading…").
func (m Model) progressLine() string {
	icon := "▶"
	if m.paused {
		icon = "⏸"
	}
	if m.loading {
		return accent.Render(icon) + " " + dim.Render("Loading…")
	}
	total := "--:--"
	if m.total > 0 {
		total = formatClock(m.total)
	}
	prefix := fmt.Sprintf("%s %s / %s  ", icon, formatClock(m.elapsed), total)
	barW := min(m.textWidth()-lipgloss.Width(prefix)-5, 30)
	if barW < 6 {
		return accent.Render(prefix)
	}
	frac := 0.0
	if m.total > 0 {
		frac = min(max(m.elapsed/m.total, 0), 1)
	}
	filled := int(frac * float64(barW))
	bar := accent.Render(strings.Repeat("█", filled)) + dim.Render(strings.Repeat("░", barW-filled))
	pct := ""
	if m.total > 0 {
		pct = fmt.Sprintf(" %d%%", int(frac*100+0.5))
	}
	return accent.Render(prefix) + bar + pct
}

// queueWindowSize is how many upcoming entries the queue shows at once.
func (m Model) queueWindowSize() int {
	if !m.wide() {
		return queueWindow
	}
	avail := m.colInnerHeight() - 1 // minus the current entry's row
	if len(m.upcoming()) <= avail {
		return max(avail, 1)
	}
	return max(avail-1, 1) // keep a line for "… and N more"
}

// queueRows are the queue lines for a panel w columns wide: the current entry
// (marked), then a window of the entries after it. While the panel is focused
// (modeQueue) the window follows the cursor; otherwise it starts at the first
// upcoming entry. Already-played entries are not shown.
func (m Model) queueRows(w int) []string {
	cur := m.currentEntry()
	if cur == nil {
		return nil
	}
	upcoming := m.upcoming()
	const durW = 8
	titleW := max(w-5-durW-1, 1)
	rowFor := func(e playlistEntry) string {
		title, _, dur := m.entryInfo(e)
		return fit(title, titleW) + " " + fmt.Sprintf("%*s", durW, formatDur(dur))
	}
	lines := []string{accentB.Render("  ▶  ") + accent.Render(rowFor(*cur))}

	win := m.queueWindowSize()
	off := 0
	if m.mode == modeQueue {
		off = max(min(m.queueOff, len(upcoming)-win), 0)
	}
	end := min(off+win, len(upcoming))
	for i := off; i < end; i++ {
		if m.mode == modeQueue && i == m.queueCursor {
			lines = append(lines, accentB.Render(fmt.Sprintf("> %d. ", i+1)+rowFor(upcoming[i])))
		} else {
			lines = append(lines, dim.Render(fmt.Sprintf("%3d. ", i+1))+rowFor(upcoming[i]))
		}
	}
	if more := len(upcoming) - end; more > 0 {
		lines = append(lines, dim.Render(fmt.Sprintf("  … and %d more", more)))
	}
	return lines
}

func (m Model) queueTitle() string { return fmt.Sprintf("Up next (%d)", len(m.upcoming())) }

// renderQueue is the stacked layout's "Up next" panel.
func (m Model) renderQueue() []string {
	return append([]string{bold.Render(m.queueTitle())}, m.queueRows(m.width)...)
}

// renderBottom is the rule, the search bar (in modeSearch) and the status line.
func (m Model) renderBottom() []string {
	lines := []string{m.rule()}
	if m.mode == modeSearch {
		lines = append(lines, clip(m.input.View(), m.width))
	}
	return append(lines, m.statusLine())
}

func (m Model) statusLine() string {
	style := dim
	if m.statusIsErr {
		style = errStyle
	}
	help := m.help()
	helpW := lipgloss.Width(help)
	if m.width >= helpW+14 {
		left := m.width - helpW - 2
		if m.status == "" {
			return fit("", left) + "  " + dim.Render(help)
		}
		return style.Render(fit(m.status, left)) + "  " + dim.Render(help)
	}
	if m.status != "" {
		return style.Render(clip(m.status, m.width))
	}
	return dim.Render(clip(help, m.width))
}

// resultsVisible is how many result rows fit on screen.
func (m Model) resultsVisible() int {
	if m.wide() {
		return max(m.colInnerHeight()-2, 0) // search bar + results header
	}
	fixed := len(m.renderTop()) + 1 + len(m.renderQueue()) + 2 + len(m.renderBottom())
	return max(m.height-fixed, 0)
}

func (m Model) resultsHeader(w int) string {
	return bold.Render(clip(fmt.Sprintf("Results for %q", m.query), w))
}

func (m Model) renderResults(vis int) []string {
	lines := []string{m.resultsHeader(m.width)}
	end := min(m.offset+vis, len(m.results))
	for i := m.offset; i < end; i++ {
		lines = append(lines, m.resultRow(m.width, i, m.results[i], true))
	}
	return lines
}

// resultRow renders one result for a panel w columns wide. The selection
// marker is dimmed when the list is not focused.
func (m Model) resultRow(w, i int, r yt.Result, focused bool) string {
	const durW = 8
	chanW := 0
	if w >= 50 {
		chanW = min(w/4, 24)
	}
	titleW := w - 2 - durW - 1
	if chanW > 0 {
		titleW -= chanW + 2
	}
	dur := fmt.Sprintf("%*s", durW, formatDur(r.Duration))
	row := fit(r.Title, max(titleW, 1)) + " " + dur
	if chanW > 0 {
		row += "  " + fit(r.Channel, chanW)
	}
	switch {
	case i == m.selected && focused:
		return accentB.Render("> " + row)
	case i == m.selected:
		return dim.Render("> " + row)
	}
	return "  " + row
}

// View draws three framed columns on wide terminals and a stacked layout
// otherwise.
func (m Model) View() string {
	if m.wide() {
		return m.viewWide()
	}
	return m.viewStacked()
}

func (m Model) viewStacked() string {
	top := m.renderTop()
	queue := m.renderQueue()
	bottom := m.renderBottom()

	lines := append([]string{}, top...)
	lines = append(lines, m.rule())
	lines = append(lines, queue...)
	if m.showResults() {
		vis := max(m.height-len(lines)-2-len(bottom), 0)
		m.scrollIntoView()
		lines = append(lines, m.rule())
		if vis > 0 {
			lines = append(lines, m.renderResults(vis)...)
		}
	}
	// Pad so the bottom block sits on the last lines of the screen.
	for len(lines)+len(bottom) < m.height {
		lines = append(lines, "")
	}
	lines = append(lines, bottom...)

	if len(lines) > m.height && m.height > 0 {
		// Too small: keep the status line (last) and drop from the middle.
		lines = append(lines[:m.height-1], lines[len(lines)-1])
	}
	for i, l := range lines {
		lines[i] = clip(l, m.width)
	}
	return strings.Join(lines, "\n")
}

// ---- wide layout: three framed columns ----

func (m Model) wide() bool { return m.width >= wideMinWidth && m.height >= wideMinHeight }

// colInnerHeight is the usable height inside the column frames (the last
// screen line belongs to the status line).
func (m Model) colInnerHeight() int { return max(m.height-1-2, 0) }

// colWidths are the outer widths of the three columns.
func (m Model) colWidths() (left, queue, search int) {
	rest := m.width - leftColWidth
	queue = rest * 45 / 100
	return leftColWidth, queue, rest - queue
}

// searchInnerWidth is the usable width of the search column.
func (m Model) searchInnerWidth() int {
	if !m.wide() {
		return m.width
	}
	_, _, sw := m.colWidths()
	return sw - 2
}

// inputWidth is the width given to the search text input.
func (m Model) inputWidth() int {
	if m.wide() {
		return max(m.searchInnerWidth()-4, 10)
	}
	return max(m.width-6, 10)
}

// box frames body with a titled border exactly innerW x innerH inside.
func box(title string, body []string, innerW, innerH int, focused bool) []string {
	border := dim
	if focused {
		border = accent
	}
	t := " " + clip(title, max(innerW-2, 0)) + " "
	top := border.Render("┌") + bold.Render(t) +
		border.Render(strings.Repeat("─", max(innerW-lipgloss.Width(t), 0))+"┐")
	out := []string{top}
	for i := 0; i < innerH; i++ {
		line := ""
		if i < len(body) {
			line = body[i]
		}
		// One space of padding inside the frame; callers lay content out for
		// innerW-1 columns.
		out = append(out, border.Render("│")+fit(" "+line, innerW)+border.Render("│"))
	}
	return append(out, border.Render("└"+strings.Repeat("─", innerW)+"┘"))
}

// leftBody is the now-playing column: cover on top, track info below.
func (m Model) leftBody(innerH int) []string {
	text := m.nowPlayingText()
	if innerH < coverColHeight {
		return text
	}
	var body []string
	for _, l := range strings.Split(m.coverFor(), "\n") {
		body = append(body, l)
	}
	body = append(body, "")
	return append(body, text...)
}

func (m Model) searchBody(w, innerH int) []string {
	var bar string
	if m.mode == modeSearch {
		bar = m.input.View()
	} else {
		bar = dim.Render("/ search YouTube")
	}
	body := []string{bar}
	if len(m.results) > 0 {
		body = append(body, m.resultsHeader(w))
		vis := max(innerH-2, 0)
		end := min(m.offset+vis, len(m.results))
		for i := m.offset; i < end; i++ {
			body = append(body, m.resultRow(w, i, m.results[i], m.mode == modeResults))
		}
	}
	return body
}

func (m Model) viewWide() string {
	innerH := m.colInnerHeight()
	lw, qw, sw := m.colWidths()
	m.scrollIntoView()

	queueBody := m.queueRows(qw - 3)
	if len(queueBody) == 0 {
		queueBody = []string{dim.Render("Nothing queued")}
	}
	left := box("Now playing", m.leftBody(innerH), lw-2, innerH, false)
	queue := box(m.queueTitle(), queueBody, qw-2, innerH, m.mode == modeQueue)
	search := box("Search", m.searchBody(sw-3, innerH), sw-2, innerH, m.mode == modeSearch || m.mode == modeResults)

	lines := make([]string, 0, m.height)
	for i := range left {
		lines = append(lines, left[i]+queue[i]+search[i])
	}
	lines = append(lines, m.statusLine())
	for i, l := range lines {
		lines[i] = clip(l, m.width)
	}
	return strings.Join(lines, "\n")
}
