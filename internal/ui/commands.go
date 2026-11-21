package ui

import (
	"context"
	"fmt"
	"math/rand"
	"slices"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/PatrickSteil/ytplay/internal/autodj"
	"github.com/PatrickSteil/ytplay/internal/yt"
)

// A vim-style command line: ":" opens it, a command is typed as a name (or its
// short form) and arguments, Enter runs it, Esc cancels. Short forms also
// work as direct keys in the modes that accept commands.

// command is one colon command.
type command struct {
	name  string
	short []string // spellings that count as the name; they win over prefixes
	// run does it. The model is in the state the command line was opened from.
	run func(m Model, arg string) (Model, tea.Cmd)
	// inPopup says the command may be run while the DJ popup is open; the others
	// ask for the popup to be closed first.
	inPopup bool
	// usage and about are what the help popup says.
	usage, about string
}

// commands are in the order the help popup lists them.
var commands = []command{
	{name: "find", short: []string{"f"}, run: cmdFind, usage: ":find NAME", about: "search for NAME, or load a YouTube link (no name: open the bar)"},
	{name: "add", run: cmdAdd, usage: ":add", about: "add the highlighted result to the queue"},
	{name: "load", short: []string{"l"}, run: cmdLoad, usage: ":load LINK", about: "add a YouTube or SoundCloud playlist, set or song link to the queue"},
	{name: "yank", short: []string{"y"}, run: cmdYank, inPopup: true, usage: ":yank", about: "copy the link of the highlighted (or playing) song"},
	{name: "cast", short: []string{"C"}, run: cmdCast, inPopup: true, usage: ":cast [NAME|off]", about: "choose a speaker; NAME connects directly, off plays here again"},
	{name: "play", run: cmdPlay, usage: ":play", about: "play the highlighted song now (Enter does too)"},
	{name: "skip", short: []string{"s", "n"}, run: cmdSkip, usage: ":skip", about: "skip to the next song"},
	{name: "prev", short: []string{"p"}, run: cmdPrev, usage: ":prev", about: "the song before (or the start of this one)"},
	{name: "seek", run: cmdSeek, usage: ":seek TIME", about: "to 1:30, 90 (seconds), 50%, or by +10 / -30 (also \u2190 \u2192)"},
	{name: "vol", short: []string{"v"}, run: cmdVol, inPopup: true, usage: ":vol N", about: "volume 0-100, or +N / -N (also the keys + -)"},
	{name: "mute", short: []string{"m"}, run: cmdMute, inPopup: true, usage: ":mute", about: "mute or unmute"},
	{name: "delete", short: []string{"x"}, run: cmdDelete, usage: ":delete", about: "remove the highlighted queue entry (or x)"},
	{name: "undo", short: []string{keyUndo}, run: cmdUndo, inPopup: true, usage: ":undo", about: "undo the last queue change (also the key u)"},
	{name: "shuffle", run: cmdShuffle, inPopup: true, usage: ":shuffle", about: "shuffle the queue"},
	{name: "download", short: []string{"d"}, run: cmdDownload, usage: ":download", about: "save the highlighted song to a file"},
	{name: "comments", short: []string{"c"}, run: cmdComments, usage: ":comments", about: "the next comments of this song (loads more when used up)"},
	{name: "radio", short: []string{"r"}, run: cmdRadio, usage: ":radio N", about: "add up to N (default 10) songs similar to the queue"},
	{name: "clear", short: []string{"X"}, run: cmdClear, usage: ":clear", about: "clear the queue (asks first)"},
	{name: "auto", short: []string{"a"}, run: cmdAuto, inPopup: true, usage: ":auto", about: "autodj on or off (the next song mixes in early)"},
	{name: "dj", run: cmdDJ, inPopup: true, usage: ":dj", about: "the DJ popup: the queue with its BPM"},
	{name: "sort", short: []string{"b"}, run: cmdSort, inPopup: true, usage: ":sort [build|down]", about: "DJ popup: arrange the queue to flow from the playing song by tempo, key and energy; build or down shapes the energy"},
	{name: "bpm", short: []string{"e"}, run: cmdBPM, inPopup: true, usage: ":bpm N", about: "DJ popup: set a song's BPM (alone: the detected one)"},
	{name: "help", short: []string{"h", keyHelp}, run: cmdHelp, inPopup: true, usage: ":help", about: "this help"},
	{name: "quit", short: []string{"q"}, run: cmdQuit, inPopup: true, usage: ":quit", about: "quit (Ctrl+C too)"},
}

