package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/PatrickSteil/ytplay/internal/yt"
)

func at(m Model, w, h int) Model {
	m.width, m.height = w, h
	return m
}

func viewLines(m Model) []string { return strings.Split(m.View(), "\n") }

func TestWideLayoutIsThreeFramedColumns(t *testing.T) {
	m := at(queueModel(t, 4), 140, 30)
	top := viewLines(m)[0]
	if strings.Count(top, "┌") != 3 || strings.Count(top, "┐") != 3 {
		t.Fatalf("expected three boxes on the top line, got %q", top)
	}
	i1, i2, i3 := strings.Index(top, "Now playing"), strings.Index(top, "Up next (3)"), strings.Index(top, "Search")
	if i1 < 0 || i2 < 0 || i3 < 0 || !(i1 < i2 && i2 < i3) {
		t.Errorf("titles in wrong order or missing: %q", top)
	}
	if last := viewLines(m)[29-1]; strings.Count(last, "└") != 3 {
		t.Errorf("bottom border line = %q", last)
	}
}

func TestWideViewFillsTheScreenExactly(t *testing.T) {
	for _, size := range [][2]int{{108, 30}, {140, 40}, {200, 50}, {108, 16}, {120, 8}} {
		w, h := size[0], size[1]
		m := at(withResults(t, 20), w, h)
		m, _ = update(m, plEvent(t, "*a", "b", "c"))
		m, _ = update(m, propEvent("playlist-pos", "0"))
		ls := viewLines(m)
		if len(ls) != h {
			t.Errorf("%dx%d: %d lines, want %d", w, h, len(ls), h)
		}
		for i, l := range ls {
			if lw := lipgloss.Width(l); lw > w {
				t.Errorf("%dx%d: line %d is %d wide: %q", w, h, i, lw, l)
			}
			if i < h-1 && lipgloss.Width(l) != w {
				t.Errorf("%dx%d: box row %d is %d wide, want exactly %d", w, h, i, lipgloss.Width(l), w)
			}
		}
	}
}

func TestNarrowTerminalKeepsStackedLayout(t *testing.T) {
	m := at(queueModel(t, 4), 107, 30)
	if strings.Contains(m.View(), "┌") {
		t.Error("below 108 columns the stacked layout should be used")
	}
	if !strings.Contains(m.View(), "Up next (3)") {
		t.Error("stacked layout lost the queue header")
	}
}

func TestWideSearchBarIsAlwaysVisible(t *testing.T) {
	m := at(queueModel(t, 2), 140, 30)
	if !strings.Contains(m.View(), "search YouTube") {
		t.Errorf("idle search bar hint missing:\n%s", m.View())
	}
	m, _ = send(m, "/", "a", "b", "c")
	out := m.View()
	if !strings.Contains(out, "/ abc") {
		t.Errorf("typed text not shown in the search column:\n%s", out)
	}
	if strings.Contains(out, "search YouTube") {
		t.Error("hint should be replaced by the input")
	}
}

func TestWideQueueUsesTheWholeColumn(t *testing.T) {
	m := at(queueModel(t, 12), 140, 30) // 11 upcoming
	out := m.View()
	if !strings.Contains(out, "Title-e11") || strings.Contains(out, "… and") {
		t.Errorf("all entries should fit in the tall queue column:\n%s", out)
	}
	m = at(queueModel(t, 60), 140, 20) // innerH = 17 rows
	out = m.View()
	if !strings.Contains(out, "… and ") || strings.Contains(out, "Title-e59") {
		t.Errorf("long queue should be cut with '… and N more':\n%s", out)
	}
}

func TestWideQueueCursorScrolls(t *testing.T) {
	m := at(queueModel(t, 40), 140, 20)
	m, _ = send(m, "tab")
	for i := 0; i < 25; i++ {
		m, _ = send(m, "j")
	}
	out := m.View()
	if !strings.Contains(out, "> 26. Title-e26") {
		t.Errorf("cursor row not visible:\n%s", out)
	}
	if strings.Count(out, "> ") != 1 {
		t.Errorf("exactly one cursor expected:\n%s", out)
	}
}

