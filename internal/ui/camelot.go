package ui

import (
	"fmt"
	"math"
	"strconv"

	"github.com/charmbracelet/lipgloss"
)

// Keys are drawn in a colour that follows the Camelot wheel: the twelve numbers
// are twelve hues around the colour circle, and the letter (minor A, major B)
// does not change it. Keys that mix well are the same number or one step
// either way, so they are the same colour or a neighbouring one.

// camelotStyle is the style a Camelot code such as "11A" is drawn in; the plain
// gray for anything that is not one.
func camelotStyle(code string) lipgloss.Style {
	if len(code) < 2 || (code[len(code)-1] != 'A' && code[len(code)-1] != 'B') {
		return dim
	}
	n, err := strconv.Atoi(code[:len(code)-1])
	if err != nil || n < 1 || n > 12 {
		return dim
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(hueColour(float64(n-1) * 30)))
}

// hueColour is the colour of a hue in degrees, bright enough for a dark terminal.
func hueColour(h float64) string {
	const s, l = 0.65, 0.62
	c := (1 - math.Abs(2*l-1)) * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := l - c/2
	var r, g, b float64
	switch int(h/60) % 6 {
	case 0:
		r, g, b = c, x, 0
	case 1:
		r, g, b = x, c, 0
	case 2:
		r, g, b = 0, c, x
	case 3:
		r, g, b = 0, x, c
	case 4:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	return fmt.Sprintf("#%02x%02x%02x", int((r+m)*255+.5), int((g+m)*255+.5), int((b+m)*255+.5))
}

// djKey is the key column of the DJ popup for a song, four cells wide: its
// Camelot code in its colour, blank if the key is not known.
func (m Model) djKey(id string) string {
	st := m.stats[id]
	if !st.hasRes || st.res.Camelot == "" {
		return "    "
	}
	return " " + camelotStyle(st.res.Camelot).Render(fmt.Sprintf("%-3s", st.res.Camelot))
}
