package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/PatrickSteil/ytplay/internal/yt"
)

// snap is what a key press can change that a person would notice.
type snap struct {
	mode                                mode
	selected, queueCursor, djCursor     int
	helpOff, calls, undo, bpmSet, query int
	status, search, path                string
	autoDJ                              bool
}

func snapshot(m Model) snap {
	return snap{
		mode: m.mode, selected: m.selected, queueCursor: m.queueCursor, djCursor: m.djCursor,
		helpOff: m.helpOff, calls: len(actionsOf(m).calls), undo: len(m.undo), bpmSet: len(m.bpmSet),
		status: m.status, search: m.input.Value(), path: m.dlInput.Value(), autoDJ: m.autoDJ,
	}
}

// press sends keys, running what they start.
func press(m Model, keys ...string) Model {
	for _, k := range keys {
		var cmd tea.Cmd
		m, cmd = send(m, k)
		m = run(m, cmd)
	}
	return m
}

// do types a command and runs it.
func do(m Model, text string) Model {
	m, cmd := typeCommand(m, text)
	return run(m, cmd)
}

type probe struct {
	hint string
	act  func(Model) Model
}

func withDownload(m Model) Model {
	m.deps.Download = func(ctx context.Context, id, path string) error { return nil }
	return m
}

// The footer names the keys that work in the state the program is in, and only
// those: in every state each of its hints, used there, changes something, and
// the footer says nothing else.
func TestTheFooterNamesExactlyTheKeysThatWorkHere(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "abc.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	withResultsAndQueue := func() Model {
		m := queueModel(t, 4)
		m.results, m.selected, m.mode = results(3), 0, modeResults
		return m
	}
	help := func(m Model) Model { m.width, m.height = 90, 14; return do(m, "help") }
	states := []struct {
		name   string
		build  func() Model
		probes []probe
	}{
		{"nothing is playing", func() Model { return newTestModel() }, []probe{
			{": command", func(m Model) Model { return press(m, ":") }},
			{"/ search", func(m Model) Model { return press(m, "/") }},
			{"+/- volume", func(m Model) Model { return press(m, "+") }},
			{"? help", func(m Model) Model { return press(m, "?") }},
		}},
		{"playing, nothing queued", func() Model { return queueModel(t, 1) }, []probe{
			{": command", func(m Model) Model { return press(m, ":") }},
			{"/ search", func(m Model) Model { return press(m, "/") }},
			{"␣ pause", func(m Model) Model { return press(m, " ") }},
			{"←/→ seek", func(m Model) Model { return press(m, "left") }},
			{"+/- volume", func(m Model) Model { return press(m, "+") }},
			{"? help", func(m Model) Model { return press(m, "?") }},
		}},
		{"playing, something queued", func() Model { return queueModel(t, 3) }, []probe{
			{": command", func(m Model) Model { return press(m, ":") }},
			{"/ search", func(m Model) Model { return press(m, "/") }},
			{"⇥ queue", func(m Model) Model { return press(m, "tab") }},
			{"␣ pause", func(m Model) Model { return press(m, " ") }},
			{"←/→ seek", func(m Model) Model { return press(m, "right") }},
			{"+/- volume", func(m Model) Model { return press(m, "-") }},
			{"? help", func(m Model) Model { return press(m, "?") }},
		}},
		{"the results are focused", withResultsAndQueue, []probe{
			{"↑/↓ move", func(m Model) Model { return press(m, "down") }},
			{"⏎ play", func(m Model) Model { return press(m, "enter") }},
			{":add queue", func(m Model) Model { return do(m, "add") }},
			{"/ search", func(m Model) Model { return press(m, "/") }},
			{"⇥ queue", func(m Model) Model { return press(m, "tab") }},
			{": command", func(m Model) Model { return press(m, ":") }},
			{"? help", func(m Model) Model { return press(m, "?") }},
		}},
		{"the queue is focused", func() Model { m, _ := send(queueModel(t, 4), "tab"); return m }, []probe{
			{"j/k select", func(m Model) Model { return press(m, "j") }},
			{"⏎ play", func(m Model) Model { return press(m, "enter") }},
			{"↑/↓ move", func(m Model) Model { return press(m, "down") }},
			{"x remove", func(m Model) Model { return press(m, "x") }},
			{"u undo", func(m Model) Model { return press(m, "u") }},
			{"⇥/esc back", func(m Model) Model { return press(m, "tab") }},
			{": command", func(m Model) Model { return press(m, ":") }},
			{"? help", func(m Model) Model { return press(m, "?") }},
		}},
		{"the search bar is open", func() Model { m, _ := send(newTestModel(), "/", "d", "a", "f", "t"); return m }, []probe{
			{"⏎ search", func(m Model) Model { return press(m, "enter") }},
			{"esc close", func(m Model) Model { return press(m, "esc") }},
		}},
		{"the DJ popup is open", func() Model { return djModel(t, 4, map[string]float64{"e1": 100, "e2": 120}) }, []probe{
			{"j/k select", func(m Model) Model { return press(m, "j") }},
			{":sort", func(m Model) Model { return do(m, "sort") }},
			{":bpm N", func(m Model) Model { return do(m, "bpm 128") }},
			{":auto", func(m Model) Model { return do(m, "auto") }},
			{"u undo", func(m Model) Model { return press(m, "u") }},
			{"esc close", func(m Model) Model { return press(m, "esc") }},
			{": command", func(m Model) Model { return press(m, ":") }},
			{"? help", func(m Model) Model { return press(m, "?") }},
		}},
		{"the help is open", func() Model { return help(queueModel(t, 3)) }, []probe{
			{"j/k scroll", func(m Model) Model { return press(m, "j") }},
			{"esc close", func(m Model) Model { return press(m, "esc") }},
		}},
		{"the help is open on a tall terminal", func() Model {
			m := queueModel(t, 3)
			m.width, m.height = 100, 100
			return do(m, "help")
		}, []probe{
			{"esc close", func(m Model) Model { return press(m, "esc") }},
		}},
		{"the queue is being cleared", func() Model { m, _ := typeCommand(queueModel(t, 3), "clear"); return m }, []probe{
			{"y yes", func(m Model) Model { return press(m, "y") }},
			{"n no", func(m Model) Model { return press(m, "n") }},
			{"esc cancel", func(m Model) Model { return press(m, "esc") }},
		}},
		{"the download popup is open", func() Model {
			m := withDownload(withResults(t, 2))
			m, _ = typeCommand(m, "download")
			m.dlInput.SetValue(filepath.Join(dir, "a"))
			return m
		}, []probe{
			{"⏎ save", func(m Model) Model { return press(m, "enter") }},
			{"⇥ complete", func(m Model) Model { return press(m, "tab") }},
			{"esc cancel", func(m Model) Model { return press(m, "esc") }},
		}},
	}
	for _, st := range states {
		var want []string
		for _, p := range st.probes {
			want = append(want, p.hint)
		}
		base := st.build()
		if got := base.help(); got != strings.Join(want, "  ") {
			t.Errorf("%s: the footer says\n  %q\nwant exactly\n  %q", st.name, got, strings.Join(want, "  "))
		}
		for _, p := range st.probes {
			m := st.build()
			before := snapshot(m)
			if after := snapshot(p.act(m)); after == before {
				t.Errorf("%s: the footer says %q but using it changed nothing", st.name, p.hint)
			}
		}
	}
}

// The footer is drawn at the bottom right where there is room for it.
func TestTheFooterIsShownOnTheStatusLine(t *testing.T) {
	m := queueModel(t, 3)
	m.width = 120
	if got := statusText(m); !strings.Contains(got, "⇥ queue") || !strings.Contains(got, "? help") {
		t.Errorf("status line = %q, want the hints of this state", got)
	}
}

func TestNoFooterHintForAKeyThatIsGone(t *testing.T) {
	for _, m := range []Model{newTestModel(), queueModel(t, 3), withResults(t, 2)} {
		h := m.help()
		for _, gone := range []string{"q queue", "r radio", "D dj", "X clear", "s skip", "Q quit", "d save"} {
			if strings.Contains(h, gone) {
				t.Errorf("the footer %q still names %q", h, gone)
			}
		}
	}
	_ = yt.Result{}
}
