package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/PatrickSteil/ytplay/internal/analyze"
)

const watch = "https://www.youtube.com/watch?v="

// withBPM records finished analyses so queue entries have a tempo.
func withBPM(m Model, bpms map[string]float64) Model {
	for id, b := range bpms {
		m.stats[id] = trackStats{hasRes: true, res: analyze.Result{BPM: b}}
	}
	return m
}

// reorderCall is what the fake player was asked to do: the ids, in order.
func reorderCall(m Model) string {
	last := lastCall(m)
	if !strings.HasPrefix(last, "reorder ") {
		return ""
	}
	return strings.ReplaceAll(strings.TrimPrefix(last, "reorder "), watch, "")
}

// djText is the popup as plain text.
func djText(m Model) string { return ansi.Strip(strings.Join(m.djBox(), "\n")) }

// djModel has e0 playing and e1..e{n-1} queued, with the DJ popup open.
func djModel(t *testing.T, n int, bpms map[string]float64) Model {
	t.Helper()
	m := withBPM(queueModel(t, n), bpms)
	m, _ = send(m, "D")
	if m.mode != modeDJ {
		t.Fatalf("mode = %v after D", m.mode)
	}
	return m
}

func djRow(m Model, title string) string {
	for _, l := range strings.Split(djText(m), "\n") {
		if strings.Contains(l, title) {
			return l
		}
	}
	return ""
}

func TestDOpensThePopupFromEveryModeAndEscClosesIt(t *testing.T) {
	for name, setup := range map[string]func() Model{
		"normal":  func() Model { return queueModel(t, 3) },
		"results": func() Model { m := queueModel(t, 3); m.results, m.mode = results(3), modeResults; return m },
		"queue":   func() Model { m, _ := send(queueModel(t, 3), "tab"); return m },
	} {
		m := setup()
		before := m.mode
		m, _ = send(m, "D")
		if m.mode != modeDJ {
			t.Errorf("%s: mode = %v after D, want the DJ popup", name, m.mode)
		}
		m, _ = send(m, "esc")
		if m.mode != before {
			t.Errorf("%s: mode = %v after Esc, want %v again", name, m.mode, before)
		}
	}
	m, _ := send(queueModel(t, 3), "D", "D") // D toggles, like h does for the help
	if m.mode == modeDJ {
		t.Error("a second D should close the popup")
	}
}

func TestDJPopupShowsTheTempoOfEverySong(t *testing.T) {
	m := queueModel(t, 5)
	m = withBPM(m, map[string]float64{"e0": 120, "e1": 96.4, "e2": 110})
	m.stats["e4"] = trackStats{analysisFailed: true}
	m.bpmSet["e2"] = 128
	m, _ = send(m, "D")
	out := djText(m)
	for want, row := range map[string]string{
		"120 BPM":  "Title-e0", // the playing song comes first
		"96 BPM":   "Title-e1",
		"128* BPM": "Title-e2", // set by hand: marked, and it wins over the detected 110
	} {
		if r := djRow(m, row); !strings.Contains(r, want) {
			t.Errorf("row of %s = %q, want %q\n%s", row, r, want, out)
		}
	}
	if r := djRow(m, "Title-e4"); !strings.Contains(r, "–") || strings.Contains(r, "BPM") {
		t.Errorf("a song without a BPM should show a dash, got %q", r)
	}
	if !strings.Contains(out, "DJ") {
		t.Errorf("popup has no title:\n%s", out)
	}
}

func TestDJPopupShowsAnalysingWhileTheTempoIsUnknown(t *testing.T) {
	f := &bpmFakes{}
	m := bpmModel(f)
	m, _ = bpmPlaying(t, m, 0, "a", "b", "c")
	m, _ = send(m, "D")
	if r := djRow(m, "B"); !strings.Contains(r, "…") {
		t.Errorf("a song still being analysed should show …, got %q", r)
	}
}

