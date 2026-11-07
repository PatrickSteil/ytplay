package ui

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/PatrickSteil/ytplay/internal/analyze"
	"github.com/PatrickSteil/ytplay/internal/autodj"
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
	m, _ = typeCommand(m, "dj")
	if m.mode != modeDJ {
		t.Fatalf("mode = %v after :dj", m.mode)
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

func TestDJOpensThePopupFromEveryModeAndEscClosesIt(t *testing.T) {
	for name, setup := range map[string]func() Model{
		"normal":  func() Model { return queueModel(t, 3) },
		"results": func() Model { m := queueModel(t, 3); m.results, m.mode = results(3), modeResults; return m },
		"queue":   func() Model { m, _ := send(queueModel(t, 3), "tab"); return m },
	} {
		m := setup()
		before := m.mode
		m, _ = typeCommand(m, "dj")
		if m.mode != modeDJ {
			t.Errorf("%s: mode = %v after :dj, want the DJ popup", name, m.mode)
		}
		m, _ = send(m, "esc")
		if m.mode != before {
			t.Errorf("%s: mode = %v after Esc, want %v again", name, m.mode, before)
		}
	}
	m, _ := typeCommand(queueModel(t, 3), "dj")
	m, _ = typeCommand(m, "dj") // :dj toggles, like :help does for the help
	if m.mode == modeDJ {
		t.Error("a second :dj should close the popup")
	}
}

func TestDJPopupShowsTheTempoOfEverySong(t *testing.T) {
	m := queueModel(t, 5)
	m = withBPM(m, map[string]float64{"e0": 120, "e1": 96.4, "e2": 110})
	m.stats["e4"] = trackStats{analysisFailed: true}
	m.bpmSet["e2"] = 128
	m, _ = typeCommand(m, "dj")
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
	m, _ = typeCommand(m, "dj")
	if r := djRow(m, "B"); !strings.Contains(r, "…") {
		t.Errorf("a song still being analysed should show …, got %q", r)
	}
}

func TestDJPopupFitsSmallTerminalsAndLongQueues(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {60, 16}, {80, 24}, {150, 45}} {
		m := queueModel(t, 40)
		m.width, m.height = size[0], size[1]
		m, _ = typeCommand(m, "dj")
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
	m, _ = typeCommand(m, "dj")
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
	m, _ = typeCommand(m, "dj")
	if m.mode != modeDJ {
		t.Fatalf("mode = %v", m.mode)
	}
	out := djText(m)
	if !strings.Contains(out, "Nothing") {
		t.Errorf("popup should say nothing is playing:\n%s", out)
	}
	m, _ = typeCommand(m, "sort") // must not panic or change anything
	m, _ = typeCommand(m, "bpm 120")
	m, _ = send(m, "j", "k")
	if len(m.bpmSet) != 0 {
		t.Errorf(":bpm with nothing selected set %v", m.bpmSet)
	}
}

func TestSortByBPMInThePopupOrdersTheUpcomingSlowestFirst(t *testing.T) {
	m := djModel(t, 5, map[string]float64{"e1": 140, "e2": 90, "e3": 120, "e4": 100})
	m = editCommand(m, "sort")
	if got := reorderCall(m); got != "e2,e4,e3,e1" {
		t.Errorf("reorder = %q, want e2,e4,e3,e1", got)
	}
	if got := queueIDs(m); got != "e0,e2,e4,e3,e1" {
		t.Errorf("playlist after sorting = %q (the playing song must stay first)", got)
	}
}

func TestSortKeepsTheSelectionOnTheSameSong(t *testing.T) {
	m := djModel(t, 5, map[string]float64{"e1": 140, "e2": 90, "e3": 120, "e4": 100}) // cursor starts on e1
	m = editCommand(m, "sort")
	if r := djRow(m, "Title-e1"); !strings.Contains(r, ">") {
		t.Errorf("the selection should follow e1 to its new place: %q\n%s", r, djText(m))
	}
}

func TestSortPutsUnknownBPMLastInTheirOldOrder(t *testing.T) {
	m := djModel(t, 5, map[string]float64{"e2": 130, "e3": 0, "e4": 100}) // e1 unknown, e3 has no steady beat
	m = editCommand(m, "sort")
	if got := reorderCall(m); got != "e4,e2,e1,e3" {
		t.Errorf("reorder = %q, want e4,e2,e1,e3", got)
	}
}

func TestSecondSortPressReverses(t *testing.T) {
	m := djModel(t, 5, map[string]float64{"e1": 140, "e2": 90, "e4": 100}) // e3 unknown
	m = editCommand(m, "sort")
	if got := reorderCall(m); got != "e2,e4,e1,e3" {
		t.Fatalf("first press: %q, want e2,e4,e1,e3", got)
	}
	m = editCommand(m, "sort")
	if got := reorderCall(m); got != "e1,e4,e2,e3" {
		t.Errorf("second press: %q, want fastest first with the unknown last: e1,e4,e2,e3", got)
	}
	m = editCommand(m, "sort")
	if got := reorderCall(m); got != "e2,e4,e1,e3" {
		t.Errorf("third press: %q, want slowest first again", got)
	}
}

func TestSortIsIgnoredWhileAnEditIsRunning(t *testing.T) {
	m := djModel(t, 4, map[string]float64{"e1": 140, "e2": 90, "e3": 100})
	m, first := typeCommand(m, "sort") // the edit is in flight: its command has not run yet
	_, second := typeCommand(m, "sort")
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
	m = editCommand(m, "sort")
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
	m, _ = typeCommand(m, "dj")
	m, _ = typeCommand(m, "sort")
	if !strings.Contains(m.status, "2 still being analysed") || !strings.Contains(m.status, "1 without a BPM") {
		t.Errorf("status = %q", m.status)
	}
}

func TestSortDoesNothingOutsideThePopup(t *testing.T) {
	m := withBPM(queueModel(t, 4), map[string]float64{"e1": 140, "e2": 90, "e3": 100})
	m = editCommand(m, "sort")
	m, _ = send(m, "tab")
	m = editCommand(m, "sort") // in the queue panel
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

func TestBPMSetsTheBPMOfTheSelectedSong(t *testing.T) {
	m := djModel(t, 4, map[string]float64{"e1": 100, "e2": 110, "e3": 120})
	m, _ = send(m, "j") // e2
	m, _ = typeCommand(m, "bpm 128")
	if m.mode != modeDJ || m.bpmSet["e2"] != 128 {
		t.Errorf("mode=%v set=%v, want 128 stored for e2 and the popup still open", m.mode, m.bpmSet)
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
		m, _ = typeCommand(m, "bpm "+in)
		if got := m.bpmSet["e1"]; got != want {
			t.Errorf("%q stored %v, want %v", in, got, want)
		}
	}
}

func TestInvalidBPMIsRejectedAndTheStatusSaysWhy(t *testing.T) {
	for _, in := range []string{"5", "29", "301", "400", "1.2.3", ".", "abc", "12x"} {
		m := djModel(t, 3, nil)
		m, _ = typeCommand(m, "bpm "+in)
		if _, ok := m.bpmSet["e1"]; ok {
			t.Errorf("%q was stored", in)
		}
		if m.mode != modeDJ || !m.statusIsErr || m.status == "" {
			t.Errorf("%q: mode=%v status=%q (error %v), want the popup to stay open and an error in the status line", in, m.mode, m.status, m.statusIsErr)
		}
	}
}

func TestAnEscapedCommandLineSetsNoBPM(t *testing.T) {
	m := djModel(t, 3, nil)
	m, _ = send(m, ":", "b", "p", "m", " ", "1", "2", "esc")
	if len(m.bpmSet) != 0 || m.mode != modeDJ {
		t.Errorf("cancelled command stored %v, mode %v, want nothing and the popup", m.bpmSet, m.mode)
	}
	m, _ = send(m, "esc")
	if m.mode == modeDJ {
		t.Error("a second Esc should close the popup")
	}
}

func TestBPMAloneClearsWhatWasSetByHand(t *testing.T) {
	m := djModel(t, 3, map[string]float64{"e1": 100})
	m.bpmSet["e1"] = 128
	m, _ = typeCommand(m, "bpm")
	if _, ok := m.bpmSet["e1"]; ok {
		t.Error(":bpm alone should remove the BPM set by hand")
	}
	if r := djRow(m, "Title-e1"); !strings.Contains(r, "100 BPM") || strings.Contains(r, "*") {
		t.Errorf("row = %q, want the detected 100 back", r)
	}
}

func TestSetBPMWorksForASongWithoutADetectedTempo(t *testing.T) {
	m := djModel(t, 4, map[string]float64{"e1": 100, "e3": 140}) // e2 has no tempo
	m, _ = send(m, "j")
	m, _ = typeCommand(m, "bpm 120") // e2 -> 120
	m = editCommand(m, "sort")
	if got := reorderCall(m); got != "e1,e2,e3" {
		t.Errorf("reorder = %q, want the song set by hand sorted in: e1,e2,e3", got)
	}
}

func TestSortUsesTheBPMSetByHand(t *testing.T) {
	m := djModel(t, 4, map[string]float64{"e1": 140, "e2": 100, "e3": 120})
	m, _ = typeCommand(m, "bpm 80") // e1 detected 140, set to 80
	m = editCommand(m, "sort")
	if got := reorderCall(m); got != "e1,e2,e3" {
		t.Errorf("reorder = %q, want e1 first after being set to 80", got)
	}
}

func TestSettingABPMRestartsTheSortDirection(t *testing.T) {
	m := djModel(t, 4, map[string]float64{"e1": 140, "e2": 90, "e3": 100})
	m = editCommand(m, "sort")      // slowest first: e2, e3, e1 (the selection follows e1); the next press would reverse
	m, _ = typeCommand(m, "bpm 95") // e1 is now 95
	m = editCommand(m, "sort")
	if got := reorderCall(m); got != "e2,e1,e3" {
		t.Errorf("reorder = %q, want slowest first again: e2,e1,e3", got)
	}
}

func TestTheBPMOfThePlayingSongCanBeSetToo(t *testing.T) {
	m := djModel(t, 3, map[string]float64{"e0": 100})
	m, _ = send(m, "k") // up to the playing song
	m, _ = typeCommand(m, "bpm 125")
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

func TestHelpListsTheDJCommand(t *testing.T) {
	m := queueModel(t, 3)
	m.height = 100 // the help scrolls on a small terminal; this is about what it says
	m.mode = modeHelp
	out := ansi.Strip(strings.Join(m.helpBox(), "\n"))
	for _, want := range []string{":dj", ":sort", ":bpm", ":auto"} {
		if !strings.Contains(out, want) {
			t.Errorf("the help popup does not list %s:\n%s", want, out)
		}
	}
	q, _ := send(queueModel(t, 3), "tab")
	if strings.Contains(q.help(), "BPM") {
		t.Errorf("the queue panel's hints mention BPM: %q", q.help())
	}
}

func TestSortKeepsTheSelectionOnASongThatMovesUp(t *testing.T) {
	m := djModel(t, 4, map[string]float64{"e1": 140, "e2": 130, "e3": 90})
	m, _ = send(m, "j", "j") // e3, which the sort moves to the front
	m = editCommand(m, "sort")
	if r := djRow(m, "Title-e3"); !strings.Contains(r, ">") {
		t.Errorf("the selection should follow e3 to the front: %q\n%s", r, djText(m))
	}
	if r := djRow(m, "Title-e1"); strings.Contains(r, ">") {
		t.Errorf("the selection ended up on e1: %q", r)
	}
}

func TestMovingSongsByHandRestartsTheSortDirection(t *testing.T) {
	m := djModel(t, 4, map[string]float64{"e1": 140, "e2": 90, "e3": 100})
	m = editCommand(m, "sort") // slowest first; the next press would reverse
	m, _ = send(m, "esc", "tab")
	m = edit(m, "down") // move a song in the queue panel
	m, _ = send(m, "esc")
	m, _ = typeCommand(m, "dj")
	m = editCommand(m, "sort")
	if got := reorderCall(m); got != "e2,e3,e1" {
		t.Errorf("reorder = %q, want slowest first again: e2,e3,e1", got)
	}
}

// ---- autodj ----

// autoRecorder stands for the player's SetAutoDJ.
type autoRecorder struct {
	mu    sync.Mutex
	calls []bool
	songs func(url string) (autodj.Song, bool)
}

func (r *autoRecorder) set(on bool, songs func(url string) (autodj.Song, bool)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls, r.songs = append(r.calls, on), songs
}

func (r *autoRecorder) last() (bool, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) == 0 {
		return false, false
	}
	return r.calls[len(r.calls)-1], true
}

// autoModel is djModel's queue with a player that can do autodj.
func autoModel(t *testing.T, n int) (Model, *autoRecorder) {
	t.Helper()
	m := queueModel(t, n)
	rec := &autoRecorder{}
	m.deps.SetAutoDJ = rec.set
	m, _ = typeCommand(m, "dj")
	return m, rec
}

func sureSongOf(length, audioEnd, period, startAnchor, endAnchor float64) autodj.Song {
	return autodj.Song{Length: length, Edges: analyze.Edges{
		AudioStart: 0.1, AudioEnd: audioEnd,
		Start: analyze.BeatGrid{Period: period, Anchor: startAnchor, Sure: true},
		End:   analyze.BeatGrid{Period: period, Anchor: endAnchor, Sure: true},
	}}
}

func TestAutoDJStartsOff(t *testing.T) {
	m, rec := autoModel(t, 3)
	if m.autoDJ {
		t.Error("autodj must be off until the user switches it on")
	}
	if _, called := rec.last(); called {
		t.Error("the player was told about autodj before anyone asked")
	}
	if out := djText(m); !strings.Contains(out, "autodj off") {
		t.Errorf("the popup should say autodj is off:\n%s", out)
	}
}

func TestAutoCommandTogglesAutoDJ(t *testing.T) {
	m, rec := autoModel(t, 3)
	m = editCommand(m, "auto")
	if on, called := rec.last(); !called || !on || !m.autoDJ {
		t.Fatalf("after :auto: model %v, player told %v (called %v), want on", m.autoDJ, on, called)
	}
	if out := djText(m); !strings.Contains(out, "autodj on") {
		t.Errorf("the popup should say autodj is on:\n%s", out)
	}
	m = editCommand(m, "auto")
	if on, _ := rec.last(); on || m.autoDJ {
		t.Errorf("after a second :auto: model %v, player told %v, want off", m.autoDJ, on)
	}
	if len(rec.calls) != 2 {
		t.Errorf("the player was told %d times, want 2", len(rec.calls))
	}
}

func TestAutoDJStaysOnWhenThePopupCloses(t *testing.T) {
	m, rec := autoModel(t, 3)
	m = editCommand(m, "auto")
	m, _ = send(m, "esc")
	if !m.autoDJ {
		t.Error("closing the popup must not switch autodj off")
	}
	if on, _ := rec.last(); !on {
		t.Error("nor must it tell the player to stop")
	}
}

func TestAutoDJNeedsThePlayerToSupportIt(t *testing.T) {
	m := queueModel(t, 3) // no SetAutoDJ in the deps
	m, _ = typeCommand(m, "dj")
	m, _ = typeCommand(m, "auto")
	if m.autoDJ {
		t.Error("autodj cannot be on without a player that can do it")
	}
	if !strings.Contains(m.status, "not available") {
		t.Errorf("status = %q, want to be told it is not available", m.status)
	}
}

func TestSongsAnalysedForTheDJReachThePlayer(t *testing.T) {
	m, rec := autoModel(t, 3)
	m = editCommand(m, "auto")
	want := analyze.Result{BPM: 124, Edges: sureSongOf(200, 199.5, 0.5, 0.3, 199).Edges}
	m, _ = update(m, analysisMsg{id: "e1", res: want})
	songs := rec.songs
	if songs == nil {
		t.Fatal("the player was given no way to look songs up")
	}
	got, ok := songs("https://www.youtube.com/watch?v=e1")
	if !ok || got.Edges != want.Edges {
		t.Errorf("lookup of e1 = %+v, %v; want its edges %+v", got, ok, want.Edges)
	}
	if _, ok := songs("https://www.youtube.com/watch?v=e2"); ok {
		t.Error("a song that was never analysed must not be found")
	}
	if _, ok := songs("not a youtube url"); ok {
		t.Error("an unknown URL must not be found")
	}
}

func TestAFailedAnalysisIsNotInThePlayersLookup(t *testing.T) {
	m, rec := autoModel(t, 3)
	m = editCommand(m, "auto")
	m, _ = update(m, analysisMsg{id: "e1", err: errors.New("no audio")})
	if _, ok := rec.songs("https://www.youtube.com/watch?v=e1"); ok {
		t.Error("a failed analysis must not appear as an analysed song")
	}
}

func TestPopupShowsHowTheNextTransitionWillBe(t *testing.T) {
	m, _ := autoModel(t, 4)
	m = editCommand(m, "auto")
	if out := djText(m); !strings.Contains(out, "not analysed") {
		t.Errorf("unanalysed songs: the popup should say so:\n%s", out)
	}
	m.songs.put("e0", sureSongOf(200, 199.6, 0.5, 0.3, 199.2))
	m.songs.put("e1", sureSongOf(180, 175, 0.48, 0.3, 174.8))
	m.songs.put("e2", autodj.Song{Length: 180, Edges: analyze.Edges{AudioEnd: 179}})
	if out := djText(m); !strings.Contains(out, "beat-matched") {
		t.Errorf("two songs with clear beats: the popup should say beat-matched:\n%s", out)
	}
	m, _ = update(m, propEvent("playlist-pos", "1")) // now e1 plays, e2 is next: e2 has no beat
	if out := djText(m); !strings.Contains(out, "crossfade") {
		t.Errorf("a next song without a beat: the popup should say crossfade:\n%s", out)
	}
}

func TestPopupSaysWhenNothingIsQueued(t *testing.T) {
	m, _ := autoModel(t, 1)
	m = editCommand(m, "auto")
	if out := djText(m); !strings.Contains(out, "nothing queued") {
		t.Errorf("popup:\n%s", out)
	}
}

func TestPopupListsTheAutoDJCommand(t *testing.T) {
	m, _ := autoModel(t, 3)
	if out := djText(m); !strings.Contains(out, ":auto") {
		t.Errorf("the popup should list :auto:\n%s", out)
	}
	m.mode, m.height = modeHelp, 100
	if out := ansi.Strip(strings.Join(m.helpBox(), "\n")); !strings.Contains(out, "autodj") {
		t.Errorf("the help popup should mention autodj:\n%s", out)
	}
}
