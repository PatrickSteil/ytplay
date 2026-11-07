package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/PatrickSteil/ytplay/internal/analyze"
	"github.com/PatrickSteil/ytplay/internal/autodj"
)

// rampWave is a 200 s song that gets louder and louder.
func rampWave() analyze.Wave {
	l := make([]uint8, 100)
	for i := range l {
		l[i] = uint8(i * 255 / 99)
	}
	return analyze.Wave{Seconds: 200, Level: l}
}

// waveModel is song "a" playing, 50 s into 200, and analysed with the given wave.
func waveModel(t *testing.T, w, h int, wave analyze.Wave) Model {
	t.Helper()
	m := analysedModel(t, w, h)
	m, _ = update(m, analysisMsg{id: "a", res: analyze.Result{BPM: 116, Key: "F# minor", Camelot: "11A", KeyScore: .9, Wave: wave}})
	return m
}

// analysedModel is the same before the analysis has an answer.
func analysedModel(t *testing.T, w, h int) Model {
	t.Helper()
	var f statsFakes
	m := statsModel(&f)
	m.width, m.height = w, h
	m, _ = startStats(t, m, "0", "*a", "b")
	m, _ = update(m, positionMsg{elapsed: 50, total: 200})
	return m
}

// column is the text of the now-playing column of a wide screen, frames and
// padding cut off.
func column(m Model) []string {
	lw, _, _ := m.colWidths()
	var out []string
	for _, l := range viewLines(m) {
		r := []rune(ansi.Strip(l))
		if len(r) >= lw {
			out = append(out, string(r[2:lw-1]))
		}
	}
	return out
}

func stripAll(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = ansi.Strip(l)
	}
	return out
}

func TestTheWaveformIsUnderTheCover(t *testing.T) {
	m := waveModel(t, 140, 40, rampWave())
	rows := m.waveRows(m.colInnerHeight())
	if rows == 0 {
		t.Fatal("no waveform on a 140x40 screen")
	}
	col := column(m)
	last := -1 // the last row of the cover
	for i, l := range col {
		if strings.Contains(l, strings.Repeat("░", coverCols)) {
			last = i
		}
	}
	if last < 0 {
		t.Fatalf("no cover:\n%s", m.View())
	}
	want := stripAll(drawWave(rampWave(), 200, 50, m.textWidth(), rows, m.theme()))
	if got := col[last+2 : last+2+rows]; !equalLines(got, want) {
		t.Errorf("under the cover:\n%q\nwant\n%q", got, want)
	}
	if strings.TrimSpace(col[last+1]) != "" || strings.TrimSpace(col[last+2+rows]) != "" {
		t.Errorf("the waveform has no blank line around it: %q, %q", col[last+1], col[last+2+rows])
	}
	if !strings.Contains(col[last+3+rows], "Alpha") {
		t.Errorf("the title does not follow: %q", col[last+3+rows])
	}
}

func TestTheWaveformFollowsThePlayhead(t *testing.T) {
	withColours(t)
	m := waveModel(t, 140, 40, rampWave())
	rows := m.waveRows(m.colInnerHeight())
	var seen []string
	for _, elapsed := range []float64{10, 150} {
		m, _ = update(m, positionMsg{elapsed: elapsed, total: 200})
		out := m.View()
		for _, row := range drawWave(rampWave(), 200, elapsed, m.textWidth(), rows, m.theme()) {
			if !strings.Contains(out, row) {
				t.Fatalf("at %v s the screen lacks the row %q", elapsed, row)
			}
		}
		seen = append(seen, drawWave(rampWave(), 200, elapsed, m.textWidth(), rows, m.theme())[0])
	}
	if seen[0] == seen[1] {
		t.Error("the playhead moved but the waveform did not change")
	}
}

func TestTheWaveformIsPlacedInTheWholeSong(t *testing.T) {
	// Only 50 s of the 200 s were analysed (the middle of a long track would be
	// the same): the rest is a flat line, and where the playhead is depends on
	// the length of the song, which is the player's, or else the track's.
	wave := analyze.Wave{Offset: 100, Seconds: 50, Level: flat(255, 50)}
	for name, total := range map[string]float64{"length from the player": 200, "length from the track": 0} {
		m := waveModel(t, 140, 40, wave)
		m, _ = update(m, positionMsg{elapsed: 120, total: total})
		rows := m.waveRows(m.colInnerHeight())
		out := m.View()
		for _, row := range drawWave(wave, 200, 120, m.textWidth(), rows, m.theme()) {
			if !strings.Contains(out, row) {
				t.Errorf("%s: the screen lacks %q", name, row)
			}
		}
	}
}

func TestTheWaveformIsAFlatLineUntilTheAnalysisHasAnswered(t *testing.T) {
	for name, m := range map[string]Model{
		"pending": analysedModel(t, 140, 40),
		"failed": func() Model {
			m := analysedModel(t, 140, 40)
			m, _ = update(m, analysisMsg{id: "a", err: errors.New("no audio")})
			return m
		}(),
	} {
		rows := m.waveRows(m.colInnerHeight())
		if rows == 0 {
			t.Fatalf("%s: the strip is not kept free", name)
		}
		out := m.View()
		for _, row := range drawWave(analyze.Wave{}, 200, 50, m.textWidth(), rows, m.theme()) {
			if !strings.Contains(out, row) {
				t.Errorf("%s: the screen lacks the flat line %q", name, row)
			}
		}
	}
}