// parseCommandLine splits a command line into the command word and the rest.
func parseCommandLine(line string) (word, arg string) {
	word, arg, _ = strings.Cut(strings.TrimSpace(line), " ")
	return word, strings.TrimSpace(arg)
}

// findCommand resolves a word against the commands.
func findCommand(word string) (*command, []string) { return resolveCommand(commands, word) }

// resolveCommand finds the command a word means: a name or short form exactly
// (they are case sensitive: x and X are different commands), else the one name
// the word is the start of. If several names start with it none is chosen and
// their names are returned, so the user can be told what it could be.
func resolveCommand(list []command, word string) (*command, []string) {
	if word == "" {
		return nil, nil
	}
	for i := range list {
		if c := &list[i]; word == c.name || slices.Contains(c.short, word) {
			return c, nil
		}
	}
	var hits []*command
	for i := range list {
		if strings.HasPrefix(list[i].name, word) {
			hits = append(hits, &list[i])
		}
	}
	switch len(hits) {
	case 0:
		return nil, nil
	case 1:
		return hits[0], nil
	}
	names := make([]string, len(hits))
	for i, c := range hits {
		names[i] = c.name
	}
	return nil, names
}

// completeCommand completes the word being typed to the name it is the start of,
// or as far as the names agree. The rest of the line is left alone.
func completeCommand(line string) string {
	if strings.Contains(line, " ") {
		return line
	}
	var hits []string
	for _, c := range commands {
		if strings.HasPrefix(c.name, line) {
			hits = append(hits, c.name)
		}
	}
	switch len(hits) {
	case 0:
		return line
	case 1:
		return hits[0] + " "
	}
	common := hits[0]
	for _, h := range hits[1:] {
		for !strings.HasPrefix(h, common) {
			common = common[:len(common)-1]
		}
	}
	return common
}

// ---- the command line ----

// commandAllowed says whether ":" opens the command line in the current state:
// not where text is being typed (the search bar, the path of the download popup)
// or a question is being answered.
func (m Model) commandAllowed() bool {
	switch m.mode {
	case modeNormal, modeResults, modeQueue, modeDJ:
		return true
	}
	return false
}

func (m Model) openCommandLine() (Model, tea.Cmd) {
	m.cmdFrom, m.mode = m.mode, modeCommand
	m.cmdInput.SetValue("")
	m.cmdHistPos, m.cmdDraft = len(m.cmdHistory), ""
	focus := m.cmdInput.Focus()
	return m, focus
}

// closeCommandLine goes back to the state the line was opened from.
func (m Model) closeCommandLine() Model {
	m.mode = m.cmdFrom
	m.cmdInput.Blur()
	m.clampQueue()
	return m
}

func (m Model) handleCommandKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyEsc:
		return m.closeCommandLine(), nil
	case keyBackspace:
		if m.cmdInput.Value() == "" {
			return m.closeCommandLine(), nil
		}
	case keyEnter:
		line := strings.TrimSpace(m.cmdInput.Value())
		m = m.closeCommandLine()
		if line == "" {
			return m, nil
		}
		if n := len(m.cmdHistory); n == 0 || m.cmdHistory[n-1] != line {
			m.cmdHistory = append(m.cmdHistory, line)
		}
		return m.runCommandLine(line)
	case keyArrowUp: // the arrow, not the letter k: that is text here
		if m.cmdHistPos > 0 {
			if m.cmdHistPos == len(m.cmdHistory) {
				m.cmdDraft = m.cmdInput.Value()
			}
			m.cmdHistPos--
			m.cmdInput.SetValue(m.cmdHistory[m.cmdHistPos])
			m.cmdInput.CursorEnd()
		}
		return m, nil
	case keyArrowDown:
		if m.cmdHistPos < len(m.cmdHistory) {
			m.cmdHistPos++
			text := m.cmdDraft
			if m.cmdHistPos < len(m.cmdHistory) {
				text = m.cmdHistory[m.cmdHistPos]
			}
			m.cmdInput.SetValue(text)
			m.cmdInput.CursorEnd()
		}
		return m, nil
	case keyComplete:
		m.cmdInput.SetValue(completeCommand(m.cmdInput.Value()))
		m.cmdInput.CursorEnd()
		return m, nil
	}
	var cmd tea.Cmd
	m.cmdInput, cmd = m.cmdInput.Update(msg)
	return m, cmd
}

// commandShortcut matches explicit aliases, without expanding command prefixes.
func (m Model) commandShortcut(key string) *command {
	for i := range commands {
		c := &commands[i]
		if slices.Contains(c.short, key) && (m.mode != modeDJ || c.inPopup) {
			return c
		}
	}
	return nil
}

