package ui

import (
	"image"
	"image/color"
	"image/draw"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/PatrickSteil/ytplay/internal/cover"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
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
	blueAccent = "\x1b[38;5;75m"  // the accent of a song without a cover colour
	grayFrame  = "\x1b[38;5;245m" // and the frame of a panel that is not focused
)

// coverTheme is a theme as a cover gives it, and the code of its accent in the
// 256-colour palette (#00d787 is colour 42 of it).
var coverTheme = newTheme("#00d787", "#5f8787")

const coverAccent = "\x1b[38;5;42m"

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

// plainCover is a rendered cover of a picture of one colour.
func plainCover(c color.RGBA) string {
	img := image.NewRGBA(image.Rect(0, 0, 64, 36))
	draw.Draw(img, img.Bounds(), image.NewUniform(c), image.Point{}, draw.Src)
	return cover.Render(img, 10, 5)
}

func greenCover() string { return plainCover(color.RGBA{30, 180, 60, 255}) }

// coveredModel is playingModel once the cover of the playing song (e0) has arrived.
func coveredModel(t *testing.T, autoDJ bool, width int, rendered string) Model {
	t.Helper()
	m, _ := update(playingModel(t, autoDJ, width), coverMsg{id: "e0", rendered: rendered})
	return m
}

// codeOf is the escape sequence that starts text in style s.
func codeOf(s lipgloss.Style) string { return strings.SplitN(s.Render("x"), "x", 2)[0] }

func TestThemeIsBlueWithoutACoverColour(t *testing.T) {
	for name, m := range map[string]Model{
		"no cover":      playingModel(t, false, 140),
		"no cover, dj":  playingModel(t, true, 140),
		"gray cover":    coveredModel(t, false, 140, "COVER"),
		"nothing plays": newTestModel(),
	} {
		th := m.theme()
		if th.accent.GetForeground() != lipgloss.Color("75") || th.frame.GetForeground() != lipgloss.Color("245") {
			t.Errorf("%s: accent %v, frame %v, want blue 75 and gray 245", name, th.accent.GetForeground(), th.frame.GetForeground())
		}
		if !th.accentB.GetBold() {
			t.Errorf("%s: the bold accent must stay bold", name)
		}
	}
}

func TestThemeTakesTheColourOfTheCoverOfThePlayingSong(t *testing.T) {
	m := coveredModel(t, false, 140, greenCover())
	th := m.theme()
	if th.accent.GetForeground() == lipgloss.Color("75") || th.frame.GetForeground() == lipgloss.Color("245") {
		t.Fatalf("the theme did not change: accent %v, frame %v", th.accent.GetForeground(), th.frame.GetForeground())
	}
	acc, frame, _ := cover.Accent(greenCover())
	if th.accent.GetForeground() != lipgloss.Color(acc) || th.frame.GetForeground() != lipgloss.Color(frame) {
		t.Errorf("accent %v, frame %v, want %s and %s", th.accent.GetForeground(), th.frame.GetForeground(), acc, frame)
	}
	if !th.accentB.GetBold() {
		t.Error("the bold accent must stay bold")
	}
}

func TestThemeFollowsTheSongThatPlays(t *testing.T) {
	m := coveredModel(t, false, 140, greenCover())
	// the cover of a song that is not playing changes nothing
	m2, _ := update(m, coverMsg{id: "e1", rendered: "COVER"})
	if m2.theme().accent.GetForeground() != m.theme().accent.GetForeground() {
		t.Error("the cover of another song changed the theme")
	}
	// the next song has no colour: back to blue
	m, _ = update(m, propEvent("playlist-pos", "1"))
	if m.theme().accent.GetForeground() != lipgloss.Color("75") {
		t.Errorf("the next song has no cover colour, accent is %v, want blue", m.theme().accent.GetForeground())
	}
	// and back to the first one
	m, _ = update(m, propEvent("playlist-pos", "0"))
	if m.theme().accent.GetForeground() == lipgloss.Color("75") {
		t.Error("the first song is playing again, but the theme is still blue")
	}
}

func TestTheWholeScreenTakesTheCoverColour(t *testing.T) {
	withColours(t)
	for name, width := range map[string]int{"three columns": 140, "stacked": 80} {
		plain, covered := playingModel(t, false, width).View(), coveredModel(t, false, width, greenCover())
		accent := codeOf(covered.theme().accent)
		if !strings.Contains(plain, blueAccent) {
			t.Errorf("%s, no cover colour: should be blue", name)
		}
		v := covered.View()
		if !strings.Contains(v, accent) || !strings.Contains(v, codeOf(covered.theme().frame)) {
			t.Errorf("%s: no accent or frame of the cover in the screen", name)
		}
		if strings.Contains(v, blueAccent) {
			t.Errorf("%s: a blue accent is left over (a place that does not use the theme)", name)
		}
	}
}

