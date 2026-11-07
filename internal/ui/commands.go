package ui

import (
	"context"
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/PatrickSteil/ytplay/internal/yt"
)

// A vim-style command line: ":" opens it, a command is typed as a name (or its
// short form) and arguments, Enter runs it, Esc cancels. Everything that used to
// have its own letter is a command now; the keys that remain are the ones for
// moving about (j k Tab Enter Esc x Space 0-9 =).

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
	{name: "find", short: []string{"f"}, run: cmdFind, usage: ":find NAME", about: "search for NAME (no name: open the search bar)"},
	{name: "add", run: cmdAdd, usage: ":add", about: "add the highlighted result to the queue"},
	{name: "skip", short: []string{"s"}, run: cmdSkip, usage: ":skip", about: "skip to the next song"},
	{name: "delete", short: []string{"x"}, run: cmdDelete, usage: ":delete", about: "remove the highlighted queue entry (or x)"},
	{name: "download", short: []string{"d"}, run: cmdDownload, usage: ":download", about: "save the highlighted song to a file"},
	{name: "auto", short: []string{"a"}, run: cmdAuto, inPopup: true, usage: ":auto", about: "autodj on or off (the next song mixes in early)"},
	{name: "dj", run: cmdDJ, inPopup: true, usage: ":dj", about: "the DJ popup: the queue with its BPM"},
	{name: "sort", short: []string{"b"}, run: cmdSort, inPopup: true, usage: ":sort", about: "DJ popup: sort the queue by BPM (again: reverse)"},
	{name: "bpm", short: []string{"e"}, run: cmdBPM, inPopup: true, usage: ":bpm N", about: "DJ popup: set a song's BPM (alone: the detected one)"},
	{name: "radio", short: []string{"r"}, run: cmdRadio, usage: ":radio", about: "add 20 songs similar to the queue"},
	{name: "clear", short: []string{"X"}, run: cmdClear, usage: ":clear", about: "clear the queue (asks first)"},
	{name: "help", short: []string{"h"}, run: cmdHelp, inPopup: true, usage: ":help", about: "this help"},
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
		return m.openDownload(yt.Result{ID: yt.VideoID(e.Filename), Title: title, Channel: channel, Duration: dur})
	}
	return m.downloadPlaying()
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

func cmdSort(m Model, _ string) (Model, tea.Cmd) {
	if next, cmd, refused := needPopup(m); refused {
		return next, cmd
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

func cmdRadio(m Model, _ string) (Model, tea.Cmd) { return m.startRadio() }

func cmdClear(m Model, _ string) (Model, tea.Cmd) { return m.askClear() }

func cmdHelp(m Model, _ string) (Model, tea.Cmd) {
	m.helpFrom, m.mode, m.helpOff = m.mode, modeHelp, 0
	return m, nil
}

func cmdQuit(m Model, _ string) (Model, tea.Cmd) {
	quit := m.quit()
	return m, quit
}