// runCommandLine runs a typed command. The model is already back in the state
// the line was opened from, so a command acts as if called from there.
func (m Model) runCommandLine(line string) (Model, tea.Cmd) {
	word, arg := parseCommandLine(line)
	if word == "" {
		return m, nil
	}
	c, candidates := findCommand(word)
	var msg string
	switch {
	case c == nil && len(candidates) > 0:
		msg = "Ambiguous: " + strings.Join(candidates, ", ")
	case c == nil:
		msg = fmt.Sprintf("Unknown command: %s (:help lists them)", word)
	case m.mode == modeDJ && !c.inPopup:
		msg = "Close the popup first (Esc)"
	default:
		return c.run(m, arg)
	}
	status := m.setStatus(msg, true)
	return m, status
}

// ---- the commands ----

func cmdFind(m Model, arg string) (Model, tea.Cmd) {
	if arg == "" {
		return m.openSearch()
	}
	if next, cmd, ok := m.loadFromText(arg); ok { // a link is loaded, not searched for
		return next, cmd
	}
	m.input.SetValue(arg)
	m.input.CursorEnd()
	m.setStickyStatus("Searching…")
	search := m.startSearch(arg)
	return m, search
}

func cmdAdd(m Model, _ string) (Model, tea.Cmd) {
	if len(m.results) == 0 {
		status := m.setStatus("Nothing to add: search first (:find NAME)", true)
		return m, status
	}
	return m.queueSelected()
}

func cmdSkip(m Model, _ string) (Model, tea.Cmd) {
	p := m.deps.Player
	skip := m.action(false, func(ctx context.Context) error { return p.Skip(ctx) })
	return m, skip
}

func cmdDelete(m Model, _ string) (Model, tea.Cmd) {
	if m.mode != modeQueue {
		status := m.setStatus("Press Tab to focus the queue first", false)
		return m, status
	}
	next, cmd, _ := m.handleQueueKey(keyRemove)
	return next, cmd
}

func cmdDownload(m Model, _ string) (Model, tea.Cmd) {
	switch {
	case m.mode == modeResults && len(m.results) > 0:
		return m.openDownload(m.results[m.selected])
	case m.mode == modeQueue && len(m.upcoming()) > 0:
		e := m.upcoming()[m.queueCursor]
		title, channel, dur := m.entryInfo(e)
		return m.openDownload(yt.Result{ID: yt.KeyOf(e.Filename), Title: title, Channel: channel, Duration: dur})
	}
	return m.downloadPlaying()
}

// cmdYank copies the link of the highlighted song (in the results, the queue or
// the DJ popup), or of the playing one.
func cmdYank(m Model, _ string) (Model, tea.Cmd) {
	var key, title string
	fromEntry := func(e playlistEntry) { key = yt.KeyOf(e.Filename); title, _, _ = m.entryInfo(e) }
	switch {
	case m.mode == modeResults && len(m.results) > 0:
		key, title = m.results[m.selected].ID, m.results[m.selected].Title
	case m.mode == modeQueue && len(m.upcoming()) > 0:
		fromEntry(m.upcoming()[m.queueCursor])
	case m.mode == modeDJ && m.djRowCount() > 0:
		fromEntry(m.djEntry(m.djCursor))
	case m.currentEntry() != nil:
		fromEntry(*m.currentEntry())
	}
	link := yt.URLOf(key)
	if link == "" {
		status := m.setStatus("Nothing to copy", false)
		return m, status
	}
	if m.deps.Copy == nil {
		status := m.setStatus("Copying is not available", true)
		return m, status
	}
	title = safe(title)
	copyText := m.deps.Copy
	return m, func() tea.Msg { return yankDoneMsg{title: title, err: copyText(link)} }
}

// cmdCast opens the speaker picker, connects by name, or returns to local playback.
func cmdCast(m Model, arg string) (Model, tea.Cmd) {
	say := func(text string, isErr bool) (Model, tea.Cmd) {
		status := m.setStatus(text, isErr)
		return m, status
	}
	switch {
	case m.deps.Cast == nil || m.deps.Devices == nil || m.deps.CastOff == nil:
		return say("Casting is not available", true)
	case m.castBusy:
		return say("Still looking for the speaker…", false)
	}
	switch arg = strings.TrimSpace(arg); arg {
	case "":
		return m.openCastPicker()
	case "off":
		return m.connectCast("", true)
	default:
		return m.connectCast(arg, false)
	}
}

