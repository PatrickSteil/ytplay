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

	// From this width on the screen is split into three equally wide framed
	// columns. A third of it must fit the cover plus frame and padding.
	wideMinWidth  = 3 * (coverCols + 4)
	wideMinHeight = 6
	// The cover needs the cover rows plus a blank line and three text lines.
	coverColHeight = coverRows + 4

	// The search column is split in two once there are results: the list above
	// and a preview of the selected result below. The preview would like a
	// cover, a blank line, two lines of text, four of description and a frame;
	// the list keeps at least previewMinTop framed lines.
	previewWant   = coverRows + 1 + 2 + 4 + 2
	previewMinTop = 8
	previewMin    = 6
)

var (
	dim      = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	errStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	bold     = lipgloss.NewStyle().Bold(true)
)

// theme is the set of colours the screen is drawn in.
type theme struct {
	accent, accentB lipgloss.Style // highlights, the focused frame, the progress bar, popups
	frame           lipgloss.Style // the frames and rules around what is not focused
}

func newTheme(accent, frame string) theme {
	a := lipgloss.NewStyle().Foreground(lipgloss.Color(accent))
	return theme{accent: a, accentB: a.Bold(true), frame: lipgloss.NewStyle().Foreground(lipgloss.Color(frame))}
}

// The default colours are of a song without a cover (or with one that has no
// colour to take), and while nothing plays.
var blueTheme = newTheme("75", "245")

// theme is the colours to draw in: those of the cover of the playing song, blue
// when there are none.
func (m Model) theme() theme {
	if t, ok := m.themes[m.currentID()]; ok {
		return t
	}
	return blueTheme
}

// autoBadge marks the progress line while autodj is on (not by a colour: the
// colours belong to the cover).
func (m Model) autoBadge() string {
	if !m.autoDJ {
		return ""
	}
	return bold.Render(" · ⇄ AUTODJ")
}

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

// help is the line of hints at the bottom right: the keys that work in the state
// the program is in, and only those (a test uses every one of them).
func (m Model) help() string {
	playing, queued := m.currentEntry() != nil, len(m.upcoming()) > 0
	var h []string
	switch m.mode {
	case modeSearch:
		h = []string{"⏎ search", "esc close"}
	case modeResults:
		h = []string{"j/k move", "⏎ play", ":add queue", "/ search"}
		if queued {
			h = append(h, "⇥ queue")
		}
		h = append(h, ": command", "? help")
	case modeQueue:
		h = []string{"j/k select", "⏎ play", "↑/↓ move", "x remove", "u undo", "⇥/esc back", ": command", "? help"}
	case modeDJ:
		h = []string{"j/k select", ":sort", ":bpm N", ":auto", "u undo", "esc close", ": command", "? help"}
	case modeHelp:
		if len(m.helpRows()) > m.helpWindow() {
			h = append(h, "j/k scroll")
		}
		h = append(h, "esc close")
	case modeConfirm:
		h = []string{"y yes", "n no", "esc cancel"}
	case modeCast:
		h = []string{"esc close"}
		if len(m.castTargets) > 0 {
			h = []string{"↑/↓ select", "⏎ cast", "esc close"}
		}
	case modeDownload:
		h = []string{"⏎ save", "⇥ complete", "esc cancel"}
	default:
		h = []string{": command", "/ search"}
		if queued {
			h = append(h, "⇥ queue")
		}
		if playing {
			h = append(h, "␣ pause", "←/→ seek")
		}
		h = append(h, "+/- volume", "? help")
	}
	return strings.Join(h, "  ")
}

func (m Model) rule() string { return m.theme().frame.Render(strings.Repeat("─", max(m.width, 0))) }

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
	text = m.besideCover(text, textW)
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
	th := m.theme()
	icon := "▶"
	if m.paused {
		icon = "⏸"
	}
	if m.loading {
		return th.accent.Render(icon) + " " + dim.Render("Loading…") + m.onSpeaker() + m.autoBadge()
	}
	total := "--:--"
	if m.total > 0 {
		total = formatClock(m.total)
	}
	prefix := fmt.Sprintf("%s %s / %s  ", icon, formatClock(m.elapsed), total)
	pct := ""
	if m.total > 0 {
		pct = fmt.Sprintf(" %d%%", int(min(max(m.elapsed/m.total, 0), 1)*100+0.5))
	}
	return th.accent.Render(strings.TrimRight(prefix, " ")) + pct + m.onSpeaker() + m.autoBadge()
}

