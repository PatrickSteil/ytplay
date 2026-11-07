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

// openDJ presses D and returns what that started.
func openDJ(t *testing.T, m Model) (Model, tea.Cmd) {
	t.Helper()
	m, cmd := send(m, "D")
	if m.mode != modeDJ {
		t.Fatalf("mode = %v after D, want the DJ popup", m.mode)
	}
	return m, cmd
}

func TestQueuedSongsAreNotAnalysedUntilTheDJPopupIsOpened(t *testing.T) {
	f := &bpmFakes{}
	m := bpmModel(f)
	m, cmd := bpmPlaying(t, m, 0, "a", "b", "c", "d")
	collect(cmd)
	m, cmd = update(m, plEvent(t, "*a", "b", "c", "d", "e")) // the queue changes too
	collect(cmd)
	if got := f.asked(); !slices.Equal(got, []string{"a"}) {
		t.Errorf("analysed %v, want only the playing song until someone opens the DJ popup", got)
	}
}

func TestQueuedSongsAreAnalysedTwoAtATime(t *testing.T) {
	f := &bpmFakes{}
	m := bpmModel(f)
	m, cmd := bpmPlaying(t, m, 0, "a", "b", "c", "d", "e")
	msgs := collect(cmd)
	m, cmd = openDJ(t, m)
	msgs = append(msgs, collect(cmd)...)
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
	m, cmd = openDJ(t, m)
	msgs = append(msgs, collect(cmd)...)
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
	m, cmd = openDJ(t, m)
	msgs = append(msgs, collect(cmd)...)
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

func TestSongsAddedAfterTheDJPopupWasOpenedAreAnalysed(t *testing.T) {
	f := &bpmFakes{}
	m := bpmModel(f)
	m, cmd := bpmPlaying(t, m, 0, "a")
	collect(cmd)
	m, cmd = openDJ(t, m)
	collect(cmd)
	m, _ = send(m, "esc") // closing the popup does not switch the analysis off
	m, cmd = update(m, plEvent(t, "*a", "b"))
	collect(cmd)
	if got := f.asked(); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("analysed %v, want b to be picked up when it was added", got)
	}
}

func TestPlayedSongsAreNotAnalysed(t *testing.T) {
	f := &bpmFakes{}
	m := bpmModel(f)
	m, cmd := bpmPlaying(t, m, 1, "a", "b", "c")
	collect(cmd)
	_, cmd = openDJ(t, m)
	collect(cmd)
	if got := f.asked(); !slices.Equal(got, []string{"b", "c"}) {
		t.Errorf("analysed %v, want only the playing song and the upcoming one", got)
	}
}

func TestQueuedAnalysisWithoutAnalyzerDoesNothing(t *testing.T) {
	m := New(Deps{Player: &fakeActions{}})
	m.statsDelay = 0
	m, cmd := bpmPlaying(t, m, 0, "a", "b")
	collect(cmd)
	_, cmd = send(m, "D")
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
	m, cmd = send(m, "D")
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
