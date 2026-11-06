package ui

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

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
	helpText         = "/ search  ⏎ play  q queue  r radio  ⇥ edit  X clear  s skip  0-9 = seek  ␣ pause  Q quit"
	queueHelpText    = "j/k select  ↑/↓ move  x remove  X clear  ⇥/esc back"

	// From this width on the screen is split into three equally wide framed
	// columns. A third of it must fit the cover plus frame and padding.
	wideMinWidth  = 3 * (coverCols + 4)
	wideMinHeight = 6
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
	if m.currentID() != "" { // too little room here for description and comments
		for _, l := range []string{m.bpmKeyLine(), m.statsLine()} {
			if l != "" {
				text = append(text, l)
			}
		}
	}
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
	return []string{bold.Render(safe(title)), dim.Render(safe(channel)), m.progressLine()}
}

// textWidth is the width available next to the cover.
func (m Model) textWidth() int {
	if m.wide() {
		lw, _, _ := m.colWidths()
		return lw - 3 // frame and one space of padding
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
		return fit(safe(title), titleW) + " " + fmt.Sprintf("%*s", durW, formatDur(dur))
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
	status := safe(m.status) // may quote a song title
	helpW := lipgloss.Width(help)
	if m.width >= helpW+14 {
		left := m.width - helpW - 2
		if status == "" {
			return fit("", left) + "  " + dim.Render(help)
		}
		return style.Render(fit(status, left)) + "  " + dim.Render(help)
	}
	if status != "" {
		return style.Render(clip(status, m.width))
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
	return bold.Render(clip(fmt.Sprintf("Results for %q", safe(m.query)), w))
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
	row := fit(safe(r.Title), max(titleW, 1)) + " " + dur
	if chanW > 0 {
		row += "  " + fit(safe(r.Channel), chanW)
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
	var v string
	if m.wide() {
		v = m.viewWide()
	} else {
		v = m.viewStacked()
	}
	if m.mode == modeConfirm || m.mode == modeDownload {
		v = m.withPopup(v)
	}
	return v
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

// colWidths are the outer widths of the three columns: equal thirds, the last
// one taking the remainder.
func (m Model) colWidths() (left, queue, search int) {
	third := m.width / 3
	return third, third, m.width - 2*third
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
	body := m.nowPlayingText()
	if innerH >= coverColHeight {
		// The cover has a fixed size: center it in the column.
		pad := strings.Repeat(" ", max((m.textWidth()-coverCols)/2, 0))
		var cover []string
		for _, l := range strings.Split(m.coverFor(), "\n") {
			cover = append(cover, pad+l)
		}
		body = append(cover, append([]string{""}, body...)...)
	}
	if room := innerH - len(body) - 1; room >= 2 {
		if stats := m.statsLines(m.textWidth(), room); len(stats) > 0 {
			body = append(append(body, ""), stats...)
		}
	}
	return body
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

// ---- extra information about the current song ----

// compact abbreviates a count: 8700 -> "8.7K", 1234567 -> "1.2M".
func compact(n int) string {
	if n < 1000 {
		return strconv.Itoa(n)
	}
	v, suffix := float64(n), ""
	for _, s := range []string{"K", "M", "B"} {
		v /= 1000
		suffix = s
		if v < 999.5 {
			break
		}
	}
	if v < 10 {
		return strings.TrimSuffix(fmt.Sprintf("%.1f", v), ".0") + suffix
	}
	return fmt.Sprintf("%.0f", v) + suffix
}

// wrapText word-wraps text to w display columns, dropping blank lines and
// control characters, and cuts it to maxLines (ending in "…" if it was cut).
func wrapText(text string, w, maxLines int) []string {
	if w <= 0 || maxLines <= 0 {
		return nil
	}
	text = dropUnsafe(ansi.Strip(text))
	var lines []string
	for _, raw := range strings.Split(text, "\n") {
		words := strings.FieldsFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) })
		cur := ""
		flush := func() {
			if cur != "" {
				lines = append(lines, cur)
				cur = ""
			}
		}
		for _, word := range words {
			for lipgloss.Width(word) > w { // a word longer than a line: break it
				flush()
				chunk := ansi.Truncate(word, w, "")
				lines = append(lines, chunk)
				word = word[len(chunk):]
			}
			switch {
			case word == "":
			case cur == "":
				cur = word
			case lipgloss.Width(cur)+1+lipgloss.Width(word) <= w:
				cur += " " + word
			default:
				flush()
				cur = word
			}
		}
		flush()
	}
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		lines[maxLines-1] = ansi.Truncate(lines[maxLines-1]+"…", w, "…")
	}
	return lines
}

// bpmKeyLine is "116 BPM · F# minor · 11A", a placeholder while the audio is
// being analysed, or "" if there is nothing (to show).
func (m Model) bpmKeyLine() string {
	id := m.currentID()
	st := m.stats[id]
	switch {
	case id == "" || m.deps.Analyze == nil || st.analysisFailed:
		return ""
	case !st.hasRes:
		return dim.Render("analysing…")
	}
	var parts []string
	if st.res.BPM > 0 {
		parts = append(parts, fmt.Sprintf("%.0f BPM", st.res.BPM))
	}
	if st.res.Key != "" {
		parts = append(parts, st.res.Key)
		if st.res.Camelot != "" {
			parts = append(parts, st.res.Camelot)
		}
	}
	return accent.Render(strings.Join(parts, " · "))
}

// statsLine is "1.2M views · 8.9K likes · 2013" (empty until known).
func (m Model) statsLine() string {
	st := m.stats[m.currentID()]
	if !st.hasInfo {
		return ""
	}
	var parts []string
	if st.info.Views > 0 {
		parts = append(parts, compact(st.info.Views)+" views")
	}
	if st.info.Likes > 0 {
		parts = append(parts, compact(st.info.Likes)+" likes")
	}
	if st.info.Year > 0 {
		parts = append(parts, strconv.Itoa(st.info.Year))
	}
	return dim.Render(strings.Join(parts, " · "))
}

// statsLines fills up to avail lines (w columns wide) with the tempo/key, the
// numbers, the description and the top comments of the current song, as far
// as they fit.
func (m Model) statsLines(w, avail int) []string {
	if m.currentID() == "" || avail <= 0 {
		return nil
	}
	var out []string
	for _, l := range []string{m.bpmKeyLine(), m.statsLine()} {
		if l != "" && len(out) < avail {
			out = append(out, clip(l, w))
		}
	}
	st := m.stats[m.currentID()]
	if !st.hasInfo {
		return out
	}

	// Description: about half of what is left, at most six lines.
	if n := min(6, (avail-len(out)-1)/2); n >= 1 {
		if lines := wrapText(st.info.Description, w, n); len(lines) > 0 {
			out = append(out, "")
			for _, l := range lines {
				out = append(out, dim.Render(l))
			}
		}
	}

	// Comments: a header, then as many (up to three, two lines each) as fit.
	if room := avail - len(out); len(st.info.Comments) > 0 && room >= 3 {
		out = append(out, "", bold.Render("Top comments"))
		for i, c := range st.info.Comments {
			room = avail - len(out)
			if i == 3 || room <= 0 {
				break
			}
			prefix := ""
			if c.Likes > 0 {
				// ASCII on purpose: glyphs such as "♥" are drawn two cells
				// wide by some terminals and would cover the count.
				prefix = "(" + compact(c.Likes) + ") "
			}
			lines := wrapText(prefix+c.Text, w, min(2, room))
			for j, l := range lines {
				if rest, ok := strings.CutPrefix(l, prefix); ok && j == 0 && prefix != "" {
					out = append(out, accent.Render(prefix)+dim.Render(rest))
				} else {
					out = append(out, dim.Render(l))
				}
			}
		}
	}
	return out
}

// ---- popup ----

// overlay draws box on top of lines, its top-left corner at column left and
// row top. Styles in the text around the box are preserved. Rows outside the
// screen are dropped; lines are never added.
func overlay(lines, box []string, width, left, top int) []string {
	out := append([]string(nil), lines...)
	for i, b := range box {
		row := top + i
		if row < 0 || row >= len(out) {
			continue
		}
		bw := lipgloss.Width(b)
		head := ansi.Truncate(out[row], left, "")
		if pad := left - lipgloss.Width(head); pad > 0 {
			head += strings.Repeat(" ", pad)
		}
		tail := ansi.TruncateLeft(out[row], left+bw, "")
		out[row] = head + b + tail
	}
	return out
}

// confirmBox is the "clear the queue?" popup.
func (m Model) confirmBox() []string {
	n := len(m.upcoming())
	songs := fmt.Sprintf("%d upcoming songs", n)
	if n == 1 {
		songs = "1 upcoming song"
	}
	w := max(min(46, m.width)-2, 0) // inner width
	row := func(s string) string { return accent.Render("│") + fit(" "+s, w) + accent.Render("│") }
	title := " Clear the queue? "
	return []string{
		accent.Render("┌") + bold.Render(clip(title, w)) + accent.Render(strings.Repeat("─", max(w-lipgloss.Width(title), 0))+"┐"),
		row(""),
		row("Remove all " + songs + "?"),
		row(dim.Render("The playing song keeps going.")),
		row(""),
		row(accentB.Render("y yes") + "        " + dim.Render("n/esc no")),
		accent.Render("└" + strings.Repeat("─", w) + "┘"),
	}
}

// downloadBox is the "save to file" popup.
func (m Model) downloadBox() []string {
	w := max(min(64, m.width)-2, 0)
	row := func(s string) string { return accent.Render("│") + fit(" "+s, w) + accent.Render("│") }
	title := " Save to file "
	lines := []string{
		accent.Render("┌") + bold.Render(clip(title, w)) + accent.Render(strings.Repeat("─", max(w-lipgloss.Width(title), 0))+"┐"),
		row(dim.Render(clip(safe(m.dlTrack.Title), w-2))),
		row(m.dlInput.View()),
	}
	if m.dlWarn != "" {
		lines = append(lines, row(accentB.Render(m.dlWarn)))
	}
	for i, o := range m.dlOptions {
		if i == 4 {
			lines = append(lines, row(dim.Render(fmt.Sprintf("… %d more", len(m.dlOptions)-4))))
			break
		}
		lines = append(lines, row(dim.Render(clip(o, w-2))))
	}
	lines = append(lines,
		row(dim.Render("tab complete   enter save   esc cancel")),
		accent.Render("└"+strings.Repeat("─", w)+"┘"))
	return lines
}

func (m Model) withPopup(view string) string {
	box := m.confirmBox()
	if m.mode == modeDownload {
		box = m.downloadBox()
	}
	left := max((m.width-lipgloss.Width(box[0]))/2, 0)
	top := max((m.height-len(box))/2, 0)
	return strings.Join(overlay(strings.Split(view, "\n"), box, m.width, left, top), "\n")
}

// ---- text from YouTube ----

// Terminals disagree with each other, and with width tables, about how many
// cells emoji take (especially joined ones such as family or flag sequences).
// A single wrong guess shifts the rest of the line and breaks the frames. So
// emoji and invisible joiners are not drawn in titles, channels, descriptions
// and comments; they are removed. The UI's own symbols are not affected,
// because only text from outside goes through here.

// unsafeRanges are the code point ranges removed from external text.
var unsafeRanges = [][2]rune{
	{0x200B, 0x200F},   // zero width space / joiner marks, directional marks
	{0x202A, 0x202E},   // bidi embeddings and overrides
	{0x2060, 0x2064},   // word joiner and invisible operators
	{0x2066, 0x2069},   // bidi isolates
	{0xFEFF, 0xFEFF},   // byte order mark
	{0xFE00, 0xFE0F},   // variation selectors
	{0x20E3, 0x20E3},   // combining keycap
	{0xE0000, 0xE007F}, // tag characters (used in flag sequences)
	{0x2194, 0x2199},   // emoji arrows
	{0x21A9, 0x21AA},
	{0x231A, 0x231B}, // watch, hourglass
	{0x2328, 0x2328},
	{0x23CF, 0x23CF},
	{0x23E9, 0x23F3},
	{0x23F8, 0x23FA},
	{0x24C2, 0x24C2},
	{0x25AA, 0x25AB},
	{0x25B6, 0x25B6},
	{0x25C0, 0x25C0},
	{0x25FB, 0x25FE},
	{0x2600, 0x27BF}, // miscellaneous symbols and dingbats (hearts, stars, sparkles ...)
	{0x2934, 0x2935},
	{0x2B05, 0x2B07},
	{0x2B1B, 0x2B1C},
	{0x2B50, 0x2B50},
	{0x2B55, 0x2B55},
	{0x3030, 0x3030},
	{0x303D, 0x303D},
	{0x3297, 0x3299},
	{0x1F000, 0x1FAFF}, // mahjong, cards, flags, pictographs, emoticons, skin tones ...
}

func isUnsafe(r rune) bool {
	for _, rg := range unsafeRanges {
		if r >= rg[0] && r <= rg[1] {
			return true
		}
	}
	return false
}

// dropUnsafe removes emoji and invisible joiners but keeps all spacing.
func dropUnsafe(s string) string {
	if !strings.ContainsFunc(s, isUnsafe) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if isUnsafe(r) {
			return -1
		}
		return r
	}, s)
}

// safe makes a single line of external text safe to draw: emoji removed and
// the gaps they leave closed.
func safe(s string) string {
	return strings.Join(strings.Fields(dropUnsafe(s)), " ")
}
