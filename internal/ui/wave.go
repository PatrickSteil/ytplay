package ui

import (
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/PatrickSteil/ytplay/internal/analyze"
	"github.com/PatrickSteil/ytplay/internal/autodj"
	"github.com/PatrickSteil/ytplay/internal/yt"
)

// The waveform of the playing song, drawn under its cover: the loudness over
// time, mirrored around a middle line, in what has been played, the playhead,
// what is still to come and, where the analysis has not seen the song, a flat
// line.

// waveTone says which colour a column of the waveform is drawn in.
type waveTone int

const (
	toneUnknown  waveTone = iota // nothing is known about this stretch of the song
	tonePlayed                   // already played: the accent of the theme
	tonePlayhead                 // the column being played now
	toneUnplayed                 // still to come
)

// style is the colour of a tone in a theme. Everything is the theme's or the
// plain gray and bold of the rest of the screen: a waveform names no colour.
func (t waveTone) style(th theme) lipgloss.Style {
	switch t {
	case tonePlayed:
		return th.accent
	case tonePlayhead:
		return bold
	case toneUnplayed:
		return dim
	}
	return th.frame
}

// waveCell is one character of the waveform. A cell of the bottom half that is
// only partly filled is drawn the other way round (flip): the lower block
// character with the colours reversed, since only the lower eighths exist as
// characters and the bottom half hangs from the middle line.
type waveCell struct {
	glyph string
	tone  waveTone
	flip  bool
	mix   bool // the next song comes in here (autodj): drawn lit up
}