func TestDJPopupFitsSmallTerminalsAndLongQueues(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {60, 16}, {80, 24}, {150, 45}} {
		m := queueModel(t, 40)
		m.width, m.height = size[0], size[1]
		m, _ = send(m, "D")
		box := m.djBox()
		if len(box) > m.height {
			t.Errorf("%dx%d: popup is %d lines tall", size[0], size[1], len(box))
		}
		for i, l := range box {
			if w := lipgloss.Width(l); w > m.width || w != lipgloss.Width(box[0]) {
				t.Errorf("%dx%d: line %d is %d wide (frame %d, terminal %d)", size[0], size[1], i, w, lipgloss.Width(box[0]), m.width)
			}
		}
		if !strings.Contains(m.View(), "DJ") {
			t.Errorf("%dx%d: the popup is not drawn", size[0], size[1])
		}
	}
}

func TestDJPopupFollowsTheCursorThroughALongQueue(t *testing.T) {
	m := queueModel(t, 30)
	m.height = 14
	m, _ = send(m, "D")
	for i := 0; i < 25; i++ {
		m, _ = send(m, "j")
	}
	want := fmt.Sprintf("Title-e%d", 26) // row 1 is e1, so 25 presses reach e26
	if r := djRow(m, want); !strings.Contains(r, ">") {
		t.Errorf("selected row not visible and marked: %q\n%s", r, djText(m))
	}
	for i := 0; i < 40; i++ {
		m, _ = send(m, "k")
	}
	if r := djRow(m, "Title-e0"); r == "" {
		t.Errorf("scrolled back up, the playing song should be visible:\n%s", djText(m))
	}
}

func TestDJPopupWithNothingQueued(t *testing.T) {
	m := newTestModel()
	m, _ = send(m, "D")
	if m.mode != modeDJ {
		t.Fatalf("mode = %v", m.mode)
	}
	out := djText(m)
	if !strings.Contains(out, "Nothing") {
		t.Errorf("popup should say nothing is playing:\n%s", out)
	}
	m, _ = send(m, "b", "e", "j", "k") // must not panic or change anything
	if m.djEditing {
		t.Error("e with nothing selected must not start editing")
	}
}

func TestSortByBPMInThePopupOrdersTheUpcomingSlowestFirst(t *testing.T) {
	m := djModel(t, 5, map[string]float64{"e1": 140, "e2": 90, "e3": 120, "e4": 100})
	m = edit(m, "b")
	if got := reorderCall(m); got != "e2,e4,e3,e1" {
		t.Errorf("reorder = %q, want e2,e4,e3,e1", got)
	}
	if got := queueIDs(m); got != "e0,e2,e4,e3,e1" {
		t.Errorf("playlist after sorting = %q (the playing song must stay first)", got)
	}
}

func TestSortKeepsTheSelectionOnTheSameSong(t *testing.T) {
	m := djModel(t, 5, map[string]float64{"e1": 140, "e2": 90, "e3": 120, "e4": 100}) // cursor starts on e1
	m = edit(m, "b")
	if r := djRow(m, "Title-e1"); !strings.Contains(r, ">") {
		t.Errorf("the selection should follow e1 to its new place: %q\n%s", r, djText(m))
	}
}

func TestSortPutsUnknownBPMLastInTheirOldOrder(t *testing.T) {
	m := djModel(t, 5, map[string]float64{"e2": 130, "e3": 0, "e4": 100}) // e1 unknown, e3 has no steady beat
	m = edit(m, "b")
	if got := reorderCall(m); got != "e4,e2,e1,e3" {
		t.Errorf("reorder = %q, want e4,e2,e1,e3", got)
	}
}

