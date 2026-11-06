package ui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/PatrickSteil/ytplay/internal/analyze"
	"github.com/PatrickSteil/ytplay/internal/yt"
)

// bpmFakes is an analyzer that records which songs it was asked about.
type bpmFakes struct {
	mu   sync.Mutex
	ids  []string
	fail map[string]bool
	bpm  map[string]float64
}

func (f *bpmFakes) analyze(ctx context.Context, id string, duration float64) (analyze.Result, error) {
	f.mu.Lock()
	f.ids = append(f.ids, id)
	fail, bpm := f.fail[id], f.bpm[id]
	f.mu.Unlock()
	if fail {
		return analyze.Result{}, errors.New("no audio")
	}
	return analyze.Result{BPM: bpm}, nil
}

// asked returns the sorted ids the analyzer was called with.
func (f *bpmFakes) asked() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := slices.Clone(f.ids)
	slices.Sort(out)
	return out
}

func bpmModel(f *bpmFakes) Model {
	m := New(Deps{Player: &fakeActions{}, Analyze: f.analyze})
	m.statsDelay = 0
	m.width, m.height = 140, 40
	for _, id := range []string{"a", "b", "c", "d", "e", "f"} {
		m.tracks[id] = yt.Result{ID: id, Title: strings.ToUpper(id), Channel: "Chan", Duration: 200}
	}
	return m
}

// playing makes ids the playlist with the entry at pos current; entries are
// plain ids ("*" is added for the current one).
func bpmPlaying(t *testing.T, m Model, pos int, ids ...string) (Model, tea.Cmd) {
	t.Helper()
	marked := slices.Clone(ids)
	marked[pos] = "*" + marked[pos]
	m, _ = update(m, plEvent(t, marked...))
	return update(m, propEvent("playlist-pos", string(rune('0'+pos))))
}

// deliver feeds the analysis message of id (taken from msgs) to the model.
func bpmDeliver(t *testing.T, m Model, msgs []tea.Msg, id string) (Model, tea.Cmd) {
	t.Helper()
	for _, msg := range msgs {
		if am, ok := msg.(analysisMsg); ok && am.id == id {
			return update(m, am)
		}
	}
	t.Fatalf("no analysis message for %q in %v", id, msgs)
	return m, nil
}

func TestQueuedSongsAreAnalysedTwoAtATime(t *testing.T) {
	f := &bpmFakes{}
	m := bpmModel(f)
	m, cmd := bpmPlaying(t, m, 0, "a", "b", "c", "d", "e")
	msgs := collect(cmd)
	if got := f.asked(); !slices.Equal(got, []string{"a", "b", "c"}) { // the playing song, then two queued
		t.Fatalf("analysed %v, want the playing song and the next two", got)
	}
	m, cmd = bpmDeliver(t, m, msgs, "b")
	msgs = append(msgs, collect(cmd)...)
	if got := f.asked(); !slices.Equal(got, []string{"a", "b", "c", "d"}) {
		t.Fatalf("after one finished, analysed %v, want d to have started", got)
	}
	m, cmd = bpmDeliver(t, m, msgs, "c")
	collect(cmd)
	if got := f.asked(); !slices.Equal(got, []string{"a", "b", "c", "d", "e"}) {
		t.Errorf("analysed %v, want e to have started", got)
	}
}

func TestAnalysedSongIsNotAnalysedAgainWhenItStartsPlaying(t *testing.T) {
	f := &bpmFakes{bpm: map[string]float64{"b": 128}}
	m := bpmModel(f)
	m, cmd := bpmPlaying(t, m, 0, "a", "b", "c")
	msgs := collect(cmd)
	m, cmd = bpmDeliver(t, m, msgs, "b")
	collect(cmd)

	m, cmd = update(m, propEvent("playlist-pos", "1")) // b starts playing
	collect(cmd)
	if got := f.asked(); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Errorf("analysed %v, want each song once", got)
	}
	if st := m.stats["b"]; !st.hasRes || st.res.BPM != 128 {
		t.Errorf("stats of b = %+v, want its queued result", st)
	}
}

