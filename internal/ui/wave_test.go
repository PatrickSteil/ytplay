package ui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/PatrickSteil/ytplay/internal/analyze"
)

func glyphs(cells [][]waveCell) []string {
	out := make([]string, len(cells))
	for i, row := range cells {
		var b strings.Builder
		for _, c := range row {
			b.WriteString(c.glyph)
		}
		out[i] = b.String()
	}
	return out
}

func equalLines(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func flat(level uint8, buckets int) []uint8 {
	l := make([]uint8, buckets)
	for i := range l {
		l[i] = level
	}
	return l
}

func TestWaveCellsFollowTheLevelMirroredAroundTheMiddle(t *testing.T) {
	w := analyze.Wave{Seconds: 4, Level: []uint8{0, 85, 170, 255}}

	// Two rows: the top one grows up from the middle line, the bottom one down.
	// Parts of a cell that hang from the top are the reversed lower blocks.
	cells := waveCells(w, 4, 0, 4, 2)
	if got, want := glyphs(cells), []string{"▁▃▅█", "▇▅▃█"}; !equalLines(got, want) {
		t.Errorf("two rows: %q, want %q", got, want)
	}
	for i, wantFlip := range []bool{true, true, true, false} {
		if cells[0][i].flip {
			t.Errorf("top row column %d is flipped", i)
		}
		if cells[1][i].flip != wantFlip {
			t.Errorf("bottom row column %d: flip %v, want %v", i, cells[1][i].flip, wantFlip)
		}
	}

	// Four rows: two each way, so twice the steps.
	if got, want := glyphs(waveCells(w, 4, 0, 4, 4)), []string{"  ▃█", "▁▅██", "▇▃██", "  ▅█"}; !equalLines(got, want) {
		t.Errorf("four rows: %q, want %q", got, want)
	}
}

func TestWaveCellsTakeTheLoudestSliceOfAColumn(t *testing.T) {
	w := analyze.Wave{Seconds: 4, Level: []uint8{200, 10, 40, 30}}
	if got, want := glyphs(waveCells(w, 4, 0, 2, 2))[0], "▆▁"; got != want {
		t.Errorf("two columns over four slices: %q, want %q (the loudest of each pair)", got, want)
	}
}

func TestWaveCellsMarkWhatWasPlayed(t *testing.T) {
	w := analyze.Wave{Seconds: 10, Level: flat(255, 10)}
	cells := waveCells(w, 10, 4.5, 10, 2)
	for i, c := range cells[0] {
		want := toneUnplayed
		switch {
		case i < 4:
			want = tonePlayed
		case i == 4:
			want = tonePlayhead
		}
		if c.tone != want {
			t.Errorf("column %d: tone %v, want %v", i, c.tone, want)
		}
	}
	for i, c := range waveCells(w, 10, 10, 10, 2)[0] {
		if c.tone != tonePlayed {
			t.Errorf("at the end column %d is %v, want played", i, c.tone)
		}
	}
}

func TestWaveCellsKnowWhatTheyDoNotKnow(t *testing.T) {
	// The middle 30 s of a five minute track were analysed: the rest is unknown,
	// drawn as a flat line, whatever the playhead says.
	w := analyze.Wave{Offset: 100, Seconds: 30, Level: flat(255, 30)}
	cells := waveCells(w, 300, 150, 30, 2)
	for i, c := range cells[0] {
		wantKnown := i >= 10 && i <= 12
		if known := c.tone != toneUnknown; known != wantKnown {
			t.Errorf("column %d (%d s..): known %v, want %v", i, i*10, known, wantKnown)
		}
		if !wantKnown && c.glyph != "▁" {
			t.Errorf("unknown column %d is %q, want the flat line", i, c.glyph)
		}
	}
	if cells[0][10].tone != tonePlayed {
		t.Errorf("known and played: %v", cells[0][10].tone)
	}
}

func TestWaveCellsWithoutAWaveAreAFlatLine(t *testing.T) {
	for _, total := range []float64{0, 200} {
		cells := waveCells(analyze.Wave{}, total, 30, 6, 2)
		for i, c := range cells[0] {
			if c.tone != toneUnknown || c.glyph != "▁" {
				t.Errorf("total %v: column %d is %+v", total, i, c)
			}
		}
		for i, c := range cells[1] {
			if c.tone != toneUnknown || c.glyph != "▇" || !c.flip {
				t.Errorf("total %v: bottom column %d is %+v", total, i, c)
			}
		}
	}
}

func TestWaveCellsFallBackToTheAudioWhenTheSongIsOfUnknownLength(t *testing.T) {
	w := analyze.Wave{Seconds: 4, Level: []uint8{0, 85, 170, 255}}
	if got, want := glyphs(waveCells(w, 0, 0, 4, 2)), []string{"▁▃▅█", "▇▅▃█"}; !equalLines(got, want) {
		t.Errorf("no total: %q, want %q", got, want)
	}
}

func TestWaveCellsOfNoSpaceAreNothing(t *testing.T) {
	w := analyze.Wave{Seconds: 4, Level: flat(255, 4)}
	for _, c := range [][2]int{{0, 2}, {-3, 2}, {4, 0}, {4, 1}} {
		if cells := waveCells(w, 4, 0, c[0], c[1]); cells != nil {
			t.Errorf("width %d rows %d gave %v", c[0], c[1], cells)
		}
	}
}

func TestDrawWaveIsAsWideAsAskedInEveryColour(t *testing.T) {
	withColours(t)
	w := analyze.Wave{Seconds: 100, Level: flat(180, 100)}
	for _, th := range []theme{blueTheme, greenTheme} {
		for width := 1; width <= 80; width += 7 {
			for _, rows := range []int{2, 4} {
				lines := drawWave(w, 100, 37, width, rows, th)
				if len(lines) != rows {
					t.Fatalf("width %d: %d lines, want %d", width, len(lines), rows)
				}
				for _, l := range lines {
					if got := lipgloss.Width(l); got != width {
						t.Errorf("width %d rows %d: a line is %d wide", width, rows, got)
					}
				}
			}
		}
	}
}

func TestDrawWaveColoursWhatWasPlayedInTheThemesAccent(t *testing.T) {
	withColours(t)
	w := analyze.Wave{Seconds: 10, Level: flat(255, 10)}
	for _, c := range []struct {
		th     theme
		accent string
	}{{blueTheme, blueAccent}, {greenTheme, greenAccent}} {
		line := drawWave(w, 10, 5.5, 10, 2, c.th)[0]
		played, unplayed := strings.Index(line, c.accent), strings.Index(line, grayFrame)
		if played < 0 || unplayed < 0 || played > unplayed {
			t.Errorf("accent %q at %d, gray at %d: the played part should come first in the accent:\n%q", c.accent, played, unplayed, line)
		}
		if !strings.Contains(line, "\x1b[1m") {
			t.Errorf("the playhead is not bold:\n%q", line)
		}
	}
}

func TestDrawWaveHangsTheBottomHalfFromTheMiddleWithReverseVideo(t *testing.T) {
	withColours(t)
	w := analyze.Wave{Seconds: 2, Level: []uint8{128, 255}}
	lines := drawWave(w, 2, 2, 2, 2, blueTheme) // both played: the same colour, only the flip differs
	reverse := regexp.MustCompile(`\x1b\[(?:[0-9;]*;)?7(?:;[0-9;]*)?m`)
	if reverse.MatchString(lines[0]) {
		t.Errorf("the top half is reversed:\n%q", lines[0])
	}
	if !reverse.MatchString(lines[1]) {
		t.Errorf("a partly filled cell of the bottom half is not reversed:\n%q", lines[1])
	}
	if got := ansi.Strip(lines[1]); got != "▄█" {
		t.Errorf("bottom half %q, want %q", got, "▄█")
	}
	// the full cell next to it is not reversed (it would be empty): drawn apart
	if !strings.Contains(lines[1], "▄\x1b[0m") {
		t.Errorf("the reversed cell and the full one share a style:\n%q", lines[1])
	}
}

func TestWaveCellsMarkWhereTheNextSongComesIn(t *testing.T) {
	w := analyze.Wave{Seconds: 10, Level: flat(255, 10)}
	cells := waveCellsMarked(w, 10, 2, 10, 2, 6, 9)
	for i, c := range cells[0] {
		if want := i >= 6 && i < 9; c.mix != want {
			t.Errorf("column %d: mix %v, want %v", i, c.mix, want)
		}
	}
	for _, c := range waveCells(w, 10, 2, 10, 2)[0] {
		if c.mix {
			t.Fatal("a waveform without a transition has a marked column")
		}
	}
	// what is not known is not marked
	part := analyze.Wave{Offset: 5, Seconds: 5, Level: flat(255, 5)}
	if c := waveCellsMarked(part, 10, 0, 10, 2, 2, 8)[0]; c[3].mix || !c[6].mix {
		t.Errorf("unknown column marked or known one not: %v %v", c[3].mix, c[6].mix)
	}
}

func TestDrawWaveLightsUpTheTransitionInTheBoldAccent(t *testing.T) {
	withColours(t)
	w := analyze.Wave{Seconds: 10, Level: flat(255, 10)}
	for _, th := range []theme{blueTheme, greenTheme} {
		line := drawWaveMarked(w, 10, 2, 10, 2, th, 6, 9)[0]
		if !strings.Contains(line, th.accentB.Render("███")) {
			t.Errorf("the transition is not drawn in the bold accent:\n%q", line)
		}
		if plain := drawWave(w, 10, 2, 10, 2, th)[0]; strings.Contains(plain, th.accentB.Render("███")) {
			t.Error("no transition, but a part is lit")
		}
	}
}

func TestAPlayedTransitionIsNotLitUp(t *testing.T) {
	withColours(t)
	w := analyze.Wave{Seconds: 10, Level: flat(255, 10)}
	if line := drawWaveMarked(w, 10, 10, 10, 2, blueTheme, 6, 9)[0]; strings.Contains(line, blueTheme.accentB.Render("███")) {
		t.Errorf("a transition already played is still lit:\n%q", line)
	}
}