func TestSecondSortPressReverses(t *testing.T) {
	m := djModel(t, 5, map[string]float64{"e1": 140, "e2": 90, "e4": 100}) // e3 unknown
	m = edit(m, "b")
	if got := reorderCall(m); got != "e2,e4,e1,e3" {
		t.Fatalf("first press: %q, want e2,e4,e1,e3", got)
	}
	m = edit(m, "b")
	if got := reorderCall(m); got != "e1,e4,e2,e3" {
		t.Errorf("second press: %q, want fastest first with the unknown last: e1,e4,e2,e3", got)
	}
	m = edit(m, "b")
	if got := reorderCall(m); got != "e2,e4,e1,e3" {
		t.Errorf("third press: %q, want slowest first again", got)
	}
}

func TestSortIsIgnoredWhileAnEditIsRunning(t *testing.T) {
	m := djModel(t, 4, map[string]float64{"e1": 140, "e2": 90, "e3": 100})
	m, first := send(m, "b") // the edit is in flight: its command has not run yet
	_, second := send(m, "b")
	if second != nil {
		t.Error("a second sort started while the first was still in flight")
	}
	m = run(m, first)
	n := 0
	for _, c := range actionsOf(m).calls {
		if strings.HasPrefix(c, "reorder ") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d reorder calls, want 1", n)
	}
}

func TestSortNeedsTwoQueuedSongs(t *testing.T) {
	m := djModel(t, 2, map[string]float64{"e1": 140})
	m = edit(m, "b")
	if got := reorderCall(m); got != "" {
		t.Errorf("one queued song is already sorted, but got %q", got)
	}
}

func TestSortStatusMentionsSongsStillBeingAnalysed(t *testing.T) {
	f := &bpmFakes{}
	m := bpmModel(f)
	m, _ = bpmPlaying(t, m, 0, "a", "b", "c", "d", "e")
	m = withBPM(m, map[string]float64{"b": 100})
	m.stats["e"] = trackStats{analysisFailed: true} // c and d are still being analysed
	m, _ = send(m, "D", "b")
	if !strings.Contains(m.status, "2 still being analysed") || !strings.Contains(m.status, "1 without a BPM") {
		t.Errorf("status = %q", m.status)
	}
}

func TestBDoesNothingOutsideThePopup(t *testing.T) {
	m := withBPM(queueModel(t, 4), map[string]float64{"e1": 140, "e2": 90, "e3": 100})
	m = edit(m, "b")
	m, _ = send(m, "tab")
	m = edit(m, "b") // in the queue panel
	if got := reorderCall(m); got != "" {
		t.Errorf("sorted from outside the DJ popup: %q", got)
	}
}

func TestQueueRowsShowNoBPM(t *testing.T) {
	m := withBPM(queueModel(t, 3), map[string]float64{"e1": 123.6})
	rows := m.queueRows(60)
	if len(rows) != 3 {
		t.Fatalf("rows = %q", rows)
	}
	for _, r := range rows {
		if strings.Contains(ansi.Strip(r), "124") {
			t.Errorf("queue row %q shows a BPM; that belongs in the DJ popup", ansi.Strip(r))
		}
	}
	if w1, w2 := lipgloss.Width(rows[1]), lipgloss.Width(rows[2]); w1 != w2 {
		t.Errorf("rows are %d and %d wide, want equal", w1, w2)
	}
}

func TestESetsTheBPMOfTheSelectedSong(t *testing.T) {
	m := djModel(t, 4, map[string]float64{"e1": 100, "e2": 110, "e3": 120})
	m, _ = send(m, "j") // e2
	m, _ = send(m, "e")
	if !m.djEditing {
		t.Fatal("e should start editing")
	}
	m, _ = send(m, "1", "2", "8", "enter")
	if m.djEditing || m.bpmSet["e2"] != 128 {
		t.Errorf("editing=%v set=%v, want 128 stored for e2", m.djEditing, m.bpmSet)
	}
	if r := djRow(m, "Title-e2"); !strings.Contains(r, "128* BPM") {
		t.Errorf("row = %q, want the BPM set by hand marked with *", r)
	}
	if _, ok := m.bpmSet["e1"]; ok {
		t.Error("only the selected song should change")
	}
}