func TestFailedAnalysisIsNotRetriedInALoop(t *testing.T) {
	f := &bpmFakes{fail: map[string]bool{"b": true}}
	m := bpmModel(f)
	m, cmd := bpmPlaying(t, m, 0, "a", "b", "c")
	msgs := collect(cmd)
	m, cmd = bpmDeliver(t, m, msgs, "b")
	collect(cmd)
	m, cmd = bpmPlaying(t, m, 0, "a", "b", "c") // the playlist changes again
	collect(cmd)
	if n := strings.Count(strings.Join(f.asked(), ""), "b"); n != 1 {
		t.Errorf("b was analysed %d times, want 1", n)
	}
	if !m.stats["b"].analysisFailed {
		t.Error("the failure should be remembered")
	}
}

func TestSongsAddedLaterAreAnalysed(t *testing.T) {
	f := &bpmFakes{}
	m := bpmModel(f)
	m, cmd := bpmPlaying(t, m, 0, "a")
	collect(cmd)
	m, cmd = update(m, plEvent(t, "*a", "b"))
	collect(cmd)
	if got := f.asked(); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("analysed %v, want b to be picked up when it was added", got)
	}
}

func TestPlayedSongsAreNotAnalysed(t *testing.T) {
	f := &bpmFakes{}
	m := bpmModel(f)
	_, cmd := bpmPlaying(t, m, 1, "a", "b", "c")
	collect(cmd)
	if got := f.asked(); !slices.Equal(got, []string{"b", "c"}) {
		t.Errorf("analysed %v, want only the playing song and the upcoming one", got)
	}
}

func TestQueuedAnalysisWithoutAnalyzerDoesNothing(t *testing.T) {
	m := New(Deps{Player: &fakeActions{}})
	m.statsDelay = 0
	_, cmd := bpmPlaying(t, m, 0, "a", "b")
	collect(cmd) // must not panic
}

func TestQuittingCancelsQueuedAnalyses(t *testing.T) {
	started := make(chan struct{}, 4)
	cancelled := make(chan struct{}, 4)
	m := New(Deps{Player: &fakeActions{}, Analyze: func(ctx context.Context, id string, d float64) (analyze.Result, error) {
		started <- struct{}{}
		<-ctx.Done()
		cancelled <- struct{}{}
		return analyze.Result{}, ctx.Err()
	}})
	m.statsDelay = 0
	m, cmd := bpmPlaying(t, m, 0, "a", "b", "c")
	go collect(cmd)
	for i := 0; i < 3; i++ { // the playing song and two queued
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d analyses started", i)
		}
	}
	m.quit()
	for i := 0; i < 3; i++ {
		select {
		case <-cancelled:
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d analyses were cancelled by quitting", i)
		}
	}
}

// withBPM records finished analyses so queue entries have a tempo.
func withBPM(m Model, bpms map[string]float64) Model {
	for id, b := range bpms {
		m.stats[id] = trackStats{hasRes: true, res: analyze.Result{BPM: b}}
	}
	return m
}

const watch = "https://www.youtube.com/watch?v="

// reorderCall is what the fake player was asked to do: the ids, in order.
func reorderCall(m Model) string {
	last := lastCall(m)
	if !strings.HasPrefix(last, "reorder ") {
		return ""
	}
	return strings.ReplaceAll(strings.TrimPrefix(last, "reorder "), watch, "")
}

func focusQueue(m Model) Model {
	m, _ = send(m, "tab")
	return m
}

func TestSortByBPMOrdersTheUpcomingSlowestFirst(t *testing.T) {
	m := focusQueue(withBPM(queueModel(t, 5), map[string]float64{"e1": 140, "e2": 90, "e3": 120, "e4": 100}))
	m = edit(m, "b")
	if got := reorderCall(m); got != "e2,e4,e3,e1" {
		t.Errorf("reorder = %q, want e2,e4,e3,e1", got)
	}
	if got := queueIDs(m); got != "e0,e2,e4,e3,e1" {
		t.Errorf("playlist after sorting = %q (the playing song must stay first)", got)
	}
}