// onSpeaker says which speaker the sound comes from while casting.
func (m Model) onSpeaker() string {
	if m.castName == "" {
		return ""
	}
	return dim.Render(" · on " + safe(m.castName))
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
	th := m.theme()
	cur := m.currentEntry()
	if cur == nil {
		return nil
	}
	upcoming := m.upcoming()
	const durW = 8
	titleW := max(w-5-2-durW-1, 1) // marker, source icon
	rowFor := func(e playlistEntry) string {
		title, _, dur := m.entryInfo(e)
		return fit(safe(title), titleW) + " " + fmt.Sprintf("%*s", durW, formatDur(dur))
	}
	playing := th.accent // the playing song is in the colour of its key on the Camelot wheel
	if st := m.stats[yt.KeyOf(cur.Filename)]; st.hasRes && st.res.Camelot != "" {
		playing = camelotStyle(st.res.Camelot)
	}
	lines := []string{th.accentB.Render("  ▶  ") + iconOfEntry(*cur) + " " + playing.Render(rowFor(*cur))}

	win := m.queueWindowSize()
	off := 0
	if m.mode == modeQueue {
		off = max(min(m.queueOff, len(upcoming)-win), 0)
	}
	end := min(off+win, len(upcoming))
	for i := off; i < end; i++ {
		if m.mode == modeQueue && i == m.queueCursor {
			lines = append(lines, th.accentB.Render(fmt.Sprintf("> %d. ", i+1))+iconOfEntry(upcoming[i])+" "+th.accentB.Render(rowFor(upcoming[i])))
		} else {
			lines = append(lines, dim.Render(fmt.Sprintf("%3d. ", i+1))+iconOfEntry(upcoming[i])+" "+rowFor(upcoming[i]))
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
	if m.mode == modeCommand { // the command line takes the place of the status
		return clip(m.cmdInput.View(), m.width)
	}
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
	return dim.Render(fit(help, m.width)) // padded: the line is as wide as the screen whatever the hints
}

// resultsVisible is how many result rows fit on screen.
func (m Model) resultsVisible() int {
	if m.wide() {
		top, _ := m.searchSplit()
		return max(top-2, 0) // search bar + results header
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
	th := m.theme()
	const durW = 8
	chanW := 0
	if w >= 50 {
		chanW = min(w/4, 24)
	}
	titleW := w - 2 - 2 - durW - 1 // marker, source icon
	if chanW > 0 {
		titleW -= chanW + 2
	}
	dur := fmt.Sprintf("%*s", durW, formatDur(r.Duration))
	row := fit(safe(r.Title), max(titleW, 1)) + " " + dur
	if chanW > 0 {
		row += "  " + fit(safe(r.Channel), chanW)
	}
	icon := sourceIcon(r.Source) + " "
	switch {
	case i == m.selected && focused:
		return th.accentB.Render("> ") + icon + th.accentB.Render(row)
	case i == m.selected:
		return dim.Render("> ") + icon + dim.Render(row)
	}
	return "  " + icon + row
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
	switch m.popupMode() {
	case modeConfirm, modeDownload, modeHelp, modeDJ, modeCast:
		v = m.withPopup(v)
	}
	return v
}

// popupMode is the state whose popup is on screen. The command line opened over
// the DJ popup does not take it away: :sort and :bpm are typed there.
func (m Model) popupMode() mode {
	if m.mode == modeCommand {
		return m.cmdFrom
	}
	return m.mode
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
func box(th theme, title string, body []string, innerW, innerH int, focused bool) []string {
	border := th.frame
	if focused {
		border = th.accent
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
	body, _ := m.leftBodyN(innerH)
	return body
}

// leftBodyN is the now-playing column, and how many comments it shows.
func (m Model) leftBodyN(innerH int) (body []string, comments int) {
	body = m.nowPlayingText()
	if innerH >= coverColHeight {
		// The cover has a fixed size: center it in the column.
		pad := strings.Repeat(" ", max((m.textWidth()-coverCols)/2, 0))
		var cover []string
		for _, l := range strings.Split(m.coverFor(), "\n") {
			cover = append(cover, pad+l)
		}
		if rows := m.waveRows(innerH); rows > 0 {
			cover = append(append(cover, ""), m.waveLines(m.textWidth(), rows)...)
		}
		body = append(cover, append([]string{""}, body...)...)
	}
	if room := innerH - len(body) - 1; room >= 2 {
		if stats, n := m.statsLinesN(m.textWidth(), room); len(stats) > 0 {
			body, comments = append(append(body, ""), stats...), n
		}
	}
	return body, comments
}

// searchSplit divides the search column's inner height into the part for the
// search bar and results and the part for the preview of the selected result
// (0 when there is nothing to preview or too little room).
func (m Model) searchSplit() (top, bottom int) {
	innerH := m.colInnerHeight()
	if len(m.results) == 0 {
		return innerH, 0
	}
	outer := min(previewWant, innerH+2-previewMinTop)
	if outer < previewMin {
		return innerH, 0
	}
	return innerH - outer, outer - 2
}

// previewBody shows the selected result: cover, title, channel, numbers and
// as much of the description as fits into h lines.
func (m Model) previewBody(w, h int) []string {
	r, ok := m.selectedResult()
	if !ok {
		return nil
	}
	var body []string
	if h >= coverRows+3 {
		pad := strings.Repeat(" ", max((w-coverCols)/2, 0))
		cover, ok := m.covers[r.ID]
		if !ok {
			cover = placeholderCover()
		}
		for _, l := range strings.Split(cover, "\n") {
			body = append(body, pad+l)
		}
		body = append(body, "")
	}
	meta := formatDur(r.Duration)
	if ch := safe(r.Channel); ch != "" {
		meta = ch + " · " + meta
	}
	body = append(body, bold.Render(clip(safe(r.Title), w)), dim.Render(clip(meta, w)))
	if l := m.statsLineFor(r.ID); l != "" && len(body) < h {
		body = append(body, clip(l, w))
	}
	if room := h - len(body) - 1; room >= 1 {
		st := m.stats[r.ID]
		switch {
		case st.hasInfo:
			if lines := wrapText(st.info.Description, w, room); len(lines) > 0 {
				body = append(body, "")
				for _, l := range lines {
					body = append(body, dim.Render(l))
				}
			}
		case !st.infoFailed && m.deps.Info != nil:
			body = append(body, "", dim.Render("Loading description…"))
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
	searchH, previewH := m.searchSplit()

	queueBody := m.queueRows(qw - 3)
	if len(queueBody) == 0 {
		queueBody = []string{dim.Render("Nothing queued")}
	}
	left := box(m.theme(), "Now playing", m.leftBody(innerH), lw-2, innerH, false)
	queue := box(m.theme(), m.queueTitle(), queueBody, qw-2, innerH, m.mode == modeQueue)
	search := box(m.theme(), "Search", m.searchBody(sw-3, searchH), sw-2, searchH, m.mode == modeSearch || m.mode == modeResults)
	if previewH > 0 {
		search = append(search, box(m.theme(), "Preview", m.previewBody(sw-3, previewH), sw-2, previewH, false)...)
	}

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
	th := m.theme()
	id := m.currentID()
	st := m.stats[id]
	bpm := m.tempoOf(id)
	switch {
	case id == "":
		return ""
	case !st.hasRes && bpm == 0:
		switch {
		case m.deps.Analyze == nil:
			return ""
		case st.analysisFailed:
			reason := strings.Join(strings.Fields(st.analysisErr), " ")
			if reason == "" {
				return dim.Render("analysis failed")
			}
			return dim.Render("analysis failed: " + clip(safe(reason), 60))
		}
		return dim.Render("analysing…")
	}
	var parts []string
	if bpm > 0 {
		parts = append(parts, fmt.Sprintf("%.0f BPM", bpm))
	}
	var key []string
	if st.hasRes && st.res.Key != "" {
		key = append(key, st.res.Key)
		if st.res.Camelot != "" {
			key = append(key, st.res.Camelot)
		}
	}
	line := th.accent.Render(strings.Join(parts, " · "))
	if len(key) > 0 { // the key in the colour of its place on the Camelot wheel
		if len(parts) > 0 {
			line += th.accent.Render(" · ")
		}
		line += camelotStyle(st.res.Camelot).Render(strings.Join(key, " · "))
	}
	return line
}

// statsLine is "1.2M views · 8.9K likes · 2013" (empty until known).
func (m Model) statsLine() string { return m.statsLineFor(m.currentID()) }

func (m Model) statsLineFor(id string) string {
	st := m.stats[id]
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
	lines, _ := m.statsLinesN(w, avail)
	return lines
}

// statsLinesN is statsLines and the number of comments it shows.
func (m Model) statsLinesN(w, avail int) (lines []string, comments int) {
	th := m.theme()
	if m.currentID() == "" || avail <= 0 {
		return nil, 0
	}
	var out []string
	for _, l := range []string{m.bpmKeyLine(), m.statsLine()} {
		if l != "" && len(out) < avail {
			out = append(out, clip(l, w))
		}
	}
	st := m.stats[m.currentID()]
	if !st.hasInfo {
		return out, 0
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

	// Comments: two lines each, as many as fit below each other, from the one
	// the user has paged to (:comments).
	n := len(st.info.Comments)
	if room := avail - len(out); n > 0 && room >= 3 {
		off := min(st.commentsOff, n-1)
		var body []string
		for i := off; i < n; i++ {
			room = avail - len(out) - 2 - len(body) // the blank line and the header come first
			if room <= 0 {
				break
			}
			c := st.info.Comments[i]
			prefix, prefixStyle := "", th.accent
			if c.Likes > 0 {
				// ASCII on purpose: glyphs such as "♥" are drawn two cells
				// wide by some terminals and would cover the count.
				prefix = "(" + compact(c.Likes) + ") "
			} else if c.At > 0 { // SoundCloud: where in the track it was left
				prefix, prefixStyle = fmt.Sprintf("@%d:%02d ", int(c.At)/60, int(c.At)%60), dim
			}
			for j, l := range wrapText(prefix+c.Text, w, min(2, room)) {
				if rest, ok := strings.CutPrefix(l, prefix); ok && j == 0 && prefix != "" {
					body = append(body, prefixStyle.Render(prefix)+dim.Render(rest))
				} else {
					body = append(body, dim.Render(l))
				}
			}
			comments++
		}
		if comments > 0 {
			out = append(out, "", clip(m.commentsHeader(st, off, comments), w))
			out = append(out, body...)
		}
	}
	return out, comments
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
	th := m.theme()
	n := len(m.upcoming())
	songs := fmt.Sprintf("%d upcoming songs", n)
	if n == 1 {
		songs = "1 upcoming song"
	}
	w := max(min(46, m.width)-2, 0) // inner width
	row := func(s string) string { return th.accent.Render("│") + fit(" "+s, w) + th.accent.Render("│") }
	title := " Clear the queue? "
	return []string{
		th.accent.Render("┌") + bold.Render(clip(title, w)) + th.accent.Render(strings.Repeat("─", max(w-lipgloss.Width(title), 0))+"┐"),
		row(""),
		row("Remove all " + songs + "?"),
		row(dim.Render("The playing song keeps going.")),
		row(""),
		row(th.accentB.Render("y yes") + "        " + dim.Render("n/esc no")),
		th.accent.Render("└" + strings.Repeat("─", w) + "┘"),
	}
}

// downloadBox is the "save to file" popup.
func (m Model) downloadBox() []string {
	th := m.theme()
	w := max(min(64, m.width)-2, 0)
	row := func(s string) string { return th.accent.Render("│") + fit(" "+s, w) + th.accent.Render("│") }
	title := " Save to file "
	lines := []string{
		th.accent.Render("┌") + bold.Render(clip(title, w)) + th.accent.Render(strings.Repeat("─", max(w-lipgloss.Width(title), 0))+"┐"),
		row(dim.Render(clip(safe(m.dlTrack.Title), w-2))),
		row(m.dlInput.View()),
	}
	if m.dlWarn != "" {
		lines = append(lines, row(th.accentB.Render(m.dlWarn)))
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
		th.accent.Render("└"+strings.Repeat("─", w)+"┘"))
	return lines
}

// djTempo is the tempo column of the DJ popup for a song: its BPM (a * marks
// one set by hand), … while it is being analysed, – if there is none.
func (m Model) djTempo(id string) string {
	st := m.stats[id]
	switch bpm, set := m.tempoOf(id), m.bpmSet[id] > 0; {
	case bpm > 0 && set:
		return fmt.Sprintf("%9s", fmt.Sprintf("%.0f* BPM", bpm))
	case bpm > 0:
		return fmt.Sprintf("%9s", fmt.Sprintf("%.0f BPM", bpm))
	case !st.hasRes && !st.analysisFailed && m.deps.Analyze != nil:
		return dim.Render(fmt.Sprintf("%9s", "…"))
	}
	return dim.Render(fmt.Sprintf("%9s", "–"))
}

// djBox is the DJ popup: the playing song and the queue with their tempo.
func (m Model) djBox() []string {
	th := m.theme()
	w := max(min(76, m.width)-2, 0)
	row := func(s string) string { return th.accent.Render("│") + fit(" "+s, w) + th.accent.Render("│") }
	title := " DJ "
	lines := []string{th.accent.Render("┌") + bold.Render(clip(title, w)) + th.accent.Render(strings.Repeat("─", max(w-lipgloss.Width(title), 0))+"┐")}

	n := m.djRowCount()
	if n == 0 {
		lines = append(lines, row(dim.Render("Nothing playing")))
	}
	win := m.djWindow()
	off := max(min(m.djOff, n-win), 0)
	if m.djCursor < off {
		off = m.djCursor
	}
	if m.djCursor >= off+win {
		off = m.djCursor - win + 1
	}
	titleW := max(w-1-2-2-1-9-4, 1) // marker, icon, title, tempo, key
	for r := off; r < min(off+win, n); r++ {
		e := m.djEntry(r)
		name, _, _ := m.entryInfo(e)
		id := yt.KeyOf(e.Filename)
		text := fit(safe(name), titleW) + " " + m.djTempo(id)
		key := m.djKey(id)
		switch {
		case r == m.djCursor:
			lines = append(lines, row(th.accentB.Render("> ")+iconOfEntry(e)+" "+th.accentB.Render(ansi.Strip(text))+key))
		case r == 0:
			lines = append(lines, row(th.accent.Render("▶ ")+iconOfEntry(e)+" "+text+key))
		default:
			lines = append(lines, row("  "+iconOfEntry(e)+" "+text+key))
		}
	}
	autoLine := dim.Render("autodj off · :auto switches it on")
	if m.autoDJ {
		autoLine = th.accent.Render("autodj on · " + m.nextTransition())
	}
	lines = append(lines, row(autoLine), row(dim.Render("j/k select   :sort by BPM   :bpm N set BPM   :auto   esc close")))
	return append(lines, th.accent.Render("└"+strings.Repeat("─", w)+"┘"))
}

// helpRows are all rows of the help popup: the keys that need no colon, then
// every command (from the table the command line runs, so none is left out).
func (m Model) helpRows() []string {
	th := m.theme()
	key := func(k, d string) string { return th.accentB.Render(fmt.Sprintf("%-10s", k)) + d }
	rows := []string{
		"ytplay by Patrick Steil",
		dim.Render("A simple CLI wrapper to just play music: mpv plays the"),
		dim.Render("audio, yt-dlp searches and downloads."),
		"",
		bold.Render("Keys"),
		key("?", "open or close this help"),
		key("/", "search bar (Enter runs it, Esc closes it)"),
		key("j / k", "move in the results, the queue and the DJ popup"),
		key("Enter", "play the highlighted result or queue entry now"),
		key("Tab", "switch between the results and the queue"),
		key("x", "remove the highlighted queue entry"),
		key("↑ / ↓", "move the highlighted queue entry (or J / K)"),
		key("J / K", "move the highlighted queue entry down / up"),
		key("u", "undo the last queue change"),
		key("Space", "pause / resume"),
		key("← / →", "seek 5 seconds back / forward"),
		key("+ / -", "volume up / down"),
		key("0-9, =", "seek to 0%-90%, 100%"),
		key("Ctrl+C", "quit"),
		key("Esc", "back out of whatever is open"),
		key(":", "the command line: Enter runs it, Esc cancels,"),
		key("", "Up/Down earlier commands, Tab completes a name"),
		"",
		bold.Render("Commands") + dim.Render("  (listed shortcuts work as keys)"),
	}
	for _, c := range commands {
		label := c.usage
		if len(c.short) > 0 {
			label += "  " + strings.Join(c.short, "/")
		}
		rows = append(rows, th.accentB.Render(fmt.Sprintf("%-23s", label))+c.about)
	}
	return rows
}

// helpWindow is how many rows of help fit on this terminal.
func (m Model) helpWindow() int { return max(m.height-5, 3) } // borders, the closing hint, a margin

// helpBox is the help popup. On a small terminal it scrolls with j and k.
func (m Model) helpBox() []string {
	th := m.theme()
	w := max(min(100, m.width)-2, 0)
	row := func(s string) string { return th.accent.Render("│") + fit(" "+s, w) + th.accent.Render("│") }
	title := " ytplay help "
	rows, win := m.helpRows(), m.helpWindow()
	off, hint := 0, "Esc closes this window"
	if len(rows) > win {
		off = max(min(m.helpOff, len(rows)-win), 0)
		hint = "j/k scroll   Esc closes this window"
	}
	lines := []string{th.accent.Render("┌") + bold.Render(clip(title, w)) + th.accent.Render(strings.Repeat("─", max(w-lipgloss.Width(title), 0))+"┐")}
	for _, r := range rows[off:min(off+win, len(rows))] {
		lines = append(lines, row(r))
	}
	lines = append(lines, row(dim.Render(hint)))
	return append(lines, th.accent.Render("└"+strings.Repeat("─", w)+"┘"))
}

func (m Model) withPopup(view string) string {
	box := m.confirmBox()
	switch m.popupMode() {
	case modeDownload:
		box = m.downloadBox()
	case modeHelp:
		box = m.helpBox()
	case modeDJ:
		box = m.djBox()
	case modeCast:
		box = m.castBox()
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