func TestSetBPMAcceptsDecimalsAndTheEdges(t *testing.T) {
	for in, want := range map[string]float64{"127.5": 127.5, "30": 30, "300": 300} {
		m := djModel(t, 3, nil)
		m, _ = send(m, "e")
		for _, r := range in {
			m, _ = send(m, string(r))
		}
		m, _ = send(m, "enter")
		if got := m.bpmSet["e1"]; got != want {
			t.Errorf("%q stored %v, want %v", in, got, want)
		}
	}
}

func TestInvalidBPMIsRejectedAndEditingContinues(t *testing.T) {
	for _, in := range []string{"5", "29", "301", "400", "1.2.3", "."} {
		m := djModel(t, 3, nil)
		m, _ = send(m, "e")
		for _, r := range in {
			m, _ = send(m, string(r))
		}
		m, _ = send(m, "enter")
		if _, ok := m.bpmSet["e1"]; ok {
			t.Errorf("%q was stored", in)
		}
		if !m.djEditing || m.djWarn == "" {
			t.Errorf("%q: editing=%v warn=%q, want the popup to stay in editing with a warning", in, m.djEditing, m.djWarn)
		}
		if !strings.Contains(djText(m), m.djWarn) {
			t.Errorf("%q: the warning is not shown in the popup", in)
		}
	}
}

func TestTypingLettersWhileSettingTheBPMIsIgnored(t *testing.T) {
	m := djModel(t, 3, nil)
	m, _ = send(m, "e", "a", "b", "1", "x", "2", "0", "enter")
	if got := m.bpmSet["e1"]; got != 120 {
		t.Errorf("stored %v, want 120 (letters ignored)", got)
	}
}

func TestEmptyBPMClearsWhatWasSetByHand(t *testing.T) {
	m := djModel(t, 3, map[string]float64{"e1": 100})
	m.bpmSet["e1"] = 128
	m, _ = send(m, "e", "enter")
	if _, ok := m.bpmSet["e1"]; ok {
		t.Error("an empty entry should remove the BPM set by hand")
	}
	if r := djRow(m, "Title-e1"); !strings.Contains(r, "100 BPM") || strings.Contains(r, "*") {
		t.Errorf("row = %q, want the detected 100 back", r)
	}
}

func TestEscCancelsSettingTheBPM(t *testing.T) {
	m := djModel(t, 3, nil)
	m, _ = send(m, "e", "1", "2", "esc")
	if m.djEditing || m.mode != modeDJ {
		t.Errorf("editing=%v mode=%v, want back in the popup, not editing", m.djEditing, m.mode)
	}
	if len(m.bpmSet) != 0 {
		t.Errorf("cancelled edit stored %v", m.bpmSet)
	}
	m, _ = send(m, "esc")
	if m.mode == modeDJ {
		t.Error("a second Esc should close the popup")
	}
}

func TestKeysDoNotLeakOutOfTheEditField(t *testing.T) {
	m := djModel(t, 3, nil)
	m, _ = send(m, "e", "s", "q", "b", "j", "h") // skip, queue, sort, ...: all just ignored text
	if got := actionsOf(m).calls; len(got) != 0 {
		t.Errorf("player was called while typing: %q", got)
	}
}

func TestSetBPMWorksForASongWithoutADetectedTempo(t *testing.T) {
	m := djModel(t, 4, map[string]float64{"e1": 100, "e3": 140}) // e2 has no tempo
	m, _ = send(m, "j", "e", "1", "2", "0", "enter")             // e2 -> 120
	m = edit(m, "b")
	if got := reorderCall(m); got != "e1,e2,e3" {
		t.Errorf("reorder = %q, want the song set by hand sorted in: e1,e2,e3", got)
	}
}