func TestSortPutsUnknownBPMLastInTheirOldOrder(t *testing.T) {
	m := queueModel(t, 5)
	m = withBPM(m, map[string]float64{"e2": 130, "e3": 0, "e4": 100}) // e1 unknown, e3 has no steady beat
	m = edit(focusQueue(m), "b")
	if got := reorderCall(m); got != "e4,e2,e1,e3" {
		t.Errorf("reorder = %q, want e4,e2,e1,e3", got)
	}
}

func TestSecondSortPressReverses(t *testing.T) {
	m := focusQueue(withBPM(queueModel(t, 5), map[string]float64{"e1": 140, "e2": 90, "e4": 100})) // e3 unknown
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

func TestManualEditsRestartTheSortDirection(t *testing.T) {
	m := focusQueue(withBPM(queueModel(t, 4), map[string]float64{"e1": 140, "e2": 90, "e3": 100}))
	m = edit(m, "b") // slowest first: e2,e3,e1
	m = edit(m, "x") // remove the selected entry
	m = edit(m, "b")
	if got := reorderCall(m); strings.Contains(got, "e1,e3") { // that would be fastest first
		t.Errorf("after another edit the next sort should be ascending again, got %q", got)
	}
}

func TestSortIsIgnoredWhileAnEditIsRunning(t *testing.T) {
	m := focusQueue(withBPM(queueModel(t, 4), map[string]float64{"e1": 140, "e2": 90, "e3": 100}))
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

func TestSortNeedsTwoSongs(t *testing.T) {
	m := focusQueue(withBPM(queueModel(t, 2), map[string]float64{"e1": 140}))
	m = edit(m, "b")
	if got := reorderCall(m); got != "" {
		t.Errorf("one upcoming song is already sorted, but got %q", got)
	}
}

func TestSortOnlyWorksInTheQueuePanel(t *testing.T) {
	m := withBPM(queueModel(t, 4), map[string]float64{"e1": 140, "e2": 90, "e3": 100})
	m = edit(m, "b") // not focused
	if got := reorderCall(m); got != "" {
		t.Errorf("sorted from outside the queue panel: %q", got)
	}
}

func TestSortStatusMentionsSongsStillBeingAnalysed(t *testing.T) {
	f := &bpmFakes{}
	m := bpmModel(f)
	m, _ = bpmPlaying(t, m, 0, "a", "b", "c", "d", "e")
	m = withBPM(m, map[string]float64{"b": 100})
	m.stats["e"] = trackStats{analysisFailed: true} // c and d are still being analysed
	m, _ = send(m, "tab")
	m, _ = send(m, "b")
	if !strings.Contains(m.status, "2 still being analysed") || !strings.Contains(m.status, "1 without a BPM") {
		t.Errorf("status = %q", m.status)
	}
}

func TestQueueRowShowsTheBPM(t *testing.T) {
	m := withBPM(queueModel(t, 3), map[string]float64{"e1": 123.6})
	rows := m.queueRows(60) // the playing song, e1, e2
	if len(rows) != 3 {
		t.Fatalf("rows = %q", rows)
	}
	if !strings.Contains(ansi.Strip(rows[1]), "124") {
		t.Errorf("row of e1 = %q, want its BPM 124", ansi.Strip(rows[1]))
	}
	if strings.Contains(ansi.Strip(rows[2]), "124") {
		t.Errorf("row of e2 = %q shows a BPM it does not have", ansi.Strip(rows[2]))
	}
	if w1, w2 := lipgloss.Width(rows[1]), lipgloss.Width(rows[2]); w1 != w2 {
		t.Errorf("rows are %d and %d wide, want equal", w1, w2)
	}
}

func TestHelpListsTheSortKey(t *testing.T) {
	if !strings.Contains(queueHelpText, "b sort by BPM") {
		t.Errorf("footer help = %q", queueHelpText)
	}
	m := queueModel(t, 3)
	m.mode = modeHelp
	if !strings.Contains(ansi.Strip(strings.Join(m.helpBox(), "\n")), "sort by BPM") {
		t.Error("the help popup does not mention sorting by BPM")
	}
}
