package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestXAsksBeforeClearing(t *testing.T) {
	m := queueModel(t, 4) // 3 upcoming
	m, _ = send(m, "X")
	if m.mode != modeConfirm {
		t.Fatalf("mode = %v, want the confirmation popup", m.mode)
	}
	out := m.View()
	for _, want := range []string{"Clear the queue?", "3 upcoming songs", "y yes", "n/esc no"} {
		if !strings.Contains(out, want) {
			t.Errorf("popup missing %q:\n%s", want, out)
		}
	}
	if n := len(actionsOf(m).calls); n != 0 {
		t.Errorf("player called %d times before confirming", n)
	}
}

func TestPopupWordsASingleSong(t *testing.T) {
	m := queueModel(t, 2)
	m, _ = send(m, "X")
	if out := m.View(); !strings.Contains(out, "1 upcoming song?") {
		t.Errorf("singular wording missing:\n%s", out)
	}
}

func TestConfirmingClearsTheQueueButKeepsThePlayingSong(t *testing.T) {
	m := queueModel(t, 4)
	m, _ = send(m, "X")
	m, cmd := send(m, "y")
	m = run(m, cmd)
	if got := actionsOf(m).calls; len(got) != 1 || got[0] != "clear" {
		t.Errorf("calls = %q", got)
	}
	if m.mode != modeNormal {
		t.Errorf("mode = %v, want normal", m.mode)
	}
	if queueIDs(m) != "e0" || m.pos != 0 {
		t.Errorf("playlist = %s pos=%d, want only the playing song", queueIDs(m), m.pos)
	}
	if m.status != "Queue cleared" {
		t.Errorf("status = %q", m.status)
	}
	if out := m.View(); !strings.Contains(out, "Title-e0") || !strings.Contains(out, "Up next (0)") {
		t.Errorf("the playing song must stay:\n%s", out)
	}
}

func TestCancelKeepsTheQueue(t *testing.T) {
	for _, k := range []string{"n", "esc"} {
		m := queueModel(t, 4)
		m, _ = send(m, "X")
		m, _ = send(m, k)
		if m.mode != modeNormal || queueIDs(m) != "e0,e1,e2,e3" {
			t.Errorf("%s: mode=%v playlist=%s", k, m.mode, queueIDs(m))
		}
		if n := len(actionsOf(m).calls); n != 0 {
			t.Errorf("%s: player called %d times", k, n)
		}
		if strings.Contains(m.View(), "Clear the queue?") {
			t.Errorf("%s: popup still visible", k)
		}
	}
}

func TestPopupReturnsToWhereItWasOpened(t *testing.T) {
	m := withResults(t, 3)
	m, _ = update(m, plEvent(t, "*a", "b", "c"))
	m, _ = update(m, propEvent("playlist-pos", "0"))
	m, _ = send(m, "X", "n")
	if m.mode != modeResults {
		t.Errorf("mode = %v, want results", m.mode)
	}
	m, _ = send(m, "tab", "X", "n")
	if m.mode != modeQueue {
		t.Errorf("mode = %v, want queue", m.mode)
	}
}

func TestOtherKeysAreIgnoredWhileAsking(t *testing.T) {
	m := queueModel(t, 4)
	m, _ = send(m, "X")
	for _, k := range []string{"s", " ", "5", "=", "r", "q", "Q", "/", "enter", "x", "tab", "j"} {
		next, c := send(m, k)
		if next.mode != modeConfirm {
			t.Errorf("key %q left the popup (mode %v)", k, next.mode)
		}
		if c != nil && isQuit(c) {
			t.Errorf("key %q quit the app", k)
		}
		if n := len(actionsOf(next).calls); n != 0 {
			t.Errorf("key %q triggered %d player calls", k, n)
		}
	}
	if _, cmd := send(m, "ctrl+c"); !isQuit(cmd) {
		t.Error("ctrl+c must still quit")
	}
}

func TestNothingToClear(t *testing.T) {
	m := queueModel(t, 1)
	m, _ = send(m, "X")
	if m.mode != modeNormal || !strings.Contains(m.status, "empty") {
		t.Errorf("mode=%v status=%q", m.mode, m.status)
	}
	idle := newTestModel()
	idle, _ = send(idle, "X")
	if idle.mode != modeNormal {
		t.Errorf("idle: mode = %v", idle.mode)
	}
}

