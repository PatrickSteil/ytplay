package ui

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// withColours makes lipgloss emit colour codes, which it does not when the
// output is not a terminal (as in tests). The codes of the 256-colour palette
// are "\x1b[38;5;<n>m".
func withColours(t *testing.T) {
	t.Helper()
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(old) })
}

const (
	blueAccent  = "\x1b[38;5;75m"  // the normal accent
	grayFrame   = "\x1b[38;5;245m" // the normal frame of a panel that is not focused
	greenAccent = "\x1b[38;5;78m"  // the accent while autodj is on
	greenFrame  = "\x1b[38;5;65m"  // and the frame
)

// playingModel is a wide screen with a song playing, autodj on or off.
func playingModel(t *testing.T, autoDJ bool, width int) Model {
	t.Helper()
	m := queueModel(t, 4)
	m.width, m.height = width, 40
	m.autoDJ = autoDJ
	// as many coloured parts as possible: a progress bar, the stats and BPM line
	// under the song, and a cursor in the DJ popup that is not on the playing song
	m.elapsed, m.total, m.loading = 30, 200, false
	m.stats["e0"] = trackStats{hasInfo: true, info: fullInfo}
	m.bpmSet["e0"] = 120
	m.djCursor = 1
	return m
}

func TestThemeIsBlueUnlessAutoDJIsOn(t *testing.T) {
	off, on := queueModel(t, 2).theme(), playingModel(t, true, 140).theme()
	if off.accent.GetForeground() != lipgloss.Color("75") || off.frame.GetForeground() != lipgloss.Color("245") {
		t.Errorf("normal theme: accent %v, frame %v, want blue 75 and gray 245", off.accent.GetForeground(), off.frame.GetForeground())
	}
	if on.accent.GetForeground() != lipgloss.Color("78") || on.frame.GetForeground() != lipgloss.Color("65") {
		t.Errorf("autodj theme: accent %v, frame %v, want green 78 and muted green 65", on.accent.GetForeground(), on.frame.GetForeground())
	}
	if !on.accentB.GetBold() || !off.accentB.GetBold() {
		t.Error("the bold accent must stay bold in both themes")
	}
}

func TestTheWholeScreenTurnsGreenWhileAutoDJIsOn(t *testing.T) {
	withColours(t)
	for name, width := range map[string]int{"three columns": 140, "stacked": 80} {
		off, on := playingModel(t, false, width).View(), playingModel(t, true, width).View()
		if !strings.Contains(off, blueAccent) || strings.Contains(off, greenAccent) || strings.Contains(off, greenFrame) {
			t.Errorf("%s, autodj off: should be blue and have no green", name)
		}
		if !strings.Contains(on, greenAccent) || !strings.Contains(on, greenFrame) {
			t.Errorf("%s, autodj on: no green accent or frame in the screen", name)
		}
		if strings.Contains(on, blueAccent) {
			t.Errorf("%s, autodj on: a blue accent is left over (a place that does not use the theme)", name)
		}
	}
}

func TestTheFramesAreGreenToo(t *testing.T) {
	withColours(t)
	top := func(m Model) string { return strings.Split(m.View(), "\n")[0] }
	if l := top(playingModel(t, false, 140)); !strings.Contains(l, grayFrame) || strings.Contains(l, greenFrame) {
		t.Errorf("normal frame line should be gray: %q", l)
	}
	if l := top(playingModel(t, true, 140)); !strings.Contains(l, greenFrame) || strings.Contains(l, grayFrame) {
		t.Errorf("autodj frame line should be green: %q", l)
	}
	// stacked layout: the rules between the parts
	rule := func(m Model) string { return m.rule() }
	if r := rule(playingModel(t, true, 80)); !strings.Contains(r, greenFrame) {
		t.Errorf("the rule should be green: %q", r)
	}
	if r := rule(playingModel(t, false, 80)); !strings.Contains(r, grayFrame) {
		t.Errorf("the normal rule should be gray: %q", r)
	}
}

func TestPopupsFollowTheTheme(t *testing.T) {
	withColours(t)
	for name, box := range map[string]func(Model) []string{
		"DJ popup":       func(m Model) []string { return m.djBox() },
		"help popup":     func(m Model) []string { return m.helpBox() },
		"confirm popup":  func(m Model) []string { return m.confirmBox() },
		"download popup": func(m Model) []string { return m.downloadBox() },
	} {
		off, on := strings.Join(box(playingModel(t, false, 140)), "\n"), strings.Join(box(playingModel(t, true, 140)), "\n")
		if !strings.Contains(off, blueAccent) || strings.Contains(off, greenAccent) {
			t.Errorf("%s with autodj off should be blue", name)
		}
		if !strings.Contains(on, greenAccent) || strings.Contains(on, blueAccent) {
			t.Errorf("%s with autodj on should be green, not blue", name)
		}
	}
}

func TestSwitchingAutoDJOnAndOffChangesTheColours(t *testing.T) {
	withColours(t)
	m := queueModel(t, 4)
	m.width, m.height = 140, 40
	m.deps.SetAutoDJ = (&autoRecorder{}).set
	m, _ = typeCommand(m, "dj")
	m = editCommand(m, "auto")
	m, _ = send(m, "esc")
	if v := m.View(); !strings.Contains(v, greenFrame) || strings.Contains(v, blueAccent) {
		t.Error("with autodj on the screen should be green")
	}
	m, _ = typeCommand(m, "dj")
	m = editCommand(m, "auto")
	m, _ = send(m, "esc")
	if v := m.View(); strings.Contains(v, greenAccent) || strings.Contains(v, greenFrame) || !strings.Contains(v, blueAccent) {
		t.Error("with autodj off again the screen should be blue")
	}
}

// Nothing may name a theme or a colour of its own: the screen is drawn in
// m.theme(), whichever it is. (The old global accent styles are gone, so this
// is the only way left to draw something in the wrong colour.)
func TestOnlyTheThemeChoiceNamesAThemeOrAnAccentColour(t *testing.T) {
	src, err := os.ReadFile("view.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	for _, name := range []string{"blueTheme", "greenTheme"} {
		// the declaration and the one place that chooses
		if n := strings.Count(text, name); n != 2 {
			t.Errorf("%s appears %d times in view.go, want 2 (declaration and m.theme())", name, n)
		}
	}
	colours := regexp.MustCompile(`lipgloss\.Color\("(\d+)"\)`).FindAllStringSubmatch(text, -1)
	for _, c := range colours {
		if c[1] != "245" && c[1] != "196" { // the gray of dim text and the red of errors; the rest is newTheme's argument
			t.Errorf("view.go draws in colour %s directly", c[1])
		}
	}
}
