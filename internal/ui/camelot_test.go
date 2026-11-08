package ui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"strconv"
	"strings"
	"testing"

	"github.com/PatrickSteil/ytplay/internal/analyze"
)

func TestCamelotColoursFollowTheWheel(t *testing.T) {
	withColours(t)
	seen := map[string]string{}
	for n := 1; n <= 12; n++ {
		code := itoa(n) + "A"
		out := camelotStyle(code).Render(code)
		for other, prev := range seen {
			if prev == out && other != code {
				t.Errorf("%s and %s have the same colour", code, other)
			}
		}
		seen[code] = out
		// minor and major of a number are the same colour: they mix
		if camelotStyle(itoa(n)+"B").GetForeground() != camelotStyle(code).GetForeground() {
			t.Errorf("%dA and %dB differ in colour", n, n)
		}
	}
	for _, bad := range []string{"", "13A", "0B", "11C", "x", "A11"} {
		if camelotStyle(bad).GetForeground() != dim.GetForeground() {
			t.Errorf("%q is not drawn in the plain gray", bad)
		}
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestTheDJPopupShowsEachSongsKeyInItsColour(t *testing.T) {
	withColours(t)
	m := at(queueModel(t, 3), 100, 30)
	m.stats["e0"] = trackStats{hasRes: true, res: analyze.Result{BPM: 120, Camelot: "11A"}}
	m.stats["e1"] = trackStats{hasRes: true, res: analyze.Result{BPM: 124, Camelot: "12A"}}
	out := strings.Join(m.djBox(), "\n")
	for _, code := range []string{"11A", "12A"} {
		if !strings.Contains(out, camelotStyle(code).Render(code)) {
			t.Errorf("the popup lacks %s in its colour:\n%s", code, out)
		}
	}
	m.djCursor = 0 // the highlighted row keeps the key's colour as well
	if !strings.Contains(strings.Join(m.djBox(), "\n"), camelotStyle("11A").Render("11A")) {
		t.Error("the highlighted row lost the colour of its key")
	}
}

func TestCamelotHuesAreSpreadOverTheCircleAndAMissingKeyIsBlank(t *testing.T) {
	for n, deg := range map[int]float64{1: 0, 5: 120, 12: 330} {
		if got := camelotStyle(itoa(n) + "A").GetForeground(); got != lipgloss.Color(hueColour(deg)) {
			t.Errorf("%dA is %v, want the hue at %v degrees", n, got, deg)
		}
	}
	if got := queueModel(t, 2).djKey("unknown"); got != "    " {
		t.Errorf("key of an unanalysed song = %q, want four blanks", got)
	}
}

func TestThePlayingSongsKeyIsInItsColourInTheNowPlayingLine(t *testing.T) {
	withColours(t)
	m := playingModel(t, false, 140)
	m.stats["e0"] = trackStats{hasRes: true, res: analyze.Result{BPM: 120, Key: "F# minor", Camelot: "11A"}}
	line := m.bpmKeyLine()
	if !strings.Contains(line, camelotStyle("11A").Render("F# minor · 11A")) {
		t.Errorf("the key is not in the colour of 11A:\n%q", line)
	}
	if !strings.Contains(ansi.Strip(line), "120 BPM · F# minor · 11A") {
		t.Errorf("the text changed: %q", ansi.Strip(line))
	}
	m.stats["e0"] = trackStats{hasRes: true, res: analyze.Result{BPM: 120}} // no key: nothing coloured by it
	if strings.Contains(m.bpmKeyLine(), "\x1b[38;2;") && strings.Contains(m.bpmKeyLine(), camelotStyle("11A").Render("1")) {
		t.Error("a song without a key got a key colour")
	}
}

func TestThePlayingSongsRowInTheQueueIsInItsKeysColour(t *testing.T) {
	withColours(t)
	m := at(queueModel(t, 3), 140, 30)
	m.stats["e0"] = trackStats{hasRes: true, res: analyze.Result{BPM: 120, Camelot: "11A"}}
	open := strings.TrimSuffix(camelotStyle("11A").Render("X"), "X\x1b[0m")
	playing := m.queueRows(60)[0]
	if !strings.Contains(playing, open+"Title-e0") {
		t.Errorf("the playing row is not in the colour of 11A:\n%q", playing)
	}
	if next := m.queueRows(60)[1]; strings.Contains(next, open) {
		t.Errorf("a queued song is coloured by a key it has not:\n%q", next)
	}
}

func TestTheHelpBoxIsWide(t *testing.T) {
	m := at(newTestModel(), 160, 50)
	m.mode = modeHelp
	if w := lipgloss.Width(m.helpBox()[0]); w != 100 {
		t.Errorf("help box is %d wide, want 100", w)
	}
	narrow := at(newTestModel(), 80, 50)
	narrow.mode = modeHelp
	if w := lipgloss.Width(narrow.helpBox()[0]); w != 80 {
		t.Errorf("on 80 columns the help box is %d wide, want the whole screen", w)
	}
}