func (m Model) connectCast(arg string, local bool) (Model, tea.Cmd) {
	ctx := m.queueCtx
	m.castBusy = true
	if local {
		off := m.deps.CastOff
		return m, func() tea.Msg { return castDoneMsg{off: true, err: off(ctx)} }
	}
	m.setStickyStatus("Connecting to " + arg + "…")
	cast := m.deps.Cast
	return m, func() tea.Msg {
		name, err := cast(ctx, arg)
		return castDoneMsg{name: name, err: err}
	}
}

func cmdAuto(m Model, _ string) (Model, tea.Cmd) { return m.toggleAutoDJ() }

func cmdDJ(m Model, _ string) (Model, tea.Cmd) {
	if m.mode == modeDJ {
		m.mode = m.djFrom
		m.clampQueue()
		return m, nil
	}
	return m.openDJ()
}

// needPopup refuses a command that only means something in the DJ popup.
func needPopup(m Model) (Model, tea.Cmd, bool) {
	if m.mode == modeDJ {
		return m, nil, false
	}
	status := m.setStatus("Open the DJ popup first (:dj)", true)
	return m, status, true
}

func cmdSort(m Model, arg string) (Model, tea.Cmd) {
	if next, cmd, refused := needPopup(m); refused {
		return next, cmd
	}
	switch strings.ToLower(strings.TrimSpace(arg)) {
	case "", "flow":
		m.sortArc = autodj.Flow
	case "build", "up":
		m.sortArc = autodj.Build
	case "down", "wind", "winddown":
		m.sortArc = autodj.WindDown
	default:
		status := m.setStatus("Sort takes build, down or flow (or nothing)", true)
		return m, status
	}
	return m.sortQueueByBPM()
}

func cmdBPM(m Model, arg string) (Model, tea.Cmd) {
	if next, cmd, refused := needPopup(m); refused {
		return next, cmd
	}
	if m.djRowCount() == 0 {
		return m, nil
	}
	m, status, warn := m.setBPM(arg)
	if warn != "" {
		cmd := m.setStatus(warn, true)
		return m, cmd
	}
	cmd := m.setStatus(status, false)
	return m, cmd
}

func cmdRadio(m Model, arg string) (Model, tea.Cmd) {
	n := radioDefault
	if arg = strings.TrimSpace(arg); arg != "" {
		var err error
		if n, err = strconv.Atoi(arg); err != nil || strings.Trim(arg, "0123456789") != "" || n < 1 || n > radioMax {
			status := m.setStatus(fmt.Sprintf("Radio takes a number of songs from 1 to %d (default %d)", radioMax, radioDefault), true)
			return m, status
		}
	}
	return m.startRadio(n, false)
}

func cmdClear(m Model, _ string) (Model, tea.Cmd) { return m.askClear() }

func cmdHelp(m Model, _ string) (Model, tea.Cmd) {
	m.helpFrom, m.mode, m.helpOff = m.mode, modeHelp, 0
	return m, nil
}

func cmdQuit(m Model, _ string) (Model, tea.Cmd) {
	quit := m.quit()
	return m, quit
}

// ---- seek, previous, play ----

type seekKind int

const (
	seekAbsolute seekKind = iota // to a time
	seekRelative                 // by a number of seconds
	seekPercent                  // to a whole percent
)

const seekUsage = "Seek to a time (1:30 or 90), a percent (50%) or by seconds (+10, -30)"

// parseSeek reads what :seek is given: "90" or "1:30" or "1:02:03" (a time),
// "50%" (a whole percent), "+10" or "-1:30" (a number of seconds either way).
func parseSeek(arg string) (kind seekKind, v float64, warn string) {
	arg = strings.TrimSpace(arg)
	if p, ok := strings.CutSuffix(arg, "%"); ok {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 100 {
			return 0, 0, "A percent is a whole number from 0 to 100"
		}
		return seekPercent, float64(n), ""
	}
	kind, sign := seekAbsolute, 1.0
	switch {
	case strings.HasPrefix(arg, "+"):
		kind, arg = seekRelative, arg[1:]
	case strings.HasPrefix(arg, "-"):
		kind, sign, arg = seekRelative, -1, arg[1:]
	}
	secs, ok := parseClock(arg)
	if !ok {
		return 0, 0, seekUsage
	}
	return kind, sign * secs, ""
}

// parseClock reads seconds, m:ss or h:mm:ss; the seconds may have decimals.
func parseClock(s string) (float64, bool) {
	parts := strings.Split(s, ":")
	if len(parts) > 3 {
		return 0, false
	}
	var total float64
	for i, part := range parts {
		digits := strings.Count(part, ".") <= 1 && strings.Trim(part, "0123456789.") == "" && strings.Trim(part, ".") != ""
		if !digits || (i < len(parts)-1 && strings.Contains(part, ".")) {
			return 0, false
		}
		n, err := strconv.ParseFloat(part, 64)
		if err != nil || (i > 0 && n >= 60) {
			return 0, false
		}
		total = total*60 + n
	}
	return total, true
}

