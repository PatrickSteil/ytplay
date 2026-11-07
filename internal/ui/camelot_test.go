package ui

import (
	"github.com/charmbracelet/lipgloss"
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