var lowerBlock = [...]string{"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}

// glyphEighths is the character filling f eighths of a cell from the bottom.
func glyphEighths(f int) string {
	if f <= 0 {
		return " "
	}
	return lowerBlock[min(f, 8)-1]
}

// tiny keeps float rounding from putting a column in the wrong slice.
const tiny = 1e-6

// waveCells lays the waveform of a song of total seconds out in width columns
// and rows rows (an even number: half of them above the middle line, half
// below it), with the playhead at elapsed seconds. Each column shows the
// loudest slice of the stretch of the song it stands for, at least as a thin
// line. Without the song's length (total 0) the audio that was analysed is the
// whole width. nil if there is no room.
func waveCells(w analyze.Wave, total, elapsed float64, width, rows int) [][]waveCell {
	return waveCellsMarked(w, total, elapsed, width, rows, 0, 0)
}

// waveCellsMarked is waveCells with the columns that stand for the seconds
// mixFrom to mixTo of the song marked, where known: the transition to the next song.
func waveCellsMarked(w analyze.Wave, total, elapsed float64, width, rows int, mixFrom, mixTo float64) [][]waveCell {
	rows -= rows % 2
	if width <= 0 || rows < 2 {
		return nil
	}
	half := rows / 2
	end := w.Offset + w.Seconds
	span := total
	if span <= 0 {
		span = end
	}
	n := len(w.Level)
	slice := 0.0
	if n > 0 {
		slice = w.Seconds / float64(n)
	}
	cells := make([][]waveCell, rows)
	for r := range cells {
		cells[r] = make([]waveCell, width)
	}
	for c := 0; c < width; c++ {
		t0, t1 := span*float64(c)/float64(width), span*float64(c+1)/float64(width)
		level, known := 0.0, false
		if from, to := math.Max(t0, w.Offset), math.Min(t1, end); slice > 0 && to-from > tiny {
			known = true
			lo := int(math.Floor((from-w.Offset)/slice + tiny))
			hi := int(math.Ceil((to-w.Offset)/slice - tiny))
			lo = min(max(lo, 0), n-1)
			hi = min(max(hi, lo+1), n)
			var peak uint8
			for _, v := range w.Level[lo:hi] {
				peak = max(peak, v)
			}
			level = float64(peak) / 255
		}
		tone := toneUnknown
		switch {
		case !known:
		case t1 <= elapsed+tiny:
			tone = tonePlayed
		case t0 <= elapsed+tiny:
			tone = tonePlayhead
		default:
			tone = toneUnplayed
		}
		mix := known && math.Min(t1, mixTo)-math.Max(t0, mixFrom) > tiny
		e := min(max(int(math.Round(level*float64(8*half))), 1), 8*half) // eighths of a half
		for r := 0; r < half; r++ {                                      // the top half, r = 0 the outermost row
			cells[r][c] = waveCell{glyph: glyphEighths(min(max(e-8*(half-1-r), 0), 8)), tone: tone, mix: mix}
		}
		for j := 0; j < half; j++ { // the bottom half, j = 0 next to the middle line
			f := min(max(e-8*j, 0), 8)
			cell := waveCell{glyph: glyphEighths(f), tone: tone, mix: mix}
			if f > 0 && f < 8 {
				cell.glyph, cell.flip = glyphEighths(8-f), true
			}
			cells[half+j][c] = cell
		}
	}
	return cells
}

// drawWave is waveCells in the colours of the theme, one string per row.
func drawWave(w analyze.Wave, total, elapsed float64, width, rows int, th theme) []string {
	return drawWaveMarked(w, total, elapsed, width, rows, th, 0, 0)
}

// drawWaveMarked is drawWave with the transition to the next song, mixFrom to
// mixTo seconds into the song, lit up in the bold accent where it is still to come.
func drawWaveMarked(w analyze.Wave, total, elapsed float64, width, rows int, th theme, mixFrom, mixTo float64) []string {
	cells := waveCellsMarked(w, total, elapsed, width, rows, mixFrom, mixTo)
	lines := make([]string, len(cells))
	for i, row := range cells {
		var line strings.Builder
		for start := 0; start < len(row); {
			end := start + 1
			for end < len(row) && row[end].tone == row[start].tone && row[end].flip == row[start].flip && row[end].mix == row[start].mix {
				end++
			}
			var run strings.Builder
			for _, c := range row[start:end] {
				run.WriteString(c.glyph)
			}
			style := row[start].tone.style(th)
			if row[start].mix && row[start].tone == toneUnplayed {
				style = th.accentB
			}
			if row[start].flip {
				style = style.Reverse(true)
			}
			line.WriteString(style.Render(run.String()))
			start = end
		}
		lines[i] = line.String()
	}
	return lines
}

// waveRows is how many rows the waveform under the cover has in a column that
// is innerH rows high: none if there is nothing to show (no analysis to ask, no
// song) or too little room to leave the stats below it a few rows, two on an
// ordinary screen, four on a tall one. The rows are kept free from the moment a
// song plays, so the column does not jump when the analysis is done.
func (m Model) waveRows(innerH int) int {
	if m.deps.Analyze == nil || m.currentID() == "" {
		return 0
	}
	// the cover and its blank line and three lines of text, then a blank line, the
	// waveform, a blank line, and the room the stats are left
	for _, c := range []struct{ rows, statsRoom int }{{4, 8}, {2, 4}} {
		if innerH >= coverColHeight+c.rows+2+c.statsRoom {
			return c.rows
		}
	}
	return 0
}

// waveLines is the waveform of the playing song, width columns by rows rows.
func (m Model) waveLines(width, rows int) []string {
	id := m.currentID()
	var w analyze.Wave
	if st := m.stats[id]; st.hasRes {
		w = st.res.Wave
	}
	total := m.total
	if total <= 0 {
		total = m.tracks[id].Duration
	}
	from, to := m.mixZone()
	return drawWaveMarked(w, total, m.elapsed, width, rows, m.theme(), from, to)
}

// mixZone is where in the playing song the next one comes in, in seconds: what
// autodj has planned (the same plan the player makes from the same analyses). 0, 0
// when autodj is off or no song follows.
func (m Model) mixZone() (from, to float64) {
	cur, next := m.currentEntry(), m.upcoming()
	if !m.autoDJ || cur == nil || len(next) == 0 {
		return 0, 0
	}
	known := func(url string, length float64) autodj.Song {
		if s, ok := m.songs.lookup(url); ok {
			return s
		}
		return autodj.Song{Length: length}
	}
	length := m.total
	if length <= 0 {
		length = m.tracks[m.currentID()].Duration
	}
	p := autodj.For(known(cur.Filename, length), known(next[0].Filename, m.tracks[yt.KeyOf(next[0].Filename)].Duration))
	return p.AStart, p.AStart + p.Overlap
}

// besideCover puts the waveform at the bottom of the lines of text next to the
// cover of the stacked layout, where the cover is taller than the text: two rows
// and a blank line before them, or nothing if they do not fit.
func (m Model) besideCover(text []string, width int) []string {
	const rows = 2
	if m.deps.Analyze == nil || m.currentID() == "" || width < 4 || len(text)+1+rows > coverRows {
		return text
	}
	for len(text) < coverRows-rows {
		text = append(text, "")
	}
	return append(text, m.waveLines(width, rows)...)
}