func TestWideResultsStayVisibleWhenUnfocused(t *testing.T) {
	m := at(withResults(t, 3), 140, 30)
	if !strings.Contains(m.View(), "Song A") {
		t.Fatal("results missing")
	}
	m, _ = send(m, "esc")
	if m.mode != modeNormal {
		t.Fatalf("mode = %v", m.mode)
	}
	out := m.View()
	if !strings.Contains(out, "Song A") || !strings.Contains(out, `Results for "x"`) {
		t.Errorf("results should stay in their column:\n%s", out)
	}
}

func TestWideResultsScrollWithSelection(t *testing.T) {
	m := at(withResults(t, 40), 140, 20)
	for i := 0; i < 30; i++ {
		m, _ = send(m, "j")
	}
	want := "> Song " + string(rune('A'+30))
	if out := m.View(); !strings.Contains(out, want) {
		t.Errorf("selected row %q not visible:\n%s", want, out)
	}
}

func TestWideLeftColumnShowsTrackAndProgress(t *testing.T) {
	m := at(queueModel(t, 3), 140, 30)
	m, _ = update(m, positionMsg{elapsed: 83, total: 232})
	out := m.View()
	for _, want := range []string{"Title-e0", "01:23 / 03:52", "36%", strings.Repeat("░", 32)} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestWideCoverDroppedWhenTooShort(t *testing.T) {
	m := at(queueModel(t, 3), 140, 12)
	out := m.View()
	if strings.Contains(out, strings.Repeat("░", 32)) {
		t.Error("cover should be dropped when the column is too short")
	}
	if !strings.Contains(out, "Title-e0") {
		t.Errorf("track info must remain:\n%s", out)
	}
}

func TestWideIdleLeftColumn(t *testing.T) {
	m := at(newTestModel(), 140, 30)
	if out := m.View(); !strings.Contains(out, "Nothing playing") {
		t.Errorf("idle view:\n%s", out)
	}
}

func TestWideLayoutRendersManyEntriesWithoutOverflow(t *testing.T) {
	m := queueModel(t, 5)
	m.tracks["e1"] = yt.Result{ID: "e1", Title: strings.Repeat("very long title ", 20), Duration: 3700}
	m = at(m, 108, 30)
	for i, l := range viewLines(m) {
		if lipgloss.Width(l) > 108 {
			t.Errorf("line %d too wide (%d): %q", i, lipgloss.Width(l), l)
		}
	}
}

// boxStarts are the columns where the three frames begin on the top line.
func boxStarts(t *testing.T, m Model) []int {
	t.Helper()
	var starts []int
	col := 0
	for _, r := range []rune(ansi.Strip(viewLines(m)[0])) {
		if r == '┌' {
			starts = append(starts, col)
		}
		col += lipgloss.Width(string(r))
	}
	return starts
}

func TestColumnsAreEqualThirds(t *testing.T) {
	tests := []struct {
		width int
		want  []int // where the second and third frames start; the first is at 0
	}{
		{108, []int{36, 72}}, // 36 + 36 + 36
		{120, []int{40, 80}}, // 40 + 40 + 40
		{140, []int{46, 92}}, // 46 + 46 + 48: the last column takes the remainder
		{200, []int{66, 132}},
	}
	for _, tt := range tests {
		m := at(queueModel(t, 4), tt.width, 30)
		starts := boxStarts(t, m)
		if len(starts) != 3 || starts[0] != 0 || starts[1] != tt.want[0] || starts[2] != tt.want[1] {
			t.Errorf("width %d: frames start at %v, want [0 %d %d]", tt.width, starts, tt.want[0], tt.want[1])
		}
	}
}

func TestCoverFitsAtTheNarrowestColumnLayoutAndIsCenteredWhenWider(t *testing.T) {
	cover := strings.Repeat("░", 32)
	for _, w := range []int{108, 140, 200} {
		m := at(queueModel(t, 3), w, 30)
		if !strings.Contains(m.View(), cover) {
			t.Errorf("width %d: the 32-column cover does not fit", w)
		}
	}
	// In a wide column the cover is centered in the column's usable width.
	m := at(queueModel(t, 3), 200, 30) // columns of 66: usable 63, cover 32
	for _, l := range viewLines(m) {
		plain := ansi.Strip(l)
		if i := strings.Index(plain, cover); i >= 0 {
			left := len([]rune(plain[:i]))
			if left < 10 || left > 20 {
				t.Errorf("cover starts at column %d, expected it roughly centered (about 16)", left)
			}
			return
		}
	}
	t.Error("cover not found")
}