func TestSortUsesTheBPMSetByHand(t *testing.T) {
	m := djModel(t, 4, map[string]float64{"e1": 140, "e2": 100, "e3": 120})
	m, _ = send(m, "e", "8", "0", "enter") // e1 detected 140, set to 80
	m = edit(m, "b")
	if got := reorderCall(m); got != "e1,e2,e3" {
		t.Errorf("reorder = %q, want e1 first after being set to 80", got)
	}
}

func TestSettingABPMRestartsTheSortDirection(t *testing.T) {
	m := djModel(t, 4, map[string]float64{"e1": 140, "e2": 90, "e3": 100})
	m = edit(m, "b")                       // slowest first: e2, e3, e1 (the selection follows e1); the next press would reverse
	m, _ = send(m, "e", "9", "5", "enter") // e1 is now 95
	m = edit(m, "b")
	if got := reorderCall(m); got != "e2,e1,e3" {
		t.Errorf("reorder = %q, want slowest first again: e2,e1,e3", got)
	}
}

func TestTheBPMOfThePlayingSongCanBeSetToo(t *testing.T) {
	m := djModel(t, 3, map[string]float64{"e0": 100})
	m, _ = send(m, "k") // up to the playing song
	m, _ = send(m, "e", "1", "2", "5", "enter")
	if m.bpmSet["e0"] != 125 {
		t.Fatalf("set = %v, want 125 for the playing song", m.bpmSet)
	}
	m, _ = send(m, "esc")
	if line := ansi.Strip(m.bpmKeyLine()); !strings.Contains(line, "125 BPM") {
		t.Errorf("the now-playing line shows %q, want the BPM set by hand", line)
	}
}

func TestNowPlayingLineStillShowsTheDetectedBPM(t *testing.T) {
	f := &bpmFakes{}
	m := bpmModel(f)
	m, _ = bpmPlaying(t, m, 0, "a", "b")
	m = withBPM(m, map[string]float64{"a": 128})
	if line := ansi.Strip(m.bpmKeyLine()); !strings.Contains(line, "128 BPM") {
		t.Errorf("line = %q, want the detected BPM without opening the DJ popup", line)
	}
}

func TestHelpListsTheDJKey(t *testing.T) {
	if !strings.Contains(helpText, "D dj") {
		t.Errorf("footer help = %q", helpText)
	}
	if strings.Contains(queueHelpText, "BPM") {
		t.Errorf("the queue panel's help still mentions BPM: %q", queueHelpText)
	}
	m := queueModel(t, 3)
	m.mode = modeHelp
	out := ansi.Strip(strings.Join(m.helpBox(), "\n"))
	if !strings.Contains(out, "DJ") {
		t.Error("the help popup does not mention the DJ popup")
	}
	if strings.Contains(out, "sort by BPM, again reverses") {
		t.Error("the help popup still lists the old queue-panel sort key")
	}
}

func TestSortKeepsTheSelectionOnASongThatMovesUp(t *testing.T) {
	m := djModel(t, 4, map[string]float64{"e1": 140, "e2": 130, "e3": 90})
	m, _ = send(m, "j", "j") // e3, which the sort moves to the front
	m = edit(m, "b")
	if r := djRow(m, "Title-e3"); !strings.Contains(r, ">") {
		t.Errorf("the selection should follow e3 to the front: %q\n%s", r, djText(m))
	}
	if r := djRow(m, "Title-e1"); strings.Contains(r, ">") {
		t.Errorf("the selection ended up on e1: %q", r)
	}
}

func TestMovingSongsByHandRestartsTheSortDirection(t *testing.T) {
	m := djModel(t, 4, map[string]float64{"e1": 140, "e2": 90, "e3": 100})
	m = edit(m, "b") // slowest first; the next press would reverse
	m, _ = send(m, "esc", "tab")
	m = edit(m, "down") // move a song in the queue panel
	m, _ = send(m, "esc", "D")
	m = edit(m, "b")
	if got := reorderCall(m); got != "e2,e3,e1" {
		t.Errorf("reorder = %q, want slowest first again: e2,e3,e1", got)
	}
}