func cmdSeek(m Model, arg string) (Model, tea.Cmd) {
	kind, v, warn := parseSeek(arg)
	if warn != "" {
		status := m.setStatus(warn, true)
		return m, status
	}
	if m.currentEntry() == nil {
		status := m.setStatus("Nothing is playing", false)
		return m, status
	}
	p := m.deps.Player
	var seek func(ctx context.Context) error
	var said string
	switch kind {
	case seekAbsolute:
		seek, said = func(ctx context.Context) error { return p.SeekTo(ctx, v) }, "Seek to "+formatClock(v)
	case seekRelative:
		seek, said = func(ctx context.Context) error { return p.SeekBy(ctx, v) }, fmt.Sprintf("Seek %+g s", v)
	default:
		seek, said = func(ctx context.Context) error { return p.SeekPercent(ctx, int(v)) }, fmt.Sprintf("Seek to %d%%", int(v))
	}
	do, say := m.action(true, seek), m.setStatus(said, false)
	return m, tea.Batch(do, say)
}

func cmdPrev(m Model, _ string) (Model, tea.Cmd) {
	if m.currentEntry() == nil {
		status := m.setStatus("Nothing is playing", false)
		return m, status
	}
	p := m.deps.Player
	return m, m.action(false, func(ctx context.Context) error { return p.Prev(ctx) })
}

func cmdPlay(m Model, _ string) (Model, tea.Cmd) {
	switch {
	case m.mode == modeResults && len(m.results) > 0:
		return m.playSelected()
	case m.mode == modeQueue && len(m.upcoming()) > 0:
		return m.playQueueEntry()
	}
	status := m.setStatus("Nothing highlighted: search first, or press Tab for the queue", false)
	return m, status
}

// ---- undo and shuffle ----

func cmdUndo(m Model, _ string) (Model, tea.Cmd) { return m.undoQueue() }

// shufflePerm is a random order of 0..n-1 (a variable so tests can fix it).
var shufflePerm = rand.Perm

func cmdShuffle(m Model, _ string) (Model, tea.Cmd) {
	up := m.upcoming()
	if len(up) < 2 {
		status := m.setStatus("Nothing to shuffle", false)
		return m, status
	}
	if m.queueBusy {
		return m, nil
	}
	m.rememberQueue("shuffle")
	pl := slices.Clone(m.playlist)
	urls := make([]string, len(up))
	base := m.pos + 1
	for k, i := range shufflePerm(len(up)) {
		pl[base+k] = m.playlist[base+i]
		urls[k] = pl[base+k].Filename
	}
	m.playlist, m.sorted = pl, false
	m.clampDJ()
	m.queueBusy = true
	p := m.deps.Player
	reorder := func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		return queueEditDoneMsg{err: p.ReorderUpcoming(ctx, urls)}
	}
	status := m.setStatus("Shuffled the queue", false)
	return m, tea.Batch(reorder, status)
}

// ---- volume ----

func cmdVol(m Model, arg string) (Model, tea.Cmd) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		text := "Volume " + strconv.FormatFloat(m.volume, 'f', -1, 64) + "%"
		if m.muted {
			text = "Muted (volume " + strconv.FormatFloat(m.volume, 'f', -1, 64) + "%)"
		}
		status := m.setStatus(text, false)
		return m, status
	}
	rel := strings.HasPrefix(arg, "+") || strings.HasPrefix(arg, "-")
	digits := strings.TrimLeft(arg, "+-")
	n, err := strconv.ParseFloat(digits, 64)
	if err != nil || strings.Trim(digits, "0123456789.") != "" || (!rel && len(digits) != len(arg)) || n < 0 || (!rel && n > 100) {
		status := m.setStatus("Volume is a number from 0 to 100, or +N / -N", true)
		return m, status
	}
	if rel {
		if strings.HasPrefix(arg, "-") {
			n = -n
		}
		return m.changeVolume(n)
	}
	return m.setVolume(n)
}

func cmdMute(m Model, _ string) (Model, tea.Cmd) {
	m.muted = !m.muted
	if !m.muted {
		return m.sendVolume("Volume " + strconv.FormatFloat(m.volume, 'f', -1, 64) + "%")
	}
	return m.sendVolume("Muted")
}