func TestTheFramesTakeTheCoverColourToo(t *testing.T) {
	withColours(t)
	top := func(m Model) string { return strings.Split(m.View(), "\n")[0] }
	m := coveredModel(t, false, 140, greenCover())
	if l := top(playingModel(t, false, 140)); !strings.Contains(l, grayFrame) {
		t.Errorf("normal frame line should be gray: %q", l)
	}
	if l := top(m); !strings.Contains(l, codeOf(m.theme().frame)) || strings.Contains(l, grayFrame) {
		t.Errorf("frame line should be in the frame colour of the cover: %q", l)
	}
	// stacked layout: the rules between the parts
	m = coveredModel(t, false, 80, greenCover())
	if r := m.rule(); !strings.Contains(r, codeOf(m.theme().frame)) {
		t.Errorf("the rule should be in the frame colour of the cover: %q", r)
	}
	if r := playingModel(t, false, 80).rule(); !strings.Contains(r, grayFrame) {
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
		plain := strings.Join(box(playingModel(t, false, 140)), "\n")
		m := coveredModel(t, false, 140, greenCover())
		covered := strings.Join(box(m), "\n")
		if !strings.Contains(plain, blueAccent) {
			t.Errorf("%s without a cover colour should be blue", name)
		}
		if !strings.Contains(covered, codeOf(m.theme().accent)) || strings.Contains(covered, blueAccent) {
			t.Errorf("%s should be in the colour of the cover, not blue", name)
		}
	}
}

// autodj is shown by a badge on the progress line, not by the colours.
func TestAutoDJDoesNotChangeTheColours(t *testing.T) {
	withColours(t)
	for name, rendered := range map[string]string{"cover colour": plainCover(color.RGBA{200, 30, 30, 255}), "no cover colour": "COVER"} {
		for _, width := range []int{140, 80} {
			off, on := coveredModel(t, false, width, rendered), coveredModel(t, true, width, rendered)
			if off.theme().accent.GetForeground() != on.theme().accent.GetForeground() || off.theme().frame.GetForeground() != on.theme().frame.GetForeground() {
				t.Errorf("%s, width %d: autodj changed the theme", name, width)
			}
			if strings.Contains(on.View(), "\x1b[38;5;78m") || strings.Contains(on.View(), "\x1b[38;5;65m") {
				t.Errorf("%s, width %d: autodj draws in green", name, width)
			}
		}
	}
}

func TestAutoDJShowsABadgeInTheNowPlayingText(t *testing.T) {
	for _, width := range []int{140, 80} {
		off, on := playingModel(t, false, width), playingModel(t, true, width)
		if strings.Contains(ansi.Strip(off.View()), "AUTODJ") {
			t.Errorf("width %d: a badge with autodj off", width)
		}
		if !strings.Contains(ansi.Strip(on.View()), "AUTODJ") {
			t.Errorf("width %d: no badge with autodj on", width)
		}
	}
	// also while the song loads
	m := playingModel(t, true, 140)
	m.loading = true
	if !strings.Contains(ansi.Strip(m.progressLine()), "AUTODJ") {
		t.Error("no badge while loading")
	}
}

func TestSwitchingAutoDJOnAndOffShowsAndHidesTheBadge(t *testing.T) {
	m := queueModel(t, 4)
	m.width, m.height = 140, 40
	m.deps.SetAutoDJ = (&autoRecorder{}).set
	m, _ = typeCommand(m, "dj")
	m = editCommand(m, "auto")
	m, _ = send(m, "esc")
	if !strings.Contains(ansi.Strip(m.View()), "AUTODJ") {
		t.Error("with autodj on the screen should show the badge")
	}
	m, _ = typeCommand(m, "dj")
	m = editCommand(m, "auto")
	m, _ = send(m, "esc")
	if strings.Contains(ansi.Strip(m.View()), "AUTODJ") {
		t.Error("with autodj off again the badge should be gone")
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
	// the declaration and the one place that chooses
	if n := strings.Count(text, "blueTheme"); n != 2 {
		t.Errorf("blueTheme appears %d times in view.go, want 2 (declaration and m.theme())", n)
	}
	colours := regexp.MustCompile(`lipgloss\.Color\("(\d+)"\)`).FindAllStringSubmatch(text, -1)
	for _, c := range colours {
		if c[1] != "245" && c[1] != "196" { // the gray of dim text and the red of errors; the rest is newTheme's argument
			t.Errorf("view.go draws in colour %s directly", c[1])
		}
	}
}