func TestXIsTypableInTheSearchBar(t *testing.T) {
	m := queueModel(t, 4)
	m, _ = send(m, "/", "X")
	if m.mode != modeSearch || m.input.Value() != "X" {
		t.Errorf("mode=%v input=%q", m.mode, m.input.Value())
	}
}

func TestConfirmWhenTheQueueEmptiedMeanwhileDoesNothing(t *testing.T) {
	m := queueModel(t, 4)
	m, _ = send(m, "X")
	m, _ = update(m, plEvent(t, "*e0")) // e.g. removed elsewhere
	m, cmd := send(m, "y")
	m = run(m, cmd)
	if n := len(actionsOf(m).calls); n != 0 || m.mode != modeNormal {
		t.Errorf("calls=%d mode=%v", n, m.mode)
	}
}

func TestPopupKeepsTheScreenExactlyTheSameSize(t *testing.T) {
	for _, size := range [][2]int{{140, 40}, {100, 24}, {90, 30}, {60, 16}, {30, 10}, {20, 6}} {
		m := at(queueModel(t, 6), size[0], size[1])
		m, _ = update(m, positionMsg{elapsed: 5, total: 100})
		plain := viewLines(m)
		m, _ = send(m, "X")
		popup := viewLines(m)
		if len(popup) != len(plain) {
			t.Errorf("%v: %d lines with popup, %d without", size, len(popup), len(plain))
		}
		for i, l := range popup {
			if w := lipgloss.Width(l); w > size[0] {
				t.Errorf("%v: line %d is %d wide", size, i, w)
			}
			if i < len(plain) && lipgloss.Width(plain[i]) == size[0] && lipgloss.Width(l) != size[0] {
				t.Errorf("%v: line %d changed width from %d to %d", size, i, size[0], lipgloss.Width(l))
			}
		}
		if size[0] >= 40 && !strings.Contains(strings.Join(popup, "\n"), "Clear the queue?") {
			t.Errorf("%v: popup text missing", size)
		}
	}
}

func TestPopupIsCentered(t *testing.T) {
	m := at(queueModel(t, 4), 120, 30)
	m, _ = send(m, "X")
	ls := viewLines(m)
	row, col := -1, -1
	for i, l := range ls {
		if c := strings.Index(ansi.Strip(l), "Clear the queue?"); c >= 0 {
			row, col = i, c
		}
	}
	if row < 10 || row > 20 {
		t.Errorf("popup row %d is not near the vertical center of 30", row)
	}
	if col < 30 || col > 70 {
		t.Errorf("popup column %d is not near the horizontal center of 120", col)
	}
}

func TestOverlayKeepsStyledContentAroundTheBox(t *testing.T) {
	red := "\x1b[31m"
	reset := "\x1b[0m"
	base := []string{
		red + strings.Repeat("a", 20) + reset,
		red + strings.Repeat("b", 20) + reset,
		red + strings.Repeat("c", 20) + reset,
	}
	got := overlay(base, []string{"[xx]", "[yy]"}, 20, 8, 1)
	want := []string{strings.Repeat("a", 20), strings.Repeat("b", 8) + "[xx]" + strings.Repeat("b", 8), strings.Repeat("c", 8) + "[yy]" + strings.Repeat("c", 8)}
	for i := range want {
		if ansi.Strip(got[i]) != want[i] {
			t.Errorf("line %d = %q, want %q", i, ansi.Strip(got[i]), want[i])
		}
	}
	if !strings.Contains(got[1], red) {
		t.Error("styling of the surrounding text was lost")
	}
	// A box taller than the screen must not panic or grow the screen.
	if out := overlay(base, make([]string, 10), 20, 0, 0); len(out) != 3 {
		t.Errorf("got %d lines", len(out))
	}
}

func TestPopupSurvivesTinyTerminals(t *testing.T) {
	for _, size := range [][2]int{{1, 1}, {2, 3}, {5, 2}, {10, 1}} {
		m := at(queueModel(t, 3), size[0], size[1])
		m, _ = send(m, "X")
		_ = m.View() // must not panic
	}
}