func TestNoWaveformWhenNothingIsAnalysed(t *testing.T) {
	// Without analysis there is nothing to show, ever: no strip, no flat line.
	m := at(queueModel(t, 3), 140, 40)
	if rows := m.waveRows(m.colInnerHeight()); rows != 0 {
		t.Errorf("%d rows without an analysis", rows)
	}
	if out := m.View(); strings.ContainsAny(out, "▁▇") {
		t.Errorf("a waveform appears:\n%s", out)
	}
	if rows := at(newTestModel(), 140, 40).waveRows(37); rows != 0 {
		t.Errorf("%d rows with nothing playing", rows)
	}
}

func TestTheWaveformGrowsWithTheScreenAndLeavesRoomForTheStats(t *testing.T) {
	for height, want := range map[int]int{14: 0, 23: 0, 24: 2, 29: 2, 30: 4, 60: 4} {
		m := waveModel(t, 140, height, rampWave())
		if got := m.waveRows(m.colInnerHeight()); got != want {
			t.Errorf("height %d: %d rows, want %d", height, got, want)
		}
	}
}

func TestTheWaveformNeverOverflowsTheColumns(t *testing.T) {
	for _, size := range [][2]int{{108, 14}, {108, 24}, {120, 30}, {140, 30}, {140, 40}, {200, 60}, {300, 80}, {80, 30}, {90, 40}, {60, 24}, {50, 24}} {
		m := waveModel(t, size[0], size[1], rampWave())
		ls := viewLines(m)
		if len(ls) != size[1] {
			t.Errorf("%dx%d: %d lines", size[0], size[1], len(ls))
		}
		for i, l := range ls {
			if w := lipgloss.Width(l); w > size[0] {
				t.Errorf("%dx%d: line %d is %d wide", size[0], size[1], i, w)
			}
			if size[0] >= 108 && i < len(ls)-1 && lipgloss.Width(l) != size[0] {
				t.Errorf("%dx%d: box line %d is %d wide, want %d", size[0], size[1], i, lipgloss.Width(l), size[0])
			}
		}
	}
}

func TestTheStackedLayoutDrawsTheWaveformBesideTheBottomOfTheCover(t *testing.T) {
	m := waveModel(t, 90, 40, rampWave())
	textW := m.width - coverCols - 3
	var coverRowsSeen []string
	for _, l := range viewLines(m) {
		if s := ansi.Strip(l); strings.Contains(s, strings.Repeat("░", coverCols)) {
			coverRowsSeen = append(coverRowsSeen, string([]rune(s)[coverCols+3:]))
		}
	}
	if len(coverRowsSeen) != coverRows {
		t.Fatalf("%d cover rows", len(coverRowsSeen))
	}
	want := stripAll(drawWave(rampWave(), 200, 50, textW, 2, m.theme()))
	if got := coverRowsSeen[coverRows-2:]; !equalLines(got, want) {
		t.Errorf("beside the bottom of the cover:\n%q\nwant\n%q", got, want)
	}
	// and the panel is not taller than before
	if len(viewLines(m)) != 40 {
		t.Errorf("%d lines on a 40 line screen", len(viewLines(m)))
	}
}

func TestTheTransitionIsMarkedOnTheWaveformWhileAutoDJIsOn(t *testing.T) {
	withColours(t)
	m := waveModel(t, 140, 40, rampWave())
	rows := m.waveRows(m.colInnerHeight())
	a, b := sureSongOf(200, 199.6, 0.5, 0.3, 199.2), sureSongOf(180, 175, 0.5, 0.3, 174.8)
	m.songs.put("a", a)
	m.songs.put("b", b)
	p := autodj.For(a, b)
	if p.Kind != autodj.BeatMatch {
		t.Fatalf("the test needs a beat-matched plan: %s", p.Why)
	}
	m.autoDJ = true
	out := m.View()
	for _, row := range drawWaveMarked(rampWave(), 200, 50, m.textWidth(), rows, m.theme(), p.AStart, p.AStart+p.Overlap) {
		if !strings.Contains(out, row) {
			t.Fatalf("the screen lacks the marked row %q", row)
		}
	}
	m.autoDJ = false
	for _, row := range drawWave(rampWave(), 200, 50, m.textWidth(), rows, m.theme()) {
		if !strings.Contains(m.View(), row) {
			t.Fatalf("with autodj off the screen lacks the plain row %q", row)
		}
	}
}

func TestWithoutAnalysisTheTransitionIsTheLastSecondsAndNothingIsMarkedWithoutANextSong(t *testing.T) {
	m := waveModel(t, 140, 40, rampWave())
	m.autoDJ = true
	from, to := m.mixZone()
	if want := 200 - autodj.FadeSeconds; from != want || to != 200 {
		t.Errorf("zone %v..%v, want the last %v s", from, to, autodj.FadeSeconds)
	}
	last := at(m, 140, 40)
	last.playlist = last.playlist[:1] // only the playing song
	if f, e := last.mixZone(); f != 0 || e != 0 {
		t.Errorf("zone %v..%v with no next song", f, e)
	}
}
